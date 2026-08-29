// Package buildinfo exposes this binary's build identity to library code.
//
// The version string has always been an ldflags-injected variable in package
// main (cmd/sidecar/main.go), which means nothing under internal/ could read
// it. That was fine while only `--version` cared. It stops being fine the
// moment a remote host has to answer "what sidecar am I talking to?" over a
// wire — the host protocol's hello carries a version string, and the code that
// builds the hello is a library.
//
// Set is called once from main before any command dispatch. Everything else
// reads Version, which falls back to debug.ReadBuildInfo so a plain `go build`
// and `go test` still report something truthful rather than an empty string.
package buildinfo

import (
	"runtime/debug"
	"sync"
)

var (
	mu        sync.RWMutex
	ldVersion string
)

// Set records the ldflags-injected version. Calling it with the empty string
// is a no-op, so main can pass its variable through unconditionally and let
// the build-info fallback answer for an un-stamped build.
func Set(version string) {
	if version == "" {
		return
	}
	mu.Lock()
	ldVersion = version
	mu.Unlock()
}

// Version returns the release version when the build was stamped, and
// otherwise derives a development version from the embedded VCS data. The
// derivation is the one `sidecar --version` has always printed; it lives here
// now so both main and the host protocol read the same answer.
func Version() string {
	mu.RLock()
	stamped := ldVersion
	mu.RUnlock()
	if stamped != "" {
		return stamped
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	var revision string
	var dirty bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if revision == "" {
		return "devel"
	}

	version := "devel+" + revision
	if len(version) > 20 {
		version = version[:20]
	}
	if dirty {
		version += "+dirty"
	}
	return version
}
