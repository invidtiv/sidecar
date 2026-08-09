package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path"
	"strings"
	"time"
)

// PRIdentity is the stable identity Sidecar uses after a pull request is found
// or created. Branch names are display/routing data; Number and NodeID identify
// the pull request across checkout and branch changes.
type PRIdentity struct {
	Number     int    `json:"number"`
	URL        string `json:"url"`
	NodeID     string `json:"id"`
	Repository string `json:"repository,omitempty"`
	HeadRef    string `json:"headRefName"`
	HeadOwner  string `json:"headOwner,omitempty"`
	HeadRepo   string `json:"headRepository,omitempty"`
	HeadOID    string `json:"headRefOid,omitempty"`
	BaseRef    string `json:"baseRefName"`
	State      string `json:"state,omitempty"`
	MergedAt   string `json:"mergedAt,omitempty"`
	MergeOID   string `json:"mergeCommitOid,omitempty"`
}

type ghRepository struct {
	NameWithOwner string `json:"nameWithOwner"`
}

type ghOwner struct {
	Login string `json:"login"`
}

type ghCommit struct {
	OID string `json:"oid"`
}

type ghPR struct {
	Number              int          `json:"number"`
	URL                 string       `json:"url"`
	ID                  string       `json:"id"`
	HeadRefName         string       `json:"headRefName"`
	HeadRefOID          string       `json:"headRefOid"`
	BaseRefName         string       `json:"baseRefName"`
	State               string       `json:"state"`
	MergedAt            string       `json:"mergedAt"`
	HeadRepository      ghRepository `json:"headRepository"`
	HeadRepositoryOwner ghOwner      `json:"headRepositoryOwner"`
	MergeCommit         ghCommit     `json:"mergeCommit"`
}

func (p ghPR) identity(repository string) PRIdentity {
	return PRIdentity{
		Number: p.Number, URL: p.URL, NodeID: p.ID, Repository: repository,
		HeadRef: p.HeadRefName, HeadOwner: p.HeadRepositoryOwner.Login,
		HeadRepo: p.HeadRepository.NameWithOwner, HeadOID: p.HeadRefOID,
		BaseRef: p.BaseRefName, State: strings.ToUpper(p.State),
		MergedAt: p.MergedAt, MergeOID: p.MergeCommit.OID,
	}
}

type PRPollKind string

const (
	PRPollOpen        PRPollKind = "open"
	PRPollMerged      PRPollKind = "merged"
	PRPollClosed      PRPollKind = "closed"
	PRPollAuth        PRPollKind = "authentication"
	PRPollRepository  PRPollKind = "repository-mismatch"
	PRPollNetwork     PRPollKind = "network-unavailable"
	PRPollUnavailable PRPollKind = "unavailable"
)

var errReviewedSourceChanged = errors.New("reviewed source changed")

func revalidateReviewedPushContext(ctx context.Context, dir, remote, branch, reviewedOID string) error {
	if remote == "" || branch == "" || reviewedOID == "" {
		return fmt.Errorf("%w: reviewed push identity is incomplete", errReviewedSourceChanged)
	}
	head, err := gitOutputContext(ctx, dir, "rev-parse", "HEAD")
	if err != nil || head != reviewedOID {
		return fmt.Errorf("%w: local HEAD is %s, expected %s", errReviewedSourceChanged, head, reviewedOID)
	}
	remoteOID, err := remoteBranchOIDContext(ctx, dir, remote, branch)
	if err != nil {
		return err
	}
	if remoteOID != reviewedOID {
		return fmt.Errorf("%w: remote %s/%s is %s, expected %s", errReviewedSourceChanged, remote, branch, remoteOID, reviewedOID)
	}
	// Make the local check adjacent to the create call as well; edits can occur
	// while the remote query is in flight.
	head, err = gitOutputContext(ctx, dir, "rev-parse", "HEAD")
	if err != nil || head != reviewedOID {
		return fmt.Errorf("%w: local HEAD is %s, expected %s", errReviewedSourceChanged, head, reviewedOID)
	}
	return nil
}

type PRPollResult struct {
	Kind                PRPollKind
	Identity            PRIdentity
	Err                 error
	ForceDeleteRequired bool
}

func validateMergedPRForCleanupContext(ctx context.Context, dir, reviewedOID, expectedBase string, identity PRIdentity) (bool, error) {
	if identity.State != "MERGED" || identity.MergeOID == "" {
		return false, errors.New("GitHub did not return a merged commit OID")
	}
	if identity.BaseRef != expectedBase {
		return false, fmt.Errorf("pull request base changed from %q to %q", expectedBase, identity.BaseRef)
	}
	if identity.HeadOID != reviewedOID {
		return false, fmt.Errorf("pull request head changed from reviewed %s to %s", reviewedOID, identity.HeadOID)
	}
	head, err := gitOutputContext(ctx, dir, "rev-parse", "HEAD")
	if err != nil || head != reviewedOID {
		return false, errors.New("workspace HEAD changed after review")
	}
	remote, err := resolveBranchRemoteContext(ctx, dir, expectedBase)
	if err != nil {
		return false, err
	}
	if _, err := gitOutputContext(ctx, dir, "fetch", remote, expectedBase); err != nil {
		return false, fmt.Errorf("fetch merged base: %w", err)
	}
	baseRef := "refs/remotes/" + remote + "/" + expectedBase
	if _, err := gitOutputContext(ctx, dir, "merge-base", "--is-ancestor", identity.MergeOID, baseRef); err != nil {
		return false, fmt.Errorf("merged OID %s is not present on %s/%s", identity.MergeOID, remote, expectedBase)
	}
	_, ancestorErr := gitOutputContext(ctx, dir, "merge-base", "--is-ancestor", reviewedOID, baseRef)
	return ancestorErr != nil, nil
}

func classifyGHError(output string, err error) PRPollKind {
	if err == nil {
		return ""
	}
	s := strings.ToLower(output + " " + err.Error())
	switch {
	case strings.Contains(s, "auth"), strings.Contains(s, "login"), strings.Contains(s, "401"), strings.Contains(s, "403"):
		return PRPollAuth
	case strings.Contains(s, "could not resolve host"), strings.Contains(s, "network"), strings.Contains(s, "connection"), strings.Contains(s, "timeout"):
		return PRPollNetwork
	case strings.Contains(s, "not found"), strings.Contains(s, "could not resolve to a repository"), strings.Contains(s, "repository"):
		return PRPollRepository
	default:
		return PRPollUnavailable
	}
}

func ghOutputContext(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("gh %s: %s: %w", strings.Join(args, " "), msg, err)
	}
	return out, nil
}

func parseGitHubRepositoryURL(remoteURL string) (string, error) {
	raw := strings.TrimSpace(remoteURL)
	if raw == "" {
		return "", errors.New("remote URL is empty")
	}
	var host, repoPath string
	if before, after, ok := strings.Cut(raw, ":"); ok && strings.Contains(before, "@") && !strings.Contains(before, "/") {
		host = strings.TrimPrefix(before[strings.LastIndex(before, "@")+1:], "//")
		repoPath = after
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "", fmt.Errorf("remote URL %q is not a GitHub URL", remoteURL)
		}
		host, repoPath = u.Hostname(), u.Path
	}
	if !strings.EqualFold(host, "github.com") {
		return "", fmt.Errorf("remote URL %q is not hosted on github.com", remoteURL)
	}
	repoPath = strings.TrimSuffix(strings.Trim(path.Clean("/"+repoPath), "/"), ".git")
	parts := strings.Split(repoPath, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("remote URL %q has no owner/repository identity", remoteURL)
	}
	return parts[0] + "/" + parts[1], nil
}

func configuredRemoteURLContext(ctx context.Context, dir, remote string) (string, error) {
	if remote == "" || remote == "." {
		return "", fmt.Errorf("remote %q is not a GitHub transport remote", remote)
	}
	remoteURL, err := gitOutputContext(ctx, dir, "config", "--get", "remote."+remote+".url")
	if err != nil || remoteURL == "" {
		return "", fmt.Errorf("remote %q has no configured URL", remote)
	}
	return remoteURL, nil
}

func resolveBaseTopologyContext(ctx context.Context, dir, baseBranch string) (remote, repository string, err error) {
	if baseBranch == "" {
		return "", "", errors.New("base branch is empty")
	}
	remote, err = gitOutputContext(ctx, dir, "config", "--get", "branch."+baseBranch+".remote")
	if err != nil || remote == "" {
		remote, err = gitOutputContext(ctx, dir, "for-each-ref", "--format=%(upstream:remotename)", "refs/heads/"+baseBranch)
	}
	if err != nil || remote == "" || remote == "." {
		return "", "", fmt.Errorf("base branch %q has no GitHub remote/upstream; configure branch.%s.remote", baseBranch, baseBranch)
	}
	remoteURL, err := configuredRemoteURLContext(ctx, dir, remote)
	if err != nil {
		return "", "", err
	}
	repository, err = parseGitHubRepositoryURL(remoteURL)
	if err != nil {
		return "", "", fmt.Errorf("resolve base repository from %s: %w", remote, err)
	}
	return remote, repository, nil
}

func newTemporaryPRRefPrefix(number int) (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("create temporary PR ref identity: %w", err)
	}
	return fmt.Sprintf("refs/sidecar/pr/%d/%x", number, token[:]), nil
}

func queryExistingPRContext(ctx context.Context, dir, repository, headOwner, headRef, baseRef string) (*PRIdentity, error) {
	// gh pr list explicitly does not accept owner:branch for --head. Ask for
	// the supported bare branch selector, then disambiguate forks from the
	// structured owner/repository fields.
	args := []string{"pr", "list", "--state", "all", "--head", headRef, "--base", baseRef, "--limit", "100", "--json", "number,url,id,headRefName,headRefOid,baseRefName,state,mergedAt,headRepository,headRepositoryOwner,mergeCommit"}
	if repository != "" {
		args = append(args, "--repo", repository)
	}
	out, err := ghOutputContext(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	var prs []ghPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parse existing pull request: %w", err)
	}
	for _, raw := range prs {
		id := raw.identity(repository)
		if id.HeadRef != headRef || id.BaseRef != baseRef {
			continue
		}
		if headOwner != "" && !strings.EqualFold(id.HeadOwner, headOwner) {
			continue
		}
		return &id, nil
	}
	return nil, nil
}

func pollPRContext(ctx context.Context, dir string, identity PRIdentity) PRPollResult {
	if identity.Number <= 0 || identity.Repository == "" {
		return PRPollResult{Kind: PRPollRepository, Identity: identity, Err: errors.New("pull request repository or number is missing")}
	}
	args := []string{"pr", "view", fmt.Sprintf("%d", identity.Number), "--repo", identity.Repository, "--json", "number,url,id,headRefName,headRefOid,baseRefName,state,mergedAt,headRepository,headRepositoryOwner,mergeCommit"}
	out, err := ghOutputContext(ctx, dir, args...)
	if err != nil {
		return PRPollResult{Kind: classifyGHError(err.Error(), err), Identity: identity, Err: err}
	}
	var raw ghPR
	if err := json.Unmarshal(out, &raw); err != nil {
		return PRPollResult{Kind: PRPollUnavailable, Identity: identity, Err: fmt.Errorf("parse pull request status: %w", err)}
	}
	got := raw.identity(identity.Repository)
	if got.Number != identity.Number || (identity.NodeID != "" && got.NodeID != identity.NodeID) {
		return PRPollResult{Kind: PRPollRepository, Identity: identity, Err: errors.New("GitHub returned a different pull request identity")}
	}
	kind := PRPollOpen
	switch got.State {
	case "MERGED":
		kind = PRPollMerged
	case "CLOSED":
		kind = PRPollClosed
	case "OPEN":
		kind = PRPollOpen
	default:
		kind = PRPollUnavailable
	}
	return PRPollResult{Kind: kind, Identity: got}
}

func viewPRIdentityContext(ctx context.Context, dir, spec, repository string) (PRIdentity, error) {
	args := []string{"pr", "view", spec, "--json", "number,url,id,headRefName,headRefOid,baseRefName,state,mergedAt,headRepository,headRepositoryOwner,mergeCommit"}
	if repository != "" {
		args = append(args, "--repo", repository)
	}
	out, err := ghOutputContext(ctx, dir, args...)
	if err != nil {
		return PRIdentity{}, err
	}
	var raw ghPR
	if err := json.Unmarshal(out, &raw); err != nil {
		return PRIdentity{}, fmt.Errorf("parse pull request identity: %w", err)
	}
	return raw.identity(repository), nil
}

func nextPRPollDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delays := [...]time.Duration{10 * time.Second, 20 * time.Second, 30 * time.Second, 60 * time.Second, 2 * time.Minute}
	if attempt >= len(delays) {
		return delays[len(delays)-1]
	}
	return delays[attempt]
}

func resolveRemoteForRepositoryContext(ctx context.Context, dir, repository string) (string, error) {
	out, err := gitOutputContext(ctx, dir, "remote")
	if err != nil {
		return "", err
	}
	var matches []string
	for _, remote := range strings.Fields(out) {
		// Read the configured URL rather than `remote get-url`: the latter
		// expands url.*.insteadOf, hiding the GitHub identity behind local mirrors.
		url, e := gitOutputContext(ctx, dir, "config", "--get", "remote."+remote+".url")
		if e != nil {
			continue
		}
		normalized := strings.TrimSuffix(strings.TrimSuffix(url, ".git"), "/")
		normalized = strings.TrimPrefix(normalized, "git@github.com:")
		normalized = strings.TrimPrefix(normalized, "ssh://git@github.com/")
		normalized = strings.TrimPrefix(normalized, "https://github.com/")
		if strings.EqualFold(normalized, repository) {
			matches = append(matches, remote)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no Git remote matches GitHub repository %q", repository)
	}
	return "", fmt.Errorf("multiple Git remotes match GitHub repository %q", repository)
}
