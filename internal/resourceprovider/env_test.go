package resourceprovider

import (
	"slices"
	"strings"
	"testing"
)

func TestBuildEnvIsAnAllowlist(t *testing.T) {
	host := []string{
		"PATH=/usr/bin",
		"HOME=/home/test",
		"TMPDIR=/tmp",
		"LANG=en_US.UTF-8",
		"LC_ALL=C",
		"LC_TIME=en_GB.UTF-8",
		"XDG_CONFIG_HOME=/home/test/.config",
		"XDG_CACHE_HOME=/home/test/.cache",
		"XDG_STATE_HOME=/home/test/.local/state",
		"XDG_DATA_HOME=/home/test/.local/share",
		"HTTP_PROXY=http://p:1",
		"HTTPS_PROXY=http://p:2",
		"NO_PROXY=localhost",
		"SSL_CERT_FILE=/certs/ca.pem",
		"SSL_CERT_DIR=/certs",
		"GIT_SSL_CAINFO=/certs/git.pem",
		// None of the following may be inherited.
		"AWS_SECRET_ACCESS_KEY=leak",
		"GITHUB_TOKEN=leak",
		"TMUX=/tmp/tmux-501/default,1,0",
		"TMUX_PANE=%3",
		"TERM=xterm-256color",
		"SHELL=/bin/zsh",
		"USER=marcus",
		"http_proxy=http://lowercase:3",
		"https_proxy=http://lowercase:4",
		"no_proxy=lowercase.localhost",
		"LOGNAME=marcus",
		"XDG_RUNTIME_DIR=/run/user/501",
		"SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
	}
	got := BuildEnv(nil, host)

	for _, want := range []string{
		"PATH=/usr/bin", "HOME=/home/test", "TMPDIR=/tmp", "LANG=en_US.UTF-8",
		"LC_ALL=C", "LC_TIME=en_GB.UTF-8",
		"XDG_CONFIG_HOME=/home/test/.config", "XDG_CACHE_HOME=/home/test/.cache",
		"XDG_STATE_HOME=/home/test/.local/state",
		"XDG_DATA_HOME=/home/test/.local/share",
		"HTTP_PROXY=http://p:1", "HTTPS_PROXY=http://p:2", "NO_PROXY=localhost",
		"SSL_CERT_FILE=/certs/ca.pem", "SSL_CERT_DIR=/certs", "GIT_SSL_CAINFO=/certs/git.pem",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("base environment missing %q", want)
		}
	}
	// The exclusions are deliberate and documented: a provider is not running
	// in a terminal and must not infer one.
	for _, forbidden := range []string{
		"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "TMUX", "TMUX_PANE",
		"TERM", "SHELL", "USER", "LOGNAME", "XDG_RUNTIME_DIR", "SSH_AUTH_SOCK",
	} {
		for _, kv := range got {
			if strings.HasPrefix(kv, forbidden+"=") {
				t.Errorf("environment inherited %q", kv)
			}
		}
	}
}

func TestBuildEnvPassEnv(t *testing.T) {
	host := []string{"PATH=/bin", "JIRA_API_TOKEN=s3cret", "OTHER=no"}
	got := BuildEnv([]string{"JIRA_API_TOKEN", " ", "MISSING_ONE"}, host)
	if !slices.Contains(got, "JIRA_API_TOKEN=s3cret") {
		t.Fatalf("passEnv value not inherited: %v", got)
	}
	for _, kv := range got {
		if strings.HasPrefix(kv, "OTHER=") || strings.HasPrefix(kv, "MISSING_ONE=") {
			t.Fatalf("unexpected entry %q", kv)
		}
	}
}

// An inline secret value in configuration is not supported. A passEnv entry
// that looks like one is dropped, never split into a name and a value.
func TestBuildEnvRefusesInlineValues(t *testing.T) {
	got := BuildEnv([]string{"JIRA_API_TOKEN=hunter2"}, []string{"PATH=/bin"})
	for _, kv := range got {
		if strings.HasPrefix(kv, "JIRA_API_TOKEN") {
			t.Fatalf("an inline passEnv value was honored: %q", kv)
		}
	}
}

func TestBuildEnvIsDeterministic(t *testing.T) {
	host := []string{"PATH=/bin", "HOME=/h", "LC_ALL=C"}
	a := BuildEnv([]string{"X"}, host)
	b := BuildEnv([]string{"X"}, host)
	if !slices.Equal(a, b) {
		t.Fatalf("BuildEnv is not stable:\n%v\n%v", a, b)
	}
	if !slices.IsSorted(a) {
		t.Fatalf("BuildEnv output is not sorted: %v", a)
	}
}

// The base set wins on conflict: naming PATH in passEnv cannot change the
// child's PATH, so a provider instance cannot quietly redirect which binaries
// its own subprocesses resolve to.
func TestBuildEnvBaseWinsOverPassEnv(t *testing.T) {
	host := []string{"PATH=/real/bin", "HOME=/real/home", "EVIL_PATH=/evil/bin"}
	got := BuildEnv([]string{"PATH", "HOME"}, host)
	if !slices.Contains(got, "PATH=/real/bin") || !slices.Contains(got, "HOME=/real/home") {
		t.Fatalf("base values were not preserved: %v", got)
	}
	// One entry each, not two.
	count := 0
	for _, kv := range got {
		if strings.HasPrefix(kv, "PATH=") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("PATH appears %d times: %v", count, got)
	}
}
