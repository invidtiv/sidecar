package buildinfo

import "testing"

func TestVersionPrefersTheStampedValue(t *testing.T) {
	restore := clearStampForTest(t)
	defer restore()

	// Set("") must be a no-op: main passes its ldflags variable through
	// unconditionally, and an un-stamped build would otherwise clobber a stamp
	// set earlier in the same process.
	Set("")
	before := Version()
	Set("")
	if Version() != before {
		t.Fatal(`Set("") changed the reported version`)
	}

	Set("v1.2.3")
	if got := Version(); got != "v1.2.3" {
		t.Errorf("Version() = %q, want the stamped value", got)
	}
}

// clearStampForTest resets the stamp through the same mutex the package uses,
// rather than poking the variable the mutex exists to protect.
func clearStampForTest(t *testing.T) func() {
	t.Helper()
	mu.Lock()
	original := ldVersion
	ldVersion = ""
	mu.Unlock()
	return func() {
		mu.Lock()
		ldVersion = original
		mu.Unlock()
	}
}

// TestVersionFallsBackToBuildInfo covers the un-stamped case: `go test` and a
// plain `go build` must still report something truthful rather than an empty
// string, because this value goes into the host protocol's hello.
func TestVersionFallsBackToBuildInfo(t *testing.T) {
	restore := clearStampForTest(t)
	defer restore()

	if got := Version(); got == "" {
		t.Error("Version() is empty with no stamp; the hello would carry nothing")
	}
}
