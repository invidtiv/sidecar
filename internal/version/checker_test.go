package version

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

func TestCheck_DevelopmentVersion(t *testing.T) {
	// Development versions should return empty result without making HTTP calls
	devVersions := []string{"", "unknown", "devel", "devel+abc123"}

	for _, v := range devVersions {
		t.Run(v, func(t *testing.T) {
			result := Check(v)
			if result.HasUpdate {
				t.Errorf("Check(%q) should not have update for dev version", v)
			}
			if result.Error != nil {
				t.Errorf("Check(%q) should not error for dev version: %v", v, result.Error)
			}
		})
	}
}

func TestCheckResult(t *testing.T) {
	result := CheckResult{
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		UpdateURL:      "https://github.com/marcus/sidecar/releases/tag/v1.2.0",
		HasUpdate:      true,
	}

	if !result.HasUpdate {
		t.Error("Expected HasUpdate to be true")
	}
	if result.Error != nil {
		t.Error("Expected no error")
	}
}

func TestRelease(t *testing.T) {
	r := Release{
		TagName:     "v1.0.0",
		PublishedAt: time.Now(),
		HTMLURL:     "https://github.com/marcus/sidecar/releases/tag/v1.0.0",
	}

	if r.TagName != "v1.0.0" {
		t.Error("TagName mismatch")
	}
}

// checkProduct must report a product that is not on PATH as not installed, and
// must not run any process for it.
func TestCheckProduct_NotInstalled(t *testing.T) {
	fake := newFakeEnv()
	fake.lookPathErr["tasks"] = true

	msg := checkProduct(context.Background(), fake.env(), TasksDescriptor(), "", false)

	if msg.Target.Installed {
		t.Fatal("expected Tasks to be reported as not installed")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected no commands for a missing product, ran %v", fake.calls)
	}
}

// A development build has no release to compare against: it is not an update
// and not a failed check.
func TestCheckProduct_DevelopmentBuild(t *testing.T) {
	fake := newFakeEnv()
	fake.paths["tasks"] = "/Users/x/.local/state/tasks/dev-installs/main/tasks"
	fake.outputs["/Users/x/.local/state/tasks/dev-installs/main/tasks --version"] = "tasks dev-main (cf216f0)"

	msg := checkProduct(context.Background(), fake.env(), TasksDescriptor(), "", false)

	if !msg.Target.Installed {
		t.Fatal("expected Tasks to be installed")
	}
	if msg.Target.HasUpdate {
		t.Error("a development build must not report an available update")
	}
	if msg.Target.CheckFailed {
		t.Error("a development build is not a failed check")
	}
	if msg.Target.Install.Managed {
		t.Error("a development selector must not be reported as managed")
	}
}

// checkProduct against a stub release API: the SIDECAR_RELEASE_API_BASE seam
// makes the whole discovery path testable without touching the network.
func withStubReleaseAPI(t *testing.T, tag string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `","body":"notes","html_url":"https://example.invalid/r"}`))
	}))
	t.Setenv(apiBaseEnv, srv.URL)
	config.SetTestConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(func() {
		srv.Close()
		config.ResetTestConfigPath()
	})
}

func TestCheckProduct_ReportsAvailableUpdate(t *testing.T) {
	withStubReleaseAPI(t, "v1.6.0")

	fake := newFakeEnv()
	for _, bin := range TasksDescriptor().SuiteBinaries {
		path := "/opt/homebrew/Cellar/tasks/1.5.0/bin/" + bin.Name
		fake.paths[bin.Name] = path
		fake.outputs[path+" "+strings.Join(bin.VersionArgs, " ")] = bin.Name + " 1.5.0"
	}
	fake.outputs["brew --cellar marcus/tap/tasks"] = "/opt/homebrew/Cellar/tasks\n"

	msg := checkProduct(context.Background(), fake.env(), TasksDescriptor(), "", false)

	if !msg.Target.Installed || !msg.Target.HasUpdate {
		t.Fatalf("expected an available update, got %+v", msg.Target)
	}
	if msg.Target.CurrentVersion != "1.5.0" || msg.Target.LatestVersion != "v1.6.0" {
		t.Errorf("versions = %q -> %q", msg.Target.CurrentVersion, msg.Target.LatestVersion)
	}
	if !msg.Target.Install.Managed || msg.Target.Install.Method != InstallMethodHomebrew {
		t.Errorf("expected a managed Homebrew install, got %+v", msg.Target.Install)
	}
	if msg.ReleaseNotes == "" {
		t.Error("expected release notes to be carried with the status")
	}
}

// A failed release check is "unknown", never "up to date", and never plannable.
func TestCheckProduct_FailedCheckIsUnknown(t *testing.T) {
	config.SetTestConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(config.ResetTestConfigPath)
	t.Setenv(apiBaseEnv, "http://127.0.0.1:1") // nothing listening

	fake := newFakeEnv()
	fake.paths["td"] = "/opt/homebrew/Cellar/td/1.0.0/bin/td"
	fake.outputs["/opt/homebrew/Cellar/td/1.0.0/bin/td version --short"] = "1.0.0"

	msg := checkProduct(context.Background(), fake.env(), TdDescriptor(), "", false)

	if !msg.Target.CheckFailed {
		t.Fatal("a failed release check must be marked as such")
	}
	if msg.Target.HasUpdate {
		t.Error("a failed check must not claim an update")
	}
	if plan := SelectPlan([]Target{msg.Target}); len(plan) != 0 {
		t.Error("a product whose check failed must not be planned")
	}
}

// A cached check answers without contacting the release API at all.
func TestCheckProduct_UsesCache(t *testing.T) {
	config.SetTestConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(config.ResetTestConfigPath)
	t.Setenv(apiBaseEnv, "http://127.0.0.1:1") // any call would fail

	d := TdDescriptor()
	if err := SaveCacheFile(d.CacheFile, &CacheEntry{
		LatestVersion: "v2.0.0", CurrentVersion: "1.0.0", CheckedAt: time.Now(), HasUpdate: true,
	}); err != nil {
		t.Fatal(err)
	}

	fake := newFakeEnv()
	fake.paths["td"] = "/opt/homebrew/Cellar/td/1.0.0/bin/td"
	fake.outputs["/opt/homebrew/Cellar/td/1.0.0/bin/td version --short"] = "1.0.0"
	fake.outputs["brew --cellar marcus/tap/td"] = "/opt/homebrew/Cellar/td\n"

	msg := checkProduct(context.Background(), fake.env(), d, "", false)

	if !msg.Target.HasUpdate || msg.Target.LatestVersion != "v2.0.0" {
		t.Fatalf("expected the cached result, got %+v", msg.Target)
	}
	if msg.Target.CheckFailed {
		t.Error("a cache hit must not be reported as a failed check")
	}
}
