package version

import "strings"

// cgoQuietWarnings are warnings the macOS SDK headers raise while cgo
// dependencies (go-sqlite3) are compiled. They are not defects in the code
// being built, but a toolchain that promotes them — a newer Xcode, or a
// CGO_CFLAGS carrying -Werror — turns them into a failed `go install`. A user
// hitting this had to run the install by hand with the same suppressions, so
// the automated path applies them itself.
var cgoQuietWarnings = []string{
	"-Wno-nullability-completeness",
	"-Wno-expansion-to-defined",
}

// GoCommandEnv returns the environment an automated `go install` runs with,
// derived from base (os.Environ() form). It is a pure function so the exact
// resulting environment is testable.
//
// Two adjustments matter:
//
//   - GOWORK=off. Sidecar usually runs with its own repository as the working
//     directory, and that repository has a go.work with local `replace`
//     directives. A workspace must not influence `go install pkg@version`:
//     it either fails outright or resolves the release against local, possibly
//     unbuildable, checkouts.
//   - CGO_CFLAGS gains the warning suppressions above, appended to whatever the
//     user already set rather than replacing it.
func GoCommandEnv(base []string) []string {
	out := make([]string, 0, len(base)+2)
	cflags := ""
	haveCFlags := false
	for _, kv := range base {
		switch {
		case strings.HasPrefix(kv, "GOWORK="):
			continue
		case strings.HasPrefix(kv, "CGO_CFLAGS="):
			cflags = strings.TrimPrefix(kv, "CGO_CFLAGS=")
			haveCFlags = true
			continue
		}
		out = append(out, kv)
	}
	if !haveCFlags {
		cflags = "-O2 -g"
	}
	fields := strings.Fields(cflags)
	for _, w := range cgoQuietWarnings {
		if !containsField(fields, w) {
			fields = append(fields, w)
		}
	}
	out = append(out, "GOWORK=off", "CGO_CFLAGS="+strings.Join(fields, " "))
	return out
}

func containsField(fields []string, want string) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}
