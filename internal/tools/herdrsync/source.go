package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/pelletier/go-toml/v2"
)

// errFileAbsent marks the one read failure that means something: upstream did
// not have this file at the ref that was asked for.
//
// Everything else -- a rate-limited fetch, a timeout, a ref a shallow clone does
// not carry -- is a read that failed, and the difference matters because the
// port-diff report turns the first into "this file is new since the port" and
// must never turn the second into that. A network blip is not evidence about
// upstream's tree.
var errFileAbsent = errors.New("no such file at that ref")

// source reads files out of a Herdr checkout at a pinned ref, either from a
// local directory or from GitHub. Both implementations read the ref's own
// bytes, so the digests in the lock always describe the commit it records.
type source interface {
	// read returns one file's bytes, capped at maxFetchBytes.
	read(relPath string) ([]byte, error)
	// readAt returns one file's bytes at an arbitrary ref rather than the
	// pinned one. It exists for a single question the report has to answer:
	// what changed in an upstream asset since the commit a Sidecar port was
	// written against. Everything vendored still comes from read.
	//
	// A file upstream did not have at that ref is errFileAbsent; any other
	// error means the comparison could not be made at all.
	readAt(ref, relPath string) ([]byte, error)
	// list returns the names of the regular files directly under relPath.
	list(relPath string) ([]string, error)
	// listDirs returns the names of the subdirectories directly under relPath.
	listDirs(relPath string) ([]string, error)
	// commit is the resolved commit sha, or "" when it is unknown.
	commit() string
	// localDir is the checkout path for a local source, or "".
	localDir() string
	// compare reports how head sits relative to base in upstream's history,
	// which is what the rollback guard needs and nothing else asks for.
	//
	// An error means the question could not be answered -- a commit this
	// checkout does not carry, a compare call that failed -- and that is never
	// the same answer as ancestryDiverged. Confusing the two is how a network
	// blip would become a claim about upstream's history.
	compare(base, head string) (ancestry, error)
	// pinTo returns a source reading the same upstream at another commit, for
	// the one caller that needs it: the guard, holding the pin where the lock
	// already had it.
	pinTo(commit string) (source, error)
}

// sourceFile is one file read from a source.
type sourceFile struct {
	path string
	data []byte
}

func openSource(opts options) (source, error) {
	if opts.sourceDir != "" {
		dir, err := expandPath(opts.sourceDir)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("--source-dir %s is not a directory", opts.sourceDir)
		}
		return newDirSource(dir, opts.ref)
	}
	if opts.offline {
		return nil, fmt.Errorf("--offline needs --source-dir; there is nothing to read without a checkout")
	}
	sha, err := resolveRemoteCommit(opts.ref)
	if err != nil {
		return nil, err
	}
	return &githubSource{ref: opts.ref, sha: sha}, nil
}

func expandPath(p string) (string, error) {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, p[2:]), nil
	}
	return filepath.Abs(p)
}

// --- local checkout -----------------------------------------------------------

// dirSource reads a Herdr checkout at one pinned commit. Every byte comes out
// of the object database with `git show <commit>:<path>`, never out of the
// working tree: the lock and the NOTICE attest the commit this source reports,
// so reading anything else would attest bytes nobody vendored.
type dirSource struct {
	dir string
	ref string
	sha string
}

// newDirSource resolves ref inside the checkout and fails when it does not
// resolve there, rather than recording an empty commit and vendoring whatever
// the working tree holds.
func newDirSource(dir, ref string) (*dirSource, error) {
	if ref == "" {
		ref = "HEAD"
	}
	sha, err := gitRevParse(dir, ref)
	if err != nil {
		return nil, fmt.Errorf("--source-dir %s cannot resolve ref %s: %w", dir, ref, err)
	}
	return &dirSource{dir: dir, ref: ref, sha: sha}, nil
}

func (s *dirSource) read(relPath string) ([]byte, error) {
	return s.readObject(s.sha, relPath)
}

// readAt resolves ref itself rather than reusing the pinned sha, so a caller
// asking about an older commit gets that commit's bytes and a ref the checkout
// does not have is an error rather than a silent fall back to the pin.
//
// A ref this checkout does not carry -- a shallow clone is the ordinary way
// that happens -- is a failure to read, never a statement about upstream's
// tree, so it stays a plain error and never becomes errFileAbsent.
func (s *dirSource) readAt(ref, relPath string) ([]byte, error) {
	sha, err := gitRevParse(s.dir, ref)
	if err != nil {
		return nil, fmt.Errorf("%s cannot resolve ref %s: %w", s.dir, ref, err)
	}
	return s.readObject(sha, relPath)
}

// readObject reads one path out of one commit, separating "the tree does not
// have it" from "git could not tell us". The existence probe comes first
// because `git show <sha>:<path>` reports both as the same exit status with
// only its stderr text to tell them apart, and parsing git's prose is how a
// message change becomes a fabricated report line.
func (s *dirSource) readObject(sha, relPath string) ([]byte, error) {
	object, err := gitRevParseObject(s.dir, sha, relPath)
	if err != nil {
		return nil, err
	}
	// cat-file on the resolved blob rather than `show <sha>:<path>`: a path
	// that names a directory resolves to a tree, and cat-file refuses it
	// instead of returning a listing that would be vendored as file bytes.
	out, err := gitOutput(s.dir, "cat-file", "blob", object)
	if err != nil {
		return nil, err
	}
	return readCapped(bytes.NewReader(out), relPath)
}

func (s *dirSource) list(relPath string) ([]string, error) {
	return s.treeEntries(relPath, "blob")
}

func (s *dirSource) listDirs(relPath string) ([]string, error) {
	return s.treeEntries(relPath, "tree")
}

// treeEntries lists one directory of the pinned commit. An absent directory is
// an error, the way os.ReadDir treats one; a directory that simply holds no
// entry of the requested kind is an empty list.
func (s *dirSource) treeEntries(relPath, kind string) ([]string, error) {
	out, err := gitOutput(s.dir, "ls-tree", "-z", "--full-tree", s.sha, strings.TrimSuffix(relPath, "/")+"/")
	if err != nil {
		return nil, err
	}
	records := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	found := 0
	var names []string
	for _, record := range records {
		if record == "" {
			continue
		}
		found++
		meta, name, ok := strings.Cut(record, "\t")
		if !ok {
			return nil, fmt.Errorf("unexpected git ls-tree record %q in %s at %s", record, relPath, s.sha)
		}
		fields := strings.Fields(meta)
		if len(fields) < 2 {
			return nil, fmt.Errorf("unexpected git ls-tree record %q in %s at %s", record, relPath, s.sha)
		}
		if fields[1] != kind {
			continue
		}
		names = append(names, path.Base(name))
	}
	if found == 0 {
		return nil, fmt.Errorf("%s does not exist in %s at %s", relPath, s.dir, s.sha)
	}
	sort.Strings(names)
	return names, nil
}

func (s *dirSource) commit() string   { return s.sha }
func (s *dirSource) localDir() string { return s.dir }

func (s *dirSource) pinTo(commit string) (source, error) {
	return newDirSource(s.dir, commit)
}

// compare answers the ancestry question out of the checkout's own object
// database, asking it in both directions: `merge-base --is-ancestor` answers
// one, and only the pair separates "head is behind base" from "the two have
// diverged".
func (s *dirSource) compare(base, head string) (ancestry, error) {
	if base == head {
		return ancestryIdentical, nil
	}
	baseContained, err := s.isAncestor(base, head)
	if err != nil {
		return "", err
	}
	headContained, err := s.isAncestor(head, base)
	if err != nil {
		return "", err
	}
	switch {
	case baseContained && headContained:
		return ancestryIdentical, nil
	case baseContained:
		return ancestryAhead, nil
	case headContained:
		return ancestryBehind, nil
	default:
		return ancestryDiverged, nil
	}
}

// isAncestor reports whether ancestor is reachable from descendant. Exit 1 is
// git's "no"; anything else -- exit 128 for a commit a shallow clone does not
// carry is the ordinary one -- is a question that could not be answered, and
// stays an error rather than becoming a "no".
func (s *dirSource) isAncestor(ancestor, descendant string) (bool, error) {
	cmd := exec.Command("git", "-C", s.dir, "merge-base", "--is-ancestor",
		"--end-of-options", ancestor, descendant)
	if _, err := cmd.Output(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() == 1 {
				return false, nil
			}
			if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
				return false, fmt.Errorf("git merge-base --is-ancestor %s %s in %s: %v: %s",
					ancestor, descendant, s.dir, err, stderr)
			}
		}
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s in %s: %w",
			ancestor, descendant, s.dir, err)
	}
	return true, nil
}

// gitRevParse resolves a ref to the commit it names. The ^{commit} peel makes
// an annotated tag resolve to its commit and makes anything that is not a
// commit fail, which is what `git show <commit>:<path>` needs.
func gitRevParse(dir, ref string) (string, error) {
	out, err := gitOutput(dir, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("git rev-parse %s printed nothing", ref)
	}
	return sha, nil
}

// gitRevParseObject resolves <sha>:<path> to the object it names, returning
// errFileAbsent when the commit's tree simply has no such path. `--quiet` makes
// that case exit 1 with nothing on stderr, which is the only signal here that
// does not depend on git's wording.
func gitRevParseObject(dir, sha, relPath string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "--end-of-options", sha+":"+relPath)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 &&
			strings.TrimSpace(string(exitErr.Stderr)) == "" {
			return "", fmt.Errorf("%s at %s: %w", relPath, sha, errFileAbsent)
		}
		return "", fmt.Errorf("git rev-parse %s:%s: %w", sha, relPath, err)
	}
	object := strings.TrimSpace(string(out))
	if object == "" {
		return "", fmt.Errorf("%s at %s: %w", relPath, sha, errFileAbsent)
	}
	return object, nil
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
				return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, stderr)
			}
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// --- GitHub -------------------------------------------------------------------

type githubSource struct {
	ref string
	sha string
	// listings caches one contents-API response per directory. Every caller
	// wants both the files and the subdirectories of the same path, and the
	// asset walk asks for the same directory twice; the sha keys it implicitly,
	// since a source is pinned to one.
	listings map[string][]githubEntry
}

type githubEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// read fetches the pinned commit, not the ref that named it. A moving ref such
// as `main` can advance between resolving the sha and reading a file, and the
// lock and the NOTICE both attest the sha: vendoring anything else would attest
// bytes that are not the ones on disk.
func (s *githubSource) read(relPath string) ([]byte, error) {
	return s.readAt(s.sha, relPath)
}

func (s *githubSource) readAt(ref, relPath string) ([]byte, error) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repoSlug, ref, relPath)
	data, err := httpGetCapped(url, relPath)
	if err != nil {
		var status *httpStatusError
		if errors.As(err, &status) && status.code == http.StatusNotFound {
			return nil, fmt.Errorf("%s at %s: %w", relPath, ref, errFileAbsent)
		}
		return nil, err
	}
	return data, nil
}

func (s *githubSource) list(relPath string) ([]string, error) {
	return s.entries(relPath, "file")
}

func (s *githubSource) listDirs(relPath string) ([]string, error) {
	return s.entries(relPath, "dir")
}

func (s *githubSource) entries(relPath, kind string) ([]string, error) {
	listing, err := s.listing(relPath)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range listing {
		if entry.Type == kind {
			names = append(names, entry.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *githubSource) listing(relPath string) ([]githubEntry, error) {
	if listing, ok := s.listings[relPath]; ok {
		return listing, nil
	}
	// The contents API is the only way to enumerate a directory at a ref, and
	// it is what `gh api` already authenticates for.
	out, err := ghAPI(fmt.Sprintf("repos/%s/contents/%s?ref=%s", repoSlug, relPath, s.sha))
	if err != nil {
		return nil, fmt.Errorf("list %s at %s (%s): %w (use --source-dir to read from a local checkout)",
			relPath, s.ref, s.sha, err)
	}
	var listing []githubEntry
	if err := json.Unmarshal(out, &listing); err != nil {
		return nil, fmt.Errorf("list %s: unexpected contents API shape: %w", relPath, err)
	}
	if s.listings == nil {
		s.listings = map[string][]githubEntry{}
	}
	s.listings[relPath] = listing
	return listing, nil
}

func (s *githubSource) commit() string   { return s.sha }
func (s *githubSource) localDir() string { return "" }

// pinTo resolves the commit rather than trusting it, so a lock carrying a
// commit this repository no longer has fails here instead of turning into 404s
// on every file the sync then tries to read.
func (s *githubSource) pinTo(commit string) (source, error) {
	sha, err := resolveRemoteCommit(commit)
	if err != nil {
		return nil, err
	}
	return &githubSource{ref: commit, sha: sha}, nil
}

// compare asks the compare API, which answers this question directly and is the
// only way to answer it without a checkout.
func (s *githubSource) compare(base, head string) (ancestry, error) {
	if base == head {
		return ancestryIdentical, nil
	}
	out, err := ghAPI(fmt.Sprintf("repos/%s/compare/%s...%s", repoSlug, base, head))
	if err != nil {
		return "", fmt.Errorf("compare %s...%s: %w", shortSHA(base), shortSHA(head), err)
	}
	state, err := ancestryFromCompare(out)
	if err != nil {
		return "", fmt.Errorf("compare %s...%s: %w", shortSHA(base), shortSHA(head), err)
	}
	return state, nil
}

// ancestryFromCompare reads the status the compare API reports for base...head.
// An unrecognised status is an error: the four it documents are the four this
// tool knows how to act on, and treating a fifth as "not behind" would be the
// rollback the guard exists to stop.
func ancestryFromCompare(data []byte) (ancestry, error) {
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("unexpected compare API shape: %w", err)
	}
	switch state := ancestry(result.Status); state {
	case ancestryIdentical, ancestryAhead, ancestryBehind, ancestryDiverged:
		return state, nil
	}
	return "", fmt.Errorf("unexpected compare status %q", result.Status)
}

func ghAPI(endpoint string) ([]byte, error) {
	cmd := exec.Command("gh", "api", endpoint)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		if stderr != "" {
			return nil, fmt.Errorf("gh api %s: %v: %s", endpoint, err, stderr)
		}
		return nil, fmt.Errorf("gh api %s: %w", endpoint, err)
	}
	return out, nil
}

func resolveRemoteCommit(ref string) (string, error) {
	out, err := ghAPI(fmt.Sprintf("repos/%s/commits/%s", repoSlug, ref))
	if err != nil {
		return "", fmt.Errorf("resolve ref %s: %w (use --source-dir to read from a local checkout)", ref, err)
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(out, &commit); err != nil {
		return "", err
	}
	return commit.SHA, nil
}

// defaultRef is the source tree a sync vendors when --ref is not given.
//
// Sidecar takes Herdr's detection work as it lands rather than waiting for a
// release: Sidecar ships faster than Herdr cuts tags, and an unreleased rule
// change is upstream's own judgement about detection. The release tag is a
// separate pin with a separate job (--release-tag, the binary the differential
// harness downloads), and conflating the two is what pinned the vendored tree
// to a commit behind itself.
//
// Offline there is nobody to ask which branch is the default, and a checkout
// already carries an answer: the commit it is standing on.
func defaultRef(opts options) string {
	if opts.offline {
		return "HEAD"
	}
	return defaultBranch()
}

// defaultBranch asks GitHub which branch Herdr develops on rather than assuming
// it. Herdr's is `master`, which is neither the name GitHub defaults to nor the
// one this repository's own docs assumed, so guessing has already been wrong
// once. A failed call falls back to the name that is true today; if that ever
// stops being true the sync fails resolving the ref, which is loud, rather than
// vendoring some other branch.
func defaultBranch() string {
	out, err := ghAPI("repos/" + repoSlug)
	if err != nil {
		return fallbackDefaultBranch
	}
	if name := defaultBranchFrom(out); name != "" {
		return name
	}
	return fallbackDefaultBranch
}

// defaultBranchFrom reads default_branch out of the repository API response,
// returning "" when there is nothing to read, which is the caller's signal to
// fall back.
func defaultBranchFrom(data []byte) string {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(data, &repo); err != nil {
		return ""
	}
	return strings.TrimSpace(repo.DefaultBranch)
}

// newestReleaseTag asks GitHub for the newest Herdr release of any kind,
// preview builds included.
//
// Herdr's preview releases are its real cadence for detection work — they land
// roughly weekly and carry the manifest fixes — and each one ships the same
// release binaries a stable tag does, so the differential harness has an oracle
// either way. Pinning to the newest *stable* tag instead would vendor manifests
// weeks behind the tree and, at the time this changed, would have dropped
// `muse.toml` entirely. A failure is not fatal: the tool falls back to the tag
// known at the time it was written and says so in the report, so an offline
// sync still records a defensible pin.
func newestReleaseTag(offline bool) string {
	if offline {
		return fallbackReleaseTag
	}
	// Not `--jq '.[0]'`: gh's ordering is not part of its contract and drafts
	// sort ahead of releases, so the choice is made here on the published date.
	cmd := exec.Command("gh", "release", "list", "--repo", repoSlug,
		"--limit", "30", "--json", "tagName,isDraft,publishedAt")
	out, err := cmd.Output()
	if err != nil {
		return fallbackReleaseTag
	}
	if tag := newestReleaseTagFrom(out); tag != "" {
		return tag
	}
	return fallbackReleaseTag
}

// newestReleaseTagFrom picks the newest published release from `gh release
// list --json tagName,isDraft,publishedAt` output. It returns "" when there is
// nothing to choose, which is the caller's signal to fall back.
//
// Drafts are excluded: they have no assets, so the harness could not download a
// binary for one. Prereleases are not, which is the whole point.
func newestReleaseTagFrom(data []byte) string {
	var releases []struct {
		TagName     string `json:"tagName"`
		IsDraft     bool   `json:"isDraft"`
		PublishedAt string `json:"publishedAt"`
	}
	if err := json.Unmarshal(data, &releases); err != nil {
		return ""
	}
	best := ""
	var bestAt time.Time
	for _, release := range releases {
		if release.IsDraft || strings.TrimSpace(release.TagName) == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, release.PublishedAt)
		if err != nil {
			// No usable date. It cannot win on recency, but it can stand in
			// when nothing else does, since gh lists newest first.
			if best == "" {
				best = release.TagName
			}
			continue
		}
		if best == "" || at.After(bestAt) {
			best, bestAt = release.TagName, at
		}
	}
	return best
}

// --- shared IO ------------------------------------------------------------------

func readCapped(r io.Reader, name string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxFetchBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFetchBytes {
		return nil, fmt.Errorf("%s exceeds the %d byte fetch cap", name, maxFetchBytes)
	}
	return data, nil
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

// httpStatusError is a response that arrived and was not 200, kept apart from a
// transport failure so a caller can tell a 404 from a 429 or a timeout.
type httpStatusError struct {
	url    string
	status string
	code   int
}

func (e *httpStatusError) Error() string { return fmt.Sprintf("GET %s: %s", e.url, e.status) }

func httpGetCapped(url, name string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &httpStatusError{url: url, status: resp.Status, code: resp.StatusCode}
	}
	return readCapped(resp.Body, name)
}

func httpGetWithETag(url, name string) ([]byte, string, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", &httpStatusError{url: url, status: resp.Status, code: resp.StatusCode}
	}
	data, err := readCapped(resp.Body, name)
	if err != nil {
		return nil, "", err
	}
	return data, resp.Header.Get("ETag"), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// --- manifest directories --------------------------------------------------------

// loadManifestDir reads every .toml in a directory and keys them by the agent
// id declared inside. index.toml is skipped, as it is a catalog, not a manifest.
func loadManifestDir(src source, dir string) (map[string]sourceFile, error) {
	names, err := src.list(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]sourceFile, len(names))
	for _, name := range names {
		if !strings.HasSuffix(name, ".toml") || name == "index.toml" {
			continue
		}
		rel := path.Join(dir, name)
		data, err := src.read(rel)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		m, err := manifest.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", rel, err)
		}
		if existing, ok := out[m.ID]; ok {
			return nil, fmt.Errorf("duplicate manifest id %q in %s and %s", m.ID, existing.path, rel)
		}
		out[m.ID] = sourceFile{path: rel, data: data}
	}
	return out, nil
}

// catalogSet is the published side of the sync: the index plus each file it
// lists, from either herdr.dev or the source checkout.
type catalogSet struct {
	indexData     []byte
	schemaVersion int
	files         map[string]sourceFile
	etag          string
	fetched       bool
	consulted     bool
	source        string
	notes         []string
}

type catalogIndex struct {
	SchemaVersion int `toml:"schema_version"`
	Agents        []struct {
		ID   string `toml:"id"`
		Path string `toml:"path"`
	} `toml:"agents"`
}

// loadCatalog fetches the published catalog and every manifest it lists. With
// --offline it reads the source checkout's distribution directory instead,
// which keeps the published-versus-bundled decision reproducible without a
// network, and records the ETag as unknown.
func loadCatalog(opts options, src source) (*catalogSet, error) {
	set := &catalogSet{files: map[string]sourceFile{}, etag: "unknown"}

	var indexData []byte
	var readFile func(rel string) ([]byte, string, error)

	if opts.offline {
		if src.localDir() == "" {
			set.notes = append(set.notes, "Offline sync with no local checkout: no published catalog was consulted.")
			set.indexData = []byte("")
			return set, nil
		}
		data, err := src.read(path.Join(publishedDir, "index.toml"))
		if err != nil {
			set.notes = append(set.notes,
				fmt.Sprintf("Offline sync: %s has no index.toml, so every agent is vendored from the bundled copy.", publishedDir))
			return set, nil
		}
		indexData = data
		set.source = path.Join(src.localDir(), publishedDir)
		readFile = func(rel string) ([]byte, string, error) {
			body, err := src.read(path.Join(publishedDir, rel))
			return body, path.Join(publishedDir, rel), err
		}
	} else {
		data, etag, err := httpGetWithETag(opts.catalogURL, "index.toml")
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w (rerun with --offline to use the checkout's %s)", opts.catalogURL, err, publishedDir)
		}
		indexData = data
		if etag != "" {
			set.etag = etag
		}
		set.fetched = true
		set.source = opts.catalogURL
		base := opts.catalogURL[:strings.LastIndex(opts.catalogURL, "/")+1]
		readFile = func(rel string) ([]byte, string, error) {
			url := base + rel
			body, err := httpGetCapped(url, rel)
			return body, url, err
		}
	}

	var index catalogIndex
	if err := toml.Unmarshal(indexData, &index); err != nil {
		return nil, fmt.Errorf("parse catalog index: %w", err)
	}
	if index.SchemaVersion != 1 {
		return nil, fmt.Errorf("catalog schema_version is %d, want 1", index.SchemaVersion)
	}
	set.indexData = indexData
	set.schemaVersion = index.SchemaVersion
	set.consulted = true

	for _, entry := range index.Agents {
		if entry.ID == "" || entry.Path == "" {
			return nil, fmt.Errorf("catalog entry needs both id and path")
		}
		if strings.Contains(entry.Path, "://") || strings.HasPrefix(entry.Path, "/") ||
			strings.Contains(entry.Path, "..") {
			return nil, fmt.Errorf("catalog path for %s is unsafe: %q", entry.ID, entry.Path)
		}
		if _, ok := set.files[entry.ID]; ok {
			return nil, fmt.Errorf("catalog lists %s twice", entry.ID)
		}
		body, where, err := readFile(entry.Path)
		if err != nil {
			return nil, fmt.Errorf("read published %s: %w", entry.Path, err)
		}
		set.files[entry.ID] = sourceFile{path: where, data: body}
	}
	return set, nil
}
