package version

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The offered release's full changelog is fetched pinned to that release's
// tag — never the file on the default branch, which can already describe
// versions the user has not been offered. Like release checks, results are
// cached in the config directory; a tag's changelog is immutable, so its
// cache never expires.

const (
	changelogRawBaseEnv = "SIDECAR_RELEASE_RAW_BASE"

	defaultRawBase = "https://raw.githubusercontent.com"

	changelogTimeout = 15 * time.Second
)

// ChangelogMsg reports a finished tag-pinned changelog fetch.
type ChangelogMsg struct {
	Owner string
	Repo  string
	Tag   string
	Body  string
	Err   error
}

var changelogTagSanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// ChangelogURL builds the raw URL for one release's changelog file. The
// raw host can be overridden with SIDECAR_RELEASE_RAW_BASE so proofs run
// against local fixtures instead of GitHub.
func ChangelogURL(owner, repo, tag string) string {
	base := os.Getenv(changelogRawBaseEnv)
	if base == "" {
		base = defaultRawBase
	}
	tag = strings.TrimPrefix(strings.TrimSpace(tag), "v")
	return fmt.Sprintf("%s/%s/%s/%s/CHANGELOG.md", strings.TrimSuffix(base, "/"), owner, repo, tag)
}

func changelogCacheFile(repo, tag string) string {
	sanitized := changelogTagSanitizer.ReplaceAllString(strings.TrimPrefix(tag, "v"), "_")
	return fmt.Sprintf("changelog_%s_%s.json", repo, sanitized)
}

// LoadChangelogCache reads a cached changelog for one release tag. A miss
// for a different tag is not a hit: the file name carries the tag.
func LoadChangelogCache(repo, tag string) (string, bool) {
	path := isolatedConfigFile(changelogCacheFile(repo, tag))
	if path == "" {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var entry struct {
		Tag  string `json:"tag"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(data, &entry); err != nil || entry.Tag != normalizeChangelogTag(tag) {
		return "", false
	}
	return entry.Body, true
}

// SaveChangelogCache persists one release's changelog body.
func SaveChangelogCache(repo, tag, body string) {
	path := isolatedConfigFile(changelogCacheFile(repo, tag))
	if path == "" {
		return
	}
	data, err := json.MarshalIndent(struct {
		Tag       string    `json:"tag"`
		Body      string    `json:"body"`
		FetchedAt time.Time `json:"fetchedAt"`
	}{normalizeChangelogTag(tag), body, time.Now()}, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

// FetchChangelogCmd fetches one release's changelog at its tag, answering
// through the cache when it can and the network only when it must.
func FetchChangelogCmd(owner, repo, tag string) tea.Cmd {
	return func() tea.Msg {
		if body, ok := LoadChangelogCache(repo, tag); ok {
			return ChangelogMsg{Owner: owner, Repo: repo, Tag: tag, Body: body}
		}
		client := &http.Client{Timeout: changelogTimeout}
		resp, err := client.Get(ChangelogURL(owner, repo, tag))
		if err != nil {
			return ChangelogMsg{Owner: owner, Repo: repo, Tag: tag, Err: err}
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return ChangelogMsg{Owner: owner, Repo: repo, Tag: tag,
				Err: fmt.Errorf("changelog fetch: HTTP %d", resp.StatusCode)}
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return ChangelogMsg{Owner: owner, Repo: repo, Tag: tag, Err: err}
		}
		text := string(body)
		SaveChangelogCache(repo, tag, text)
		return ChangelogMsg{Owner: owner, Repo: repo, Tag: tag, Body: text}
	}
}

func normalizeChangelogTag(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}
