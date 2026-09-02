// Command herdrsync refreshes Sidecar's vendored copy of Herdr's
// agent-detection manifests, the lock that pins them, the alias and authority
// tables extracted from Herdr's source, and Herdr's provider integration
// assets.
//
// It writes under two output directories and nowhere else:
// internal/agentactivity/manifests for the detection lane and
// internal/agentintegration for the hooks lane, each with its own lock.
// Vendored files are byte-for-byte copies; nothing here rewrites upstream
// content.
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
// A local checkout is read through `git show <ref>:<path>`, so the vendored
// bytes and the commit recorded in the lock are always the same object; a ref
// that does not resolve in that checkout fails the run.
//
// There are two pins and they are not the same pin. --ref selects the source
// tree to vendor and defaults to Herdr's own default branch, because Sidecar
// ships faster than Herdr tags and takes detection fixes as they land.
// --release-tag names the release whose binary the differential harness
// downloads, and defaults to the newest release of any kind, because it has to
// be something a runner can actually download. A sync never moves --ref's
// resolved commit backwards past the one the lock records; see holdPin.
//
// All six steps of the plan's sync design are implemented.
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

	// fallbackDefaultBranch is Herdr's default branch, used only when the
	// repository API cannot be reached to confirm it. It is `master`, not
	// `main`: `main` does not exist in that repository at all.
	fallbackDefaultBranch = "master"

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
	// integrationOut is the second output root, for the vendored integration
	// assets and their own lock. It is deliberately separate from out: the two
	// trees are embedded into different packages, and a lock that described
	// files outside its own directory could not be checked by the package that
	// embeds it.
	integrationOut string
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
	fs.StringVar(&opts.ref, "ref", "", "Herdr git ref to vendor from (default: Herdr's default branch, or HEAD with --offline)")
	fs.StringVar(&opts.releaseTag, "release-tag", "", "Herdr release tag the differential harness runs against (default: the newest release tag)")
	fs.StringVar(&opts.catalogURL, "catalog", defaultCatalogURL, "published catalog index URL")
	fs.StringVar(&opts.sourceDir, "source-dir", "", "read Herdr files from a local checkout instead of fetching them")
	fs.BoolVar(&opts.offline, "offline", false, "do not touch the network; take published copies from the source checkout's distribution directory")
	fs.StringVar(&opts.out, "out", filepath.Join("internal", "agentactivity", "manifests"), "output directory for the manifests and their lock")
	fs.StringVar(&opts.integrationOut, "integration-out", filepath.Join("internal", "agentintegration"), "output directory for the vendored integration assets and their lock")
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
	// Whether the ref was typed decides what the rollback guard is allowed to
	// do with it, so it has to be read before the default fills it in.
	explicitRef := strings.TrimSpace(opts.ref) != ""
	if !explicitRef {
		opts.ref = defaultRef(opts)
	}
	// The report compares upstream aliases against Sidecar's own source, so a
	// working directory outside this repository is a failure worth catching
	// before anything is written.
	root, err := sidecarRepoRoot()
	if err != nil {
		return nil, err
	}
	if opts.integrationOut == "" {
		// Nothing derives this from opts.out. The two trees are embedded into
		// different packages, and guessing a second output root from the first
		// is how a sync writes a vendored tree somewhere nobody is looking.
		return nil, fmt.Errorf("no integration output directory; pass --integration-out")
	}
	if err := checkOutputDir(root, "--out", opts.out); err != nil {
		return nil, err
	}
	if err := checkOutputDir(root, "--integration-out", opts.integrationOut); err != nil {
		return nil, err
	}

	src, err := openSource(opts)
	if err != nil {
		return nil, err
	}

	// The lock is read here rather than at step 6 because the guard needs the
	// commit it records before a byte is vendored: everything below reads the
	// tree the guard may still move.
	previous := readPreviousLock(opts.out)
	src, pin, err := holdPin(src, previous, opts.ref, explicitRef)
	if err != nil {
		return nil, err
	}
	if pin != nil && pin.keptPin {
		// The lock describes the tree that was vendored, so a held pin keeps
		// the previous lock's ref beside the commit it belongs to rather than
		// recording a ref that names something else.
		opts.ref = previous.Herdr.Ref
		if opts.ref == "" {
			opts.ref = previous.Herdr.Commit
		}
	}

	report := &syncReport{
		Ref:            opts.ref,
		ReleaseTag:     opts.releaseTag,
		Out:            opts.out,
		IntegrationOut: opts.integrationOut,
		StartedAt:      time.Now().UTC(),
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

	// Step 4: extract the alias and authority tables. The integration assets are
	// read here rather than inside the authority extractor because step 5
	// vendors the same bytes, and reading the tree twice would cost 34 more
	// reads to learn what is already in hand.
	aliases, err := extractAliases(src, opts.ref)
	if err != nil {
		return nil, fmt.Errorf("extract aliases: %w", err)
	}
	assetDirs, sharedAssets, err := integrationAssets(src)
	if err != nil {
		return nil, fmt.Errorf("read integration assets: %w", err)
	}
	authority, err := extractAuthority(src, opts.ref, assetDirs)
	if err != nil {
		return nil, fmt.Errorf("extract authority: %w", err)
	}

	previousIntegration := readPreviousIntegrationLock(opts.integrationOut)
	// The vendored bytes as they stand *before* step 6 overwrites them. There
	// is no second copy afterwards, so the verdict-flip table's "before" side
	// has to be taken here or not at all.
	previousManifests := readVendoredManifests(filepath.Join(opts.out, "upstream"))

	// Step 5: vendor the integration assets and lock them.
	integrationLock, err := writeIntegrationTree(opts, src, assetDirs, sharedAssets)
	if err != nil {
		return nil, fmt.Errorf("vendor integration assets: %w", err)
	}

	// Step 6: write the tree, the lock, and the report.
	lock, err := writeTree(opts, src, catalog, choices, aliases, authority, pin)
	if err != nil {
		return nil, err
	}
	report.Lock = lock
	report.Pin = pin
	if pin != nil && pin.reportNote != "" {
		report.Notes = append(report.Notes, pin.reportNote)
	}
	report.Previous = previous
	report.PreviousManifests = previousManifests
	report.Manifests = chosenBytes(choices)
	report.Aliases = aliases
	report.Authority = authority
	report.Integration = integrationLock
	report.PreviousIntegration = previousIntegration
	report.IntegrationDiffs = integrationPortDiffs(src, opts.integrationOut, integrationLock)
	report.FinishedAt = time.Now().UTC()

	body, err := report.render()
	if err != nil {
		return nil, err
	}
	reportPath := filepath.Join(opts.out, "report.md")
	if err := os.WriteFile(reportPath, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", reportPath, err)
	}
	report.Body = body
	return report, nil
}

// checkOutputDir refuses an output directory outside the places a sync is meant
// to write.
//
// It is not a tidiness rule. Both output roots are emptied of anything the run
// did not produce, and the integration side does that recursively, directories
// included: `--integration-out ~` from the repository root would delete
// everything under ~/upstream. The flag is the only unvalidated path in a tool
// that otherwise assumes it is standing in the Sidecar repository, so it is
// checked against that assumption before anything is written.
//
// The system temp directory is allowed as well, because that is where the tests
// and every rehearsal sync write, and refusing it would mean the destructive
// path could only be exercised against the repository itself.
func checkOutputDir(root, flag, dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("no output directory for %s", flag)
	}
	abs, err := expandPath(dir)
	if err != nil {
		return fmt.Errorf("%s %s: %w", flag, dir, err)
	}
	for _, base := range []string{root, os.TempDir()} {
		if withinDir(base, abs) {
			return nil
		}
	}
	return fmt.Errorf("%s %s resolves to %s, which is outside the Sidecar repository at %s. "+
		"A sync removes everything under its output directories that it did not write, "+
		"so it refuses to write anywhere else", flag, dir, abs, root)
}

// withinDir reports whether target is base or sits under it, comparing paths
// with symlinks resolved so /var and /private/var are one place.
func withinDir(base, target string) bool {
	rel, err := filepath.Rel(resolveExisting(base), resolveExisting(target))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// resolveExisting resolves symlinks on the deepest part of p that exists and
// re-joins the rest, so an output directory a sync has not created yet still
// compares against a resolved base.
func resolveExisting(p string) string {
	rest := ""
	for {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
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

// chosenBytes keys this sync's vendored bytes by file base, the way
// readVendoredManifests keys the previous ones, so the two are comparable.
func chosenBytes(choices []chosenManifest) map[string][]byte {
	out := make(map[string][]byte, len(choices))
	for _, choice := range choices {
		out[strings.TrimSuffix(choice.filename, ".toml")] = choice.data
	}
	return out
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
	aliases *manifests.Aliases, authority *manifests.Authority, pin *pinDecision) (*manifests.Lock, error) {

	// Read before write, for the reason writeIntegrationTree gives: a LICENSE
	// fetch that fails after the manifests are on disk leaves the tree updated
	// with no matching lock, and the digest test then fails in the repository
	// until someone re-runs the sync.
	license, err := src.read(licenseSource)
	if err != nil {
		return nil, fmt.Errorf("read Herdr LICENSE: %w", err)
	}

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
	if err := os.WriteFile(filepath.Join(upstream, "LICENSE"), license, 0o644); err != nil {
		return nil, err
	}
	// The NOTICE is rendered from the lock, so it is pinned after the fields it
	// quotes are set; nothing in it depends on the digests recorded here.
	notice := []byte(noticeText(lock))
	if err := os.WriteFile(filepath.Join(upstream, "NOTICE"), notice, 0o644); err != nil {
		return nil, err
	}
	lock.Files = []manifests.LockFile{
		{
			Path:   "upstream/LICENSE",
			SHA256: sha256Hex(license),
			Bytes:  len(license),
			Origin: licenseSource,
		},
		{
			Path:   "upstream/NOTICE",
			SHA256: sha256Hex(notice),
			Bytes:  len(notice),
			Origin: manifests.GeneratedNotice,
		},
	}

	// The pin note first: it is the one note that says the vendored tree is not
	// simply what the ref names.
	if pin != nil && pin.lockNote != "" {
		lock.Notes = append(lock.Notes, pin.lockNote)
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
