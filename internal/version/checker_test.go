package version

import (
	"context"
	"testing"
	"time"
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
