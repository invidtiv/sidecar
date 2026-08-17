package version

import (
	"strings"
	"testing"
)

func envValue(env []string, key string) (string, bool) {
	var val string
	var found bool
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			val = strings.TrimPrefix(kv, key+"=")
			found = true
		}
	}
	return val, found
}

func TestGoCommandEnvDisablesWorkspace(t *testing.T) {
	env := GoCommandEnv([]string{"PATH=/bin", "GOWORK=/repo/go.work"})
	if v, _ := envValue(env, "GOWORK"); v != "off" {
		t.Fatalf("GOWORK = %q, want off", v)
	}
	if count := strings.Count(strings.Join(env, "\n"), "GOWORK="); count != 1 {
		t.Fatalf("GOWORK appears %d times, want 1", count)
	}
	if v, _ := envValue(env, "PATH"); v != "/bin" {
		t.Fatalf("PATH = %q, want /bin", v)
	}
}

func TestGoCommandEnvPreservesUserCFlags(t *testing.T) {
	env := GoCommandEnv([]string{"CGO_CFLAGS=-O1 -DFOO"})
	v, ok := envValue(env, "CGO_CFLAGS")
	if !ok {
		t.Fatal("CGO_CFLAGS missing")
	}
	for _, want := range []string{"-O1", "-DFOO", "-Wno-nullability-completeness"} {
		if !strings.Contains(v, want) {
			t.Fatalf("CGO_CFLAGS = %q, missing %q", v, want)
		}
	}
}

func TestGoCommandEnvDefaultsAndDeduplicates(t *testing.T) {
	env := GoCommandEnv([]string{"CGO_CFLAGS=-O2 -g -Wno-nullability-completeness"})
	v, _ := envValue(env, "CGO_CFLAGS")
	if strings.Count(v, "-Wno-nullability-completeness") != 1 {
		t.Fatalf("CGO_CFLAGS = %q, suppression duplicated", v)
	}

	env = GoCommandEnv([]string{"PATH=/bin"})
	if v, _ := envValue(env, "CGO_CFLAGS"); !strings.HasPrefix(v, "-O2 -g ") {
		t.Fatalf("CGO_CFLAGS = %q, want default -O2 -g prefix", v)
	}
}
