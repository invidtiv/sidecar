package configchecks

import (
	"fmt"
	"strconv"
	"strings"
)

// minTmuxMajor and minTmuxMinor are the oldest tmux Sidecar relies on. Below
// this, control mode and the pane options the embedded terminal uses are either
// missing or differently spelled.
const (
	minTmuxMajor = 3
	minTmuxMinor = 4
)

// MinTmuxVersion is the requirement, phrased for the user.
const MinTmuxVersion = "3.4"

// checkTmux reports tmux availability and version. Availability stays a
// LookPath — the same one the shell operations use — so the check and the thing
// it predicts agree.
func checkTmux(in Input) Result {
	env := in.env()
	problem := Result{
		ID:           CheckTmux,
		Title:        "tmux",
		Action:       "Set up tmux",
		ActionDetail: "Workspaces and embedded shells need tmux " + MinTmuxVersion + "+",
		Badge:        BadgeFix,
		Repair:       RepairTmux,
	}

	path, err := env.LookPath("tmux")
	if err != nil || path == "" {
		problem.Summary = "Not found on PATH · workspaces need tmux " + MinTmuxVersion + "+"
		problem.Evidence = []string{"tmux was not found on PATH."}
		return problem
	}

	out, err := env.Output("tmux", "-V")
	raw := strings.TrimSpace(string(out))
	if err != nil {
		// tmux answers, but not in a way we can read. It is installed, which is
		// the part that gates workspaces, so this is a note rather than a fault.
		return Result{
			ID: CheckTmux, Title: "tmux", OK: true,
			Summary:  "Installed · version could not be determined",
			Evidence: []string{"Found at " + path, "`tmux -V` failed: " + err.Error()},
		}
	}

	version, ok := ParseTmuxVersion(raw)
	if !ok {
		return Result{
			ID: CheckTmux, Title: "tmux", OK: true,
			Summary:  "Installed · version could not be determined",
			Evidence: []string{"Found at " + path, "`tmux -V` reported " + strconv.Quote(raw)},
		}
	}
	if version.AtLeast(minTmuxMajor, minTmuxMinor) {
		return Result{
			ID: CheckTmux, Title: "tmux", OK: true,
			Summary:  "Version " + version.String() + " available",
			Evidence: []string{"Found at " + path, "`tmux -V` reported " + raw},
		}
	}
	problem.Summary = fmt.Sprintf("Version %s is older than tmux %s", version.String(), MinTmuxVersion)
	problem.Evidence = []string{"Found at " + path, "`tmux -V` reported " + raw}
	return problem
}

// TmuxVersion is a parsed tmux version. Suffixed releases (3.2a) and
// development builds (next-3.6) reduce to their numeric part.
type TmuxVersion struct {
	Major, Minor int
	Raw          string
}

func (v TmuxVersion) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// AtLeast reports whether the version meets a minimum.
func (v TmuxVersion) AtLeast(major, minor int) bool {
	if v.Major != major {
		return v.Major > major
	}
	return v.Minor >= minor
}

// ParseTmuxVersion reads the output of `tmux -V`. It reports false for the
// builds that carry no number at all (`tmux master`, some OpenBSD packages),
// which callers treat as "installed, version unknown" rather than as a fault.
func ParseTmuxVersion(out string) (TmuxVersion, bool) {
	digits := strings.IndexFunc(out, func(r rune) bool { return r >= '0' && r <= '9' })
	if digits < 0 {
		return TmuxVersion{}, false
	}
	rest := out[digits:]
	major, rest := leadingInt(rest)
	if major < 0 {
		return TmuxVersion{}, false
	}
	minor := 0
	if strings.HasPrefix(rest, ".") {
		if parsed, _ := leadingInt(rest[1:]); parsed >= 0 {
			minor = parsed
		}
	}
	return TmuxVersion{Major: major, Minor: minor, Raw: strings.TrimSpace(out)}, true
}

func leadingInt(s string) (int, string) {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return -1, s
	}
	value, err := strconv.Atoi(s[:end])
	if err != nil {
		return -1, s
	}
	return value, s[end:]
}

// TmuxInstallCommand is the command Sidecar recommends for this machine. It is
// only ever shown or prefilled — never executed by Sidecar, and never with sudo
// on a machine where Homebrew can do the job without it.
func TmuxInstallCommand(env Env) string {
	env = env.withDefaults()
	if HomebrewAvailable(env) {
		return "brew install tmux"
	}
	switch env.GOOS {
	case "darwin":
		// No Homebrew: recommending a sudo package manager Sidecar cannot see
		// would be a guess, so point at the install the user can verify.
		return "See https://github.com/tmux/tmux/wiki/Installing"
	case "linux":
		return "sudo apt install tmux   # or: sudo dnf install tmux"
	default:
		return "Install tmux from your package manager"
	}
}

// HomebrewAvailable reports whether `brew` is on PATH.
func HomebrewAvailable(env Env) bool {
	env = env.withDefaults()
	path, err := env.LookPath("brew")
	return err == nil && path != ""
}

// TmuxRepairPrefillable reports whether the recommended command is safe to
// prefill into a shell: a real command, and never one that needs sudo.
func TmuxRepairPrefillable(env Env) bool {
	command := TmuxInstallCommand(env)
	return strings.HasPrefix(command, "brew install")
}

func plural(count int, suffix string) string {
	return fmt.Sprintf("%d %s", count, suffix)
}
