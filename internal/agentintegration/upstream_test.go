package agentintegration

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"strings"
	"testing"
)

func loadUpstreamLock(t *testing.T) *UpstreamLock {
	t.Helper()
	lock, err := LoadUpstreamLock()
	if err != nil {
		t.Fatalf("LoadUpstreamLock: %v", err)
	}
	return lock
}

// TestVendoredIntegrationAssetsMatchLock is the integration tree's half of the
// rule the vendored manifests already live under: an upstream copy is never
// edited by hand, and the way that stays true is a digest test rather than a
// convention.
func TestVendoredIntegrationAssetsMatchLock(t *testing.T) {
	lock := loadUpstreamLock(t)
	if len(lock.Providers) == 0 {
		t.Fatal("the lock names no providers")
	}

	pinned := map[string]bool{}
	check := func(file UpstreamFile) {
		t.Helper()
		name := strings.TrimPrefix(file.Path, "upstream/")
		pinned[name] = true
		data, err := UpstreamAssetBytes(name)
		if err != nil {
			t.Errorf("read %s: %v", file.Path, err)
			return
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != file.SHA256 {
			t.Errorf("%s sha256 is %s, the lock says %s.\n"+
				"Vendored files are byte-for-byte copies of Herdr's integration assets and are\n"+
				"never edited by hand. Sidecar's own assets live in assets/; re-run\n"+
				"scripts/sync-herdr.sh if upstream changed.", file.Path, got, file.SHA256)
		}
		if len(data) != file.Bytes {
			t.Errorf("%s is %d bytes, the lock says %d", file.Path, len(data), file.Bytes)
		}
		if file.Origin == "" {
			t.Errorf("%s records no origin", file.Path)
		}
	}

	for _, provider := range lock.Providers {
		if len(provider.Files) == 0 {
			t.Errorf("provider %s pins no files", provider.ID)
		}
		for _, file := range provider.Files {
			check(file)
		}
	}
	for _, file := range lock.Files {
		check(file)
	}

	// An unpinned vendored file is one this test cannot protect, so the tree is
	// walked rather than only the lock read.
	dir, err := UpstreamAssets()
	if err != nil {
		t.Fatalf("UpstreamAssets: %v", err)
	}
	err = fs.WalkDir(dir, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !pinned[path] {
			t.Errorf("upstream/%s is vendored but not named in upstream.lock.json", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the vendored tree: %v", err)
	}
}

// TestVendoredIntegrationAssetsIncludeUnderscoreNamedFiles is the regression for
// the one thing embed does silently.
//
// //go:embed upstream drops every file whose name begins with "_" or ".", which
// here is exactly hermes/__init__.py -- the whole of that provider's integration.
// The lock would still name it, so only the walk above would have caught it, and
// only as a confusing "vendored but not pinned" inversion. The all: prefix is
// what makes it present; this says so by name.
func TestVendoredIntegrationAssetsIncludeUnderscoreNamedFiles(t *testing.T) {
	data, err := UpstreamAssetBytes("hermes/__init__.py")
	if err != nil {
		t.Fatalf("hermes/__init__.py is not embedded (is the go:embed directive missing its all: prefix?): %v", err)
	}
	if len(data) == 0 {
		t.Error("hermes/__init__.py is embedded but empty")
	}
}

func TestVendoredIntegrationAttributionTravelsWithTheAssets(t *testing.T) {
	notice, err := UpstreamAssetBytes("NOTICE")
	if err != nil {
		t.Fatalf("read NOTICE: %v", err)
	}
	for _, want := range []string{"Herdr", "https://github.com/herdrdev/herdr", "Apache License", "unmodified copies"} {
		if !strings.Contains(string(notice), want) {
			t.Errorf("NOTICE does not mention %q", want)
		}
	}
	lock := loadUpstreamLock(t)
	if lock.Herdr.Commit != "" && !strings.Contains(string(notice), lock.Herdr.Commit) {
		t.Errorf("NOTICE does not name the vendored commit %s", lock.Herdr.Commit)
	}
	license, err := UpstreamAssetBytes("LICENSE")
	if err != nil {
		t.Fatalf("read LICENSE: %v", err)
	}
	if !strings.Contains(string(license), "Apache License") {
		t.Error("upstream/LICENSE is not the Apache licence text")
	}
	if notice, ok := lock.File("upstream/NOTICE"); ok && notice.Origin != UpstreamGeneratedNotice {
		t.Errorf("upstream/NOTICE origin = %q, want %q", notice.Origin, UpstreamGeneratedNotice)
	}
}

// TestEveryAdapterRecordsWhatItWasPortedFrom is why the ported-from table is
// data and not a comment: a new adapter with no record makes the next sync
// report silently skip its provider, which reads as "nothing changed upstream".
func TestEveryAdapterRecordsWhatItWasPortedFrom(t *testing.T) {
	for _, adapter := range DefaultAdapters() {
		if _, ok := PortedFromProvider(adapter.Provider()); !ok {
			t.Errorf("%s ships an integration with no PortedFrom record.\n"+
				"Add one to portedfrom.go naming the Herdr integration version it was written\n"+
				"against, or UnknownPortedVersion when that cannot be established from evidence.",
				adapter.Provider())
		}
	}
	shipped := map[string]bool{}
	for _, adapter := range DefaultAdapters() {
		shipped[adapter.Provider()] = true
	}
	for _, record := range PortedFromRecords() {
		if !shipped[record.Provider] {
			t.Errorf("portedfrom.go records %s but no adapter ships it", record.Provider)
		}
	}
}

// TestEveryPortedFromNamesAVendoredUpstreamVersion keeps the two halves honest.
// A record naming a version the vendored tree does not carry means either the
// port moved on without the record, or upstream rolled a version back, and both
// are review conversations rather than things to discover during a re-port.
func TestEveryPortedFromNamesAVendoredUpstreamVersion(t *testing.T) {
	lock := loadUpstreamLock(t)
	for _, record := range PortedFromRecords() {
		if record.Evidence == "" {
			t.Errorf("%s records no evidence for its ported-from version", record.Provider)
		}
		provider, ok := lock.Provider(record.UpstreamID)
		if !ok {
			t.Errorf("%s is ported from herdr %q, which the vendored tree does not carry",
				record.Provider, record.UpstreamID)
			continue
		}
		if provider.Directory != record.UpstreamDir {
			t.Errorf("%s names upstream directory %q, the lock says %q",
				record.Provider, record.UpstreamDir, provider.Directory)
		}
		if record.Version == UnknownPortedVersion {
			// Legitimate, and the sync report renders the whole upstream file
			// instead of a diff. Nothing else to check.
			continue
		}
		if record.Commit == "" {
			t.Errorf("%s names version %s but no commit, so a sync cannot diff against it",
				record.Provider, record.Version)
		}
		// The version the port was written against is not required to equal the
		// vendored one -- upstream bumping is the normal case, and the whole
		// point of the record -- but it may never be ahead of it.
		if versionAbove(record.Version, provider.Version) {
			t.Errorf("%s claims to be ported from herdr %s version %s, which is newer than the vendored %d",
				record.Provider, record.UpstreamID, record.Version, provider.Version)
		}
	}
}

// versionAbove reports whether a decimal version string is greater than n. An
// unparseable string is not above anything; the field is a string only so
// UnknownPortedVersion fits in it, and that case is handled by the caller.
func versionAbove(version string, n int) bool {
	value := 0
	for _, r := range version {
		if r < '0' || r > '9' {
			return false
		}
		value = value*10 + int(r-'0')
	}
	return value > n
}
