// Command herdrsync refreshes Sidecar's vendored copy of Herdr's
// agent-detection manifests, the lock that pins them, and the alias and
// authority tables extracted from Herdr's source.
//
// It writes only under the output directory (internal/agentactivity/manifests
// by default). Vendored files are byte-for-byte copies; nothing here rewrites
// upstream content.
//
// Usage:
//
//	go run ./internal/tools/herdrsync [flags]
//	scripts/sync-herdr.sh [flags]
//
// The tested path is a local Herdr checkout:
//
//	go run ./internal/tools/herdrsync --source-dir ~/code/herdr --ref e2b85c7
//
// Steps 1 to 4 and 6 of the plan's sync design are implemented. Step 5,
// vendoring src/integration/assets, is Phase 3 work and is marked TODO below.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/agentactivity/manifests"
)

const (
	// herdrRepository is the upstream this tool tracks.
	herdrRepository = "https://github.com/herdrdev/herdr"
	repoSlug        = "herdrdev/herdr"

	// defaultCatalogURL is Herdr's published catalog index.
	defaultCatalogURL = "https://herdr.dev/agent-detection/index.toml"

	// fallbackReleaseTag is the newest Herdr release known when this tool was
	// written. It is used when `gh release list` cannot reach GitHub, so an
	// offline run still records a defensible pin for the differential harness.
	fallbackReleaseTag = "v0.8.2"

	// bundledDir and publishedDir are the two manifest directories in the
	// Herdr repository.
	bundledDir   = "src/detect/manifests"
	publishedDir = "distribution/agent-detection"

	// aliasSource and authoritySource are the files the extractors read.
	aliasSource     = "src/detect/mod.rs"
	authoritySource = "docs/preview/website/src/content/docs/agents.mdx"
	assetsDir       = "src/integration/assets"
	licenseSource   = "LICENSE"

	// maxFetchBytes is Herdr's own per-file cap (MAX_FETCH_BYTES).
	maxFetchBytes = 256 * 1024
)

type options struct {
	ref        string
	releaseTag string
	catalogURL string
	sourceDir  string
	offline    bool
	out        string
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "herdrsync: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, out *os.File) error {
	var opts options
	fs := flag.NewFlagSet("herdrsync", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&opts.ref, "ref", "", "Herdr git ref to vendor from (default: the newest release tag)")
	fs.StringVar(&opts.releaseTag, "release-tag", "", "Herdr release tag the differential harness runs against (default: the newest release tag)")
	fs.StringVar(&opts.catalogURL, "catalog", defaultCatalogURL, "published catalog index URL")
	fs.StringVar(&opts.sourceDir, "source-dir", "", "read Herdr files from a local checkout instead of fetching them")
	fs.BoolVar(&opts.offline, "offline", false, "do not touch the network; take published copies from the source checkout's distribution directory")
	fs.StringVar(&opts.out, "out", filepath.Join("internal", "agentactivity", "manifests"), "output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	report, err := sync(opts)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprint(out, report.summary())
	return nil
}

// sync performs the whole vendoring pass and returns the rendered review.
func sync(opts options) (*syncReport, error) {
	if opts.releaseTag == "" {
		opts.releaseTag = newestReleaseTag(opts.offline)
	}
	if opts.ref == "" {
		opts.ref = opts.releaseTag
	}

	src, err := openSource(opts)
	if err != nil {
		return nil, err
	}

	report := &syncReport{
		Ref:        opts.ref,
		ReleaseTag: opts.releaseTag,
		Out:        opts.out,
		StartedAt:  time.Now().UTC(),
	}

	// Step 1: fetch both manifest sets.
	bundled, err := loadManifestDir(src, bundledDir)
	if err != nil {
		return nil, fmt.Errorf("read bundled manifests: %w", err)
	}
	if len(bundled) == 0 {
		return nil, fmt.Errorf("read bundled manifests: %s is empty", bundledDir)
	}

	catalog, err := loadCatalog(opts, src)
	if err != nil {
		return nil, fmt.Errorf("read published catalog: %w", err)
	}
	report.Notes = append(report.Notes, catalog.notes...)

	// Steps 2 and 3: validate everything, then choose per agent the copy a
	// Herdr client would load.
	choices, err := chooseManifests(bundled, catalog)
	if err != nil {
		return nil, err
	}

	// Step 4: extract the alias and authority tables.
	aliases, err := extractAliases(src, opts.ref)
	if err != nil {
		return nil, fmt.Errorf("extract aliases: %w", err)
	}
	authority, err := extractAuthority(src, opts.ref)
	if err != nil {
		return nil, fmt.Errorf("extract authority: %w", err)
	}

	// Step 5 (integration assets under internal/agentintegration/upstream) is
	// Phase 3 work. TODO(phase3): vendor src/integration/assets with the same
	// lock discipline, recording each file's digest and its
	// HERDR_INTEGRATION_VERSION, and report every version bump. The version
	// numbers themselves are already extracted into authority.upstream.json,
	// so the report can name a bump before the assets are vendored.

	previous := readPreviousLock(opts.out)

	// Step 6: write the tree, the lock, and the report.
	lock, err := writeTree(opts, src, catalog, choices, aliases, authority)
	if err != nil {
		return nil, err
	}
	report.Lock = lock
	report.Previous = previous
	report.Aliases = aliases
	report.Authority = authority
	report.FinishedAt = time.Now().UTC()

	body := report.render()
	reportPath := filepath.Join(opts.out, "report.md")
	if err := os.WriteFile(reportPath, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", reportPath, err)
	}
	report.Body = body
	return report, nil
}

// chosenManifest is one agent's winning copy plus why it won.
type chosenManifest struct {
	id       string
	filename string
	data     []byte
	parsed   *manifest.Manifest
	source   string
	reason   string

	bundledVersion   string
	publishedVersion string
}

// chooseManifests validates every file and picks, per agent, the copy a Herdr
// client would load: the published one unless it is older than the bundled one
// (manifest.rs read_remote_manifest). Where only one copy exists, it wins.
func chooseManifests(bundled map[string]sourceFile, catalog *catalogSet) ([]chosenManifest, error) {
	opts := manifest.ValidateOptions{AllowIncompatibleRegex: true}

	parse := func(where string, file sourceFile) (*manifest.Manifest, error) {
		m, err := manifest.ParseRemoteWith(file.data, opts)
		if err != nil {
			if tooNew, ok := manifest.AsEngineTooNew(err); ok {
				return nil, fmt.Errorf("%s %s needs manifest engine %d and this engine is %d; it cannot be vendored",
					where, file.path, tooNew.Required, tooNew.Engine)
			}
			return nil, fmt.Errorf("%s %s failed validation: %w", where, file.path, err)
		}
		return m, nil
	}

	var out []chosenManifest
	ids := make([]string, 0, len(bundled))
	for id := range bundled {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		bundledFile := bundled[id]
		bundledManifest, err := parse("bundled", bundledFile)
		if err != nil {
			return nil, err
		}
		choice := chosenManifest{
			id:             id,
			filename:       filepath.Base(bundledFile.path),
			data:           bundledFile.data,
			parsed:         bundledManifest,
			source:         manifests.SourceBundled,
			bundledVersion: bundledManifest.Version,
		}

		publishedFile, ok := catalog.files[id]
		if !ok {
			if catalog.consulted {
				choice.reason = "bundled only; the published catalog does not list this agent"
			} else {
				choice.reason = "bundled only; no published catalog was consulted"
			}
			out = append(out, choice)
			continue
		}
		publishedManifest, err := parse("published", publishedFile)
		if err != nil {
			return nil, err
		}
		choice.publishedVersion = publishedManifest.Version

		switch cmp := manifest.CompareVersions(publishedManifest.Version, bundledManifest.Version); {
		case cmp < 0:
			choice.reason = fmt.Sprintf(
				"bundled %s is newer than published %s; a Herdr client ignores the older remote copy",
				bundledManifest.Version, publishedManifest.Version)
		case cmp > 0:
			choice.source = manifests.SourcePublished
			choice.data = publishedFile.data
			choice.parsed = publishedManifest
			choice.filename = filepath.Base(publishedFile.path)
			choice.reason = fmt.Sprintf("published %s is newer than bundled %s",
				publishedManifest.Version, bundledManifest.Version)
		default:
			choice.source = manifests.SourcePublished
			choice.data = publishedFile.data
			choice.parsed = publishedManifest
			choice.filename = filepath.Base(publishedFile.path)
			choice.reason = fmt.Sprintf(
				"published and bundled are both %s; a Herdr client prefers the remote copy",
				publishedManifest.Version)
		}
		out = append(out, choice)
	}
	return out, nil
}

// writeTree writes upstream/, the extracted JSON tables, and the lock.
func writeTree(opts options, src source, catalog *catalogSet, choices []chosenManifest,
	aliases *manifests.Aliases, authority *manifests.Authority) (*manifests.Lock, error) {

	upstream := filepath.Join(opts.out, "upstream")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		return nil, err
	}
	if err := pruneStaleManifests(upstream, choices); err != nil {
		return nil, err
	}

	lock := &manifests.Lock{
		SchemaVersion: manifests.LockSchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		EngineVersion: manifest.EngineVersion,
		Herdr: manifests.LockUpstream{
			Repository:       herdrRepository,
			Ref:              opts.ref,
			Commit:           src.commit(),
			SourceDir:        src.localDir(),
			PinnedReleaseTag: opts.releaseTag,
		},
		Catalog: manifests.LockCatalog{
			URL:           opts.catalogURL,
			ETag:          catalog.etag,
			Fetched:       catalog.fetched,
			Source:        catalog.source,
			SchemaVersion: catalog.schemaVersion,
			Path:          "upstream/index.toml",
			SHA256:        sha256Hex(catalog.indexData),
		},
	}

	for _, choice := range choices {
		rel := filepath.Join("upstream", choice.filename)
		if err := os.WriteFile(filepath.Join(opts.out, rel), choice.data, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", rel, err)
		}
		entry := manifests.LockAgent{
			ID:               choice.id,
			Path:             filepath.ToSlash(rel),
			SHA256:           sha256Hex(choice.data),
			Bytes:            len(choice.data),
			Version:          choice.parsed.Version,
			UpdatedAt:        choice.parsed.UpdatedAt,
			RuleCount:        len(choice.parsed.Rules),
			Source:           choice.source,
			SourceReason:     choice.reason,
			BundledVersion:   choice.bundledVersion,
			PublishedVersion: choice.publishedVersion,
			Aliases:          choice.parsed.Aliases,
		}
		if choice.parsed.MinEngineVersion != nil {
			entry.MinEngineVersion = *choice.parsed.MinEngineVersion
		}
		for _, bad := range choice.parsed.RegexIncompatibilities() {
			entry.RegexIncompatibilities = append(entry.RegexIncompatibilities,
				manifests.LockRegexIncompatibility{
					RuleID:  bad.RuleID,
					Field:   bad.Field,
					Pattern: bad.Pattern,
					Error:   bad.Error,
				})
		}
		if entry.Aliases == nil {
			entry.Aliases = []string{}
		}
		lock.Agents = append(lock.Agents, entry)
	}

	if err := os.WriteFile(filepath.Join(upstream, "index.toml"), catalog.indexData, 0o644); err != nil {
		return nil, err
	}
	license, err := src.read(licenseSource)
	if err != nil {
		return nil, fmt.Errorf("read Herdr LICENSE: %w", err)
	}
	if err := os.WriteFile(filepath.Join(upstream, "LICENSE"), license, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(upstream, "NOTICE"), []byte(noticeText(lock)), 0o644); err != nil {
		return nil, err
	}

	if !catalog.fetched {
		lock.Notes = append(lock.Notes,
			"The published catalog was not fetched over the network; published copies came from "+catalog.source+
				" and the catalog ETag is recorded as unknown.")
	}
	lock.Notes = append(lock.Notes, catalog.notes...)

	if err := writeJSON(filepath.Join(opts.out, "aliases.upstream.json"), aliases); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(opts.out, "authority.upstream.json"), authority); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(opts.out, "upstream.lock.json"), lock); err != nil {
		return nil, err
	}
	return lock, nil
}

// pruneStaleManifests removes a vendored .toml that this sync no longer
// produces, so a dropped upstream agent does not linger unpinned.
func pruneStaleManifests(upstream string, choices []chosenManifest) error {
	keep := map[string]bool{"index.toml": true}
	for _, choice := range choices {
		keep[choice.filename] = true
	}
	entries, err := os.ReadDir(upstream)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".toml") || keep[name] {
			continue
		}
		if err := os.Remove(filepath.Join(upstream, name)); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func readPreviousLock(out string) *manifests.Lock {
	data, err := os.ReadFile(filepath.Join(out, "upstream.lock.json"))
	if err != nil {
		return nil
	}
	var lock manifests.Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil
	}
	return &lock
}

func noticeText(lock *manifests.Lock) string {
	var b strings.Builder
	b.WriteString("Herdr agent-detection manifests\n")
	b.WriteString("===============================\n\n")
	b.WriteString("The files in this directory are unmodified copies of agent-detection\n")
	b.WriteString("manifests from Herdr.\n\n")
	fmt.Fprintf(&b, "Project:    Herdr\n")
	fmt.Fprintf(&b, "Repository: %s\n", lock.Herdr.Repository)
	fmt.Fprintf(&b, "Ref:        %s\n", lock.Herdr.Ref)
	if lock.Herdr.Commit != "" {
		fmt.Fprintf(&b, "Commit:     %s\n", lock.Herdr.Commit)
	}
	fmt.Fprintf(&b, "Catalog:    %s\n", lock.Catalog.URL)
	fmt.Fprintf(&b, "License:    Apache License, Version 2.0 (see LICENSE in this directory)\n\n")
	b.WriteString("These files are copied verbatim. Sidecar does not modify them; local rule\n")
	b.WriteString("changes live in ../sidecar/<agent>.toml as overlays, and\n")
	b.WriteString("TestVendoredManifestsMatchLock fails if a byte here changes without a\n")
	b.WriteString("matching entry in ../upstream.lock.json.\n\n")
	b.WriteString("Licensed under the Apache License, Version 2.0 (the \"License\"); you may not\n")
	b.WriteString("use these files except in compliance with the License. You may obtain a copy\n")
	b.WriteString("of the License at\n\n")
	b.WriteString("    http://www.apache.org/licenses/LICENSE-2.0\n\n")
	b.WriteString("Unless required by applicable law or agreed to in writing, software\n")
	b.WriteString("distributed under the License is distributed on an \"AS IS\" BASIS, WITHOUT\n")
	b.WriteString("WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the\n")
	b.WriteString("License for the specific language governing permissions and limitations\n")
	b.WriteString("under the License.\n")
	return b.String()
}
