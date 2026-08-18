package resourceprovider

import (
	"sort"
	"strings"
)

// baseEnvNames is the exact allowlist the protocol document specifies. Nothing
// else is inherited: not SHELL, not USER, not TERM, not TMUX, not the agent
// harness's variables, and never a secret Sidecar was not told to pass.
var baseEnvNames = []string{
	"PATH",
	"HOME",
	"TMPDIR",
	"LANG",
	"XDG_CONFIG_HOME",
	"XDG_CACHE_HOME",
	"XDG_STATE_HOME",
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"NO_PROXY",
	"SSL_CERT_FILE",
	"SSL_CERT_DIR",
	"GIT_SSL_CAINFO",
}

// localePrefix covers the LC_* family, which the document names as a group
// rather than by variable.
const localePrefix = "LC_"

// BuildEnv returns the complete environment for one invocation: the documented
// base, plus the current values of the names in passEnv, and nothing else.
//
// host is an os.Environ()-shaped slice, so a test can build an environment
// without touching the process's own and without a magic sentinel.
//
// A passEnv entry is a name only. Sidecar never accepts an inline secret value
// in configuration, never logs a passed value, and never renders one — which is
// also why an entry containing "=" is refused rather than split.
func BuildEnv(passEnv []string, host []string) []string {
	values := make(map[string]string, len(host))
	order := make([]string, 0, len(host))
	for _, kv := range host {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		name := kv[:i]
		if _, dup := values[name]; !dup {
			order = append(order, name)
		}
		values[name] = kv[i+1:]
	}

	seen := make(map[string]bool)
	var out []string
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		if v, ok := values[name]; ok {
			out = append(out, name+"="+v)
		}
	}

	for _, name := range baseEnvNames {
		add(name)
	}
	for _, name := range order {
		if strings.HasPrefix(name, localePrefix) {
			add(name)
		}
	}
	for _, name := range passEnv {
		name = strings.TrimSpace(name)
		if name == "" || strings.Contains(name, "=") {
			continue
		}
		add(name)
	}

	sort.Strings(out)
	return out
}
