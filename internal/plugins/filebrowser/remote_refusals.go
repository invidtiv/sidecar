package filebrowser

import (
	tea "charm.land/bubbletea/v2"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
)

// What a bound Files tab will not do, and why it says so instead.
//
// Files owns no host write verb, and no host verb answers blame or a content
// search. Refusing is the finished behaviour for those, not a placeholder — and
// refusing by name matters more than refusing quietly: every one of these
// gestures has a local meaning that would apply to a same-named path on THIS
// machine, so silently doing nothing and silently doing it here look identical
// to the user right up until the wrong file is gone.

// remoteRefusals maps a tree or preview key onto what it would have done. The
// gate is one list in one place rather than a guard at each call site: a new
// write gesture that forgets its guard is a write to the wrong machine, while
// a new read gesture that forgets this list merely has to be added to it.
var remoteRefusals = map[string]string{
	"R":      "renaming",
	"m":      "moving",
	"a":      "creating a file",
	"A":      "creating a directory",
	"D":      "deleting",
	"p":      "pasting",
	"e":      "editing",
	"o":      "editing",
	"E":      "opening an external editor",
	"w":      "saving",
	"B":      "git blame",
	"I":      "file info",
	"ctrl+r": "revealing in a file manager",
	"f":      "project search",
}

// remoteRefusal is what this key would have done on this machine, or "" when
// the key is fine to run while bound.
func (p *Plugin) remoteRefusal(key string) (string, bool) {
	if !p.remoteBound() {
		return "", false
	}
	what, ok := remoteRefusals[key]
	return what, ok
}

// refuseRemoteKey answers a gesture that has no host verb behind it. It names
// the host so the sentence cannot be read as "this file cannot be renamed".
func (p *Plugin) refuseRemoteKey(what string) tea.Cmd {
	return appmsg.ShowFlash(what + " is unavailable on [" + p.ctx.HostID + "]")
}

// remoteCommandIDs are the commands a bound Files tab can actually run. Every
// other command in the local set has no host verb behind it.
var remoteCommandIDs = map[string]bool{
	"quick-open": true,
	"new-tab":    true,
	"close-tab":  true,
	"search":     true,
}

// remoteCommands is the reachable subset of the local command set, taken from
// that set rather than written out again so a renamed or re-described command
// cannot come to mean two things.
func (p *Plugin) remoteCommands() []plugin.Command {
	if !p.remoteAvailable() {
		return nil
	}
	local := p.localCommands()
	out := make([]plugin.Command, 0, len(local))
	for _, cmd := range local {
		if remoteCommandIDs[cmd.ID] {
			out = append(out, cmd)
		}
	}
	return out
}
