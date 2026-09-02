package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentintegration"
)

// Step 5 of the plan's sync design: vendor Herdr's integration assets under
// internal/agentintegration/upstream/<provider>/ with the same discipline the
// manifests get.
//
// They are reference material rather than anything Sidecar installs. A Sidecar
// asset is a port of the provider-specific half of one of these files, so the
// question after every sync is "what changed since the version ours was written
// against"; keeping the upstream bytes beside the port, digest-pinned, is what
// turns that into a diff a reviewer reads rather than a file they re-read.
//
// The tree mirrors upstream's shape exactly, including the shared
// herdr-agent-state.test.ts that sits at the root of the assets directory
// rather than inside a provider, so a path in Herdr's own repository can be
// found here by the same name.

// writeIntegrationTree vendors the assets and writes the lock that pins them.
// It writes only under opts.integrationOut.
func writeIntegrationTree(opts options, src source, dirs []integrationAssetDir,
	shared []integrationAsset) (*agentintegration.UpstreamLock, error) {

	// Apache-2.0 requires the licence to travel with the copied files, so it is
	// read here, before anything is written: a fetch that fails after the assets
	// are on disk leaves the tree updated with no matching lock, and
	// TestVendoredIntegrationAssetsMatchLock then fails in the repository until
	// someone re-runs the sync. Read first, write second, like the rest of the
	// tool.
	license, err := src.read(licenseSource)
	if err != nil {
		return nil, fmt.Errorf("read Herdr LICENSE: %w", err)
	}

	upstream := filepath.Join(opts.integrationOut, "upstream")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		return nil, err
	}

	lock := &agentintegration.UpstreamLock{
		SchemaVersion: agentintegration.UpstreamLockSchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Herdr: agentintegration.UpstreamLockSource{
			Repository: herdrRepository,
			Ref:        opts.ref,
			Commit:     src.commit(),
			SourceDir:  src.localDir(),
			AssetsDir:  assetsDir,
		},
	}

	keep := map[string]bool{"LICENSE": true, "NOTICE": true}

	write := func(rel string, data []byte) error {
		target := filepath.Join(upstream, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		keep[rel] = true
		return nil
	}

	for _, dir := range dirs {
		provider := agentintegration.UpstreamProvider{
			ID:        dir.id,
			Directory: dir.dir,
			Version:   dir.version,
		}
		for _, asset := range dir.files {
			rel := dir.dir + "/" + asset.name
			if err := write(rel, asset.data); err != nil {
				return nil, err
			}
			provider.Files = append(provider.Files, agentintegration.UpstreamFile{
				Path:    "upstream/" + rel,
				Origin:  asset.path,
				SHA256:  sha256Hex(asset.data),
				Bytes:   len(asset.data),
				Version: asset.version,
			})
		}
		lock.Providers = append(lock.Providers, provider)
	}

	for _, asset := range shared {
		if err := write(asset.name, asset.data); err != nil {
			return nil, err
		}
		lock.Files = append(lock.Files, agentintegration.UpstreamFile{
			Path:    "upstream/" + asset.name,
			Origin:  asset.path,
			SHA256:  sha256Hex(asset.data),
			Bytes:   len(asset.data),
			Version: asset.version,
		})
	}

	// This tree carries its own licence and notice pair rather than pointing at
	// the manifests tree's: the two are vendored from different upstream
	// directories and can be synced at different refs.
	if err := write("LICENSE", license); err != nil {
		return nil, err
	}
	notice := []byte(integrationNoticeText(lock))
	if err := write("NOTICE", notice); err != nil {
		return nil, err
	}
	lock.Files = append(lock.Files,
		agentintegration.UpstreamFile{
			Path:   "upstream/LICENSE",
			Origin: licenseSource,
			SHA256: sha256Hex(license),
			Bytes:  len(license),
		},
		agentintegration.UpstreamFile{
			Path:   "upstream/NOTICE",
			Origin: agentintegration.UpstreamGeneratedNotice,
			SHA256: sha256Hex(notice),
			Bytes:  len(notice),
		},
	)
	sort.Slice(lock.Files, func(i, j int) bool { return lock.Files[i].Path < lock.Files[j].Path })

	if err := pruneStaleIntegrationAssets(upstream, keep); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(opts.integrationOut, "upstream.lock.json"), lock); err != nil {
		return nil, err
	}
	return lock, nil
}

// pruneStaleIntegrationAssets removes a vendored file this sync no longer
// produces, and any directory it empties. A provider Herdr drops must not
// linger here unpinned, because an unpinned file is one the lock test cannot
// protect.
func pruneStaleIntegrationAssets(upstream string, keep map[string]bool) error {
	var stale []string
	err := filepath.Walk(upstream, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(upstream, p)
		if err != nil {
			return err
		}
		if !keep[filepath.ToSlash(rel)] {
			stale = append(stale, p)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, p := range stale {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	// Remove directories the pruning emptied, deepest first.
	var dirs []string
	if err := filepath.Walk(upstream, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && p != upstream {
			dirs = append(dirs, p)
		}
		return nil
	}); err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			if err := os.Remove(dir); err != nil {
				return err
			}
		}
	}
	return nil
}

// integrationPortDiff is what the report can say about one provider Sidecar has
// already ported.
type integrationPortDiff struct {
	// Ported is the record from internal/agentintegration, read as data rather
	// than parsed out of an asset header: two of the three Sidecar assets are Go
	// values with no header to parse.
	Ported agentintegration.PortedFrom
	// CurrentVersion is the HERDR_INTEGRATION_VERSION just vendored.
	CurrentVersion int
	// Files is one entry per upstream file for the provider.
	Files []integrationFileDiff
	// Note explains a comparison that could not be made, or qualifies one that
	// was. Empty when the file list speaks for itself.
	Note string
}

// integrationFileDiff is one upstream file's change since the port.
type integrationFileDiff struct {
	Path    string
	Changed bool
	// Whole is true when Body is the current file rather than a diff, which is
	// what an unknown starting point earns.
	Whole bool
	// Skipped is true when no comparison could be made and Body says why. It is
	// deliberately not Changed: a read that failed is evidence about the read,
	// never about upstream, and "this file is new since the port" is a claim a
	// rate-limited fetch must never be allowed to make.
	Skipped bool
	Body    string
}

// integrationPortDiffs compares each ported provider's vendored assets against
// the same files at the commit the port was written from.
//
// It compares bytes rather than version numbers. A version number says what
// Herdr chose to declare; the bytes say what actually changed, and a file
// edited without a bump is exactly the case a version comparison would miss.
func integrationPortDiffs(src source, outDir string, lock *agentintegration.UpstreamLock) []integrationPortDiff {
	return integrationPortDiffsFor(src, outDir, lock, agentintegration.PortedFromRecords())
}

// integrationPortDiffsFor takes the records explicitly so a test can drive the
// unknown-starting-point path, which no shipped record uses today and which is
// exactly the path a future port with no provenance will land on.
func integrationPortDiffsFor(src source, outDir string, lock *agentintegration.UpstreamLock,
	records []agentintegration.PortedFrom) []integrationPortDiff {

	if lock == nil {
		return nil
	}
	// The vendored bytes are read back from the tree this run just wrote, not
	// from the embedded copy, which is still the previous sync's.
	read := func(vendoredPath string) ([]byte, error) {
		return os.ReadFile(filepath.Join(outDir, filepath.FromSlash(vendoredPath)))
	}
	var out []integrationPortDiff
	for _, ported := range records {
		entry := integrationPortDiff{Ported: ported}
		provider, ok := lock.Provider(ported.UpstreamID)
		if !ok {
			entry.Note = fmt.Sprintf("Herdr no longer ships integration assets for `%s`. "+
				"Sidecar's asset is on its own from here.", ported.UpstreamID)
			out = append(out, entry)
			continue
		}
		entry.CurrentVersion = provider.Version

		// An unknown starting point means there is nothing to diff against, so
		// the report owes the reader the whole file. That is the plan's rule and
		// it is the honest amount of work: nobody can say which parts of this
		// file the Sidecar asset already reflects.
		if ported.Version == agentintegration.UnknownPortedVersion || ported.Commit == "" {
			entry.Note = "The upstream version this asset was written against is not recorded, " +
				"so the whole current upstream file is shown rather than a diff."
			for _, file := range provider.Files {
				body, err := read(file.Path)
				if err != nil {
					entry.Files = append(entry.Files, integrationFileDiff{
						Path: file.Path, Skipped: true,
						Body: fmt.Sprintf("the vendored copy could not be read: %v", err),
					})
					continue
				}
				entry.Files = append(entry.Files, integrationFileDiff{
					Path: file.Path, Changed: true, Whole: true,
					Body: truncateLines(string(body), diffLineBudget),
				})
			}
			out = append(out, entry)
			continue
		}

		for _, file := range provider.Files {
			before, err := src.readAt(ported.Commit, file.Origin)
			switch {
			case errors.Is(err, errFileAbsent):
				// Upstream's tree at that commit says so: the file arrived
				// afterwards, and the whole of it is new since the port.
				entry.Files = append(entry.Files, integrationFileDiff{
					Path: file.Path, Changed: true, Whole: true,
					Body: fmt.Sprintf("upstream had no %s at %s; this file is new since the port.",
						file.Origin, shortCommit(ported.Commit)),
				})
				continue
			case err != nil:
				// Anything else -- a 429, a timeout, a shallow clone with no
				// such commit -- is a comparison that did not happen. Saying
				// "new since the port" here is how a network blip becomes a
				// re-port request in a pull request body.
				entry.Files = append(entry.Files, integrationFileDiff{
					Path: file.Path, Skipped: true,
					Body: fmt.Sprintf("%s could not be read at %s, so no comparison was made: %v",
						file.Origin, shortCommit(ported.Commit), err),
				})
				continue
			}
			after, err := read(file.Path)
			if err != nil {
				// Per file, not per entry: a Note written in this loop is
				// overwritten by the next failure, and the first one then
				// vanishes from the report with nothing to say it happened.
				entry.Files = append(entry.Files, integrationFileDiff{
					Path: file.Path, Skipped: true,
					Body: fmt.Sprintf("the vendored copy could not be read, so no comparison was made: %v", err),
				})
				continue
			}
			body, changed := unifiedDiff(
				fmt.Sprintf("%s @ %s", file.Origin, shortCommit(ported.Commit)),
				fmt.Sprintf("%s @ %s", file.Origin, shortCommit(lock.Herdr.Commit)),
				before, after, diffLineBudget)
			entry.Files = append(entry.Files, integrationFileDiff{
				Path: file.Path, Changed: changed, Body: body,
			})
		}
		out = append(out, entry)
	}
	return out
}

func shortCommit(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}

func readPreviousIntegrationLock(dir string) *agentintegration.UpstreamLock {
	data, err := os.ReadFile(filepath.Join(dir, "upstream.lock.json"))
	if err != nil {
		return nil
	}
	var lock agentintegration.UpstreamLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil
	}
	return &lock
}

func integrationNoticeText(lock *agentintegration.UpstreamLock) string {
	var b strings.Builder
	b.WriteString("Herdr integration assets\n")
	b.WriteString("========================\n\n")
	b.WriteString("The files in this directory are unmodified copies of the provider\n")
	b.WriteString("integration assets from Herdr.\n\n")
	fmt.Fprintf(&b, "Project:    Herdr\n")
	fmt.Fprintf(&b, "Repository: %s\n", lock.Herdr.Repository)
	fmt.Fprintf(&b, "Ref:        %s\n", lock.Herdr.Ref)
	if lock.Herdr.Commit != "" {
		fmt.Fprintf(&b, "Commit:     %s\n", lock.Herdr.Commit)
	}
	fmt.Fprintf(&b, "Source:     %s\n", lock.Herdr.AssetsDir)
	fmt.Fprintf(&b, "License:    Apache License, Version 2.0 (see LICENSE in this directory)\n\n")
	b.WriteString("These files are copied verbatim and are reference material only: Sidecar\n")
	b.WriteString("does not install them, and nothing at runtime reads them. They exist so\n")
	b.WriteString("that re-porting a provider is a review of a diff against the upstream a\n")
	b.WriteString("Sidecar asset was written from. Sidecar's own assets live in ../assets,\n")
	b.WriteString("and PortedFromRecords in ../portedfrom.go records which upstream version\n")
	b.WriteString("each was written against.\n\n")
	b.WriteString("TestVendoredIntegrationAssetsMatchLock fails if a byte here changes\n")
	b.WriteString("without a matching entry in ../upstream.lock.json.\n\n")
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
