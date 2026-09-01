package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// source reads files out of a Herdr checkout at a pinned ref, either from a
// local directory or from GitHub.
type source interface {
	// read returns one file's bytes, capped at maxFetchBytes.
	read(relPath string) ([]byte, error)
	// list returns the names of the regular files directly under relPath.
	list(relPath string) ([]string, error)
	// listDirs returns the names of the subdirectories directly under relPath.
	listDirs(relPath string) ([]string, error)
	// commit is the resolved commit sha, or "" when it is unknown.
	commit() string
	// localDir is the checkout path for a local source, or "".
	localDir() string
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
		return &dirSource{dir: dir, sha: gitRevParse(dir, opts.ref)}, nil
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

type dirSource struct {
	dir string
	sha string
}

func (s *dirSource) read(relPath string) ([]byte, error) {
	full := filepath.Join(s.dir, filepath.FromSlash(relPath))
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return readCapped(f, relPath)
}

func (s *dirSource) list(relPath string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, filepath.FromSlash(relPath)))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *dirSource) listDirs(relPath string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, filepath.FromSlash(relPath)))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *dirSource) commit() string   { return s.sha }
func (s *dirSource) localDir() string { return s.dir }

func gitRevParse(dir, ref string) string {
	if ref == "" {
		ref = "HEAD"
	}
	cmd := exec.Command("git", "-C", dir, "rev-parse", ref)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// --- GitHub -------------------------------------------------------------------

type githubSource struct {
	ref string
	sha string
}

func (s *githubSource) read(relPath string) ([]byte, error) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repoSlug, s.ref, relPath)
	return httpGetCapped(url, relPath)
}

func (s *githubSource) list(relPath string) ([]string, error) {
	// The contents API is the only way to enumerate a directory at a ref, and
	// it is what `gh api` already authenticates for.
	out, err := ghAPI(fmt.Sprintf("repos/%s/contents/%s?ref=%s", repoSlug, relPath, s.ref))
	if err != nil {
		return nil, fmt.Errorf("list %s at %s: %w (use --source-dir to read from a local checkout)", relPath, s.ref, err)
	}
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("list %s: unexpected contents API shape: %w", relPath, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.Type == "file" {
			names = append(names, entry.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *githubSource) listDirs(relPath string) ([]string, error) {
	out, err := ghAPI(fmt.Sprintf("repos/%s/contents/%s?ref=%s", repoSlug, relPath, s.ref))
	if err != nil {
		return nil, fmt.Errorf("list %s at %s: %w (use --source-dir to read from a local checkout)", relPath, s.ref, err)
	}
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("list %s: unexpected contents API shape: %w", relPath, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.Type == "dir" {
			names = append(names, entry.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *githubSource) commit() string   { return s.sha }
func (s *githubSource) localDir() string { return "" }

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

// newestReleaseTag asks GitHub for the newest Herdr release. A failure is not
// fatal: the tool falls back to the tag known at the time it was written and
// says so in the report, so an offline sync still records a defensible pin.
func newestReleaseTag(offline bool) string {
	if offline {
		return fallbackReleaseTag
	}
	// Herdr publishes preview builds as prereleases between stable tags. The
	// differential harness downloads a release binary, so only a stable tag is
	// a usable pin.
	cmd := exec.Command("gh", "release", "list", "--repo", repoSlug,
		"--limit", "30", "--json", "tagName,isPrerelease",
		"--jq", "[.[] | select(.isPrerelease == false)] | .[0].tagName")
	out, err := cmd.Output()
	if err != nil {
		return fallbackReleaseTag
	}
	tag := strings.TrimSpace(string(out))
	if tag == "" {
		return fallbackReleaseTag
	}
	return tag
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

func httpGetCapped(url, name string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
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
		return nil, "", fmt.Errorf("GET %s: %s", url, resp.Status)
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
