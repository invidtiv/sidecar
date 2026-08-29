package buildinfo

import "testing"

func TestVersionPrefersTheStampedValue(t *testing.T) {
	original := ldVersion
	t.Cleanup(func() { ldVersion = original })

	ldVersion = ""
	Set("")
	if ldVersion != "" {
		t.Fatal("Set(\"\") overwrote the stamp; main passes its variable through unconditionally")
	}

	Set("v1.2.3")
	if got := Version(); got != "v1.2.3" {
		t.Errorf("Version() = %q, want the stamped value", got)
	}
}

// TestVersionFallsBackToBuildInfo covers the un-stamped case: `go test` and a
// plain `go build` must still report something truthful rather than an empty
// string, because this value goes into the host protocol's hello.
func TestVersionFallsBackToBuildInfo(t *testing.T) {
	original := ldVersion
	t.Cleanup(func() { ldVersion = original })
	ldVersion = ""

	if got := Version(); got == "" {
		t.Error("Version() is empty with no stamp; the hello would carry nothing")
	}
}
