package config

import (
	"errors"
	"fmt"
	"strings"
)

// Host registry mutations live here so every surface that registers, edits,
// removes, or switches off a remote machine asks the same questions and writes
// through the same Load→mutate→Save boundary. The validation half is
// deliberately state-free: `sidecar host add` and the Remote Hosts page both
// call it unchanged, which is the only reason the two cannot drift into
// accepting different entries.
//
// Nothing here connects to anything. Registering a host is a configuration
// change; whether the machine answers is the registry's question, asked over
// ssh, and reported as health.

// ErrHostNotFound is what a mutation returns when nothing is registered under
// the name it was given. It is a sentinel rather than a formatted string so a
// caller can distinguish "you named a host I do not have" from "the
// configuration could not be written" without reading the message — a CLI has
// to answer those two with different exit codes.
var ErrHostNotFound = errors.New("host not found")

// hostValueError marks a refusal that is about a value the caller supplied — a
// duplicate name, an empty target, a malformed env entry — rather than about
// the file. Its message is the same sentence the Configuration form shows, so
// the two surfaces refuse in the same words as well as on the same grounds.
type hostValueError struct{ message string }

func (e hostValueError) Error() string { return e.message }

// IsHostValueRejection reports whether an error is a refused value.
func IsHostValueRejection(err error) bool {
	var rejection hostValueError
	return errors.As(err, &rejection)
}

// HostIDFor is the name an entry is known by: its ID, or its target when the ID
// is empty. It matches the defaulting hosts.FromConfig does, so the name a
// surface shows is the name the running registry uses.
func HostIDFor(host HostConfig) string {
	if id := strings.TrimSpace(host.ID); id != "" {
		return id
	}
	return strings.TrimSpace(host.Target)
}

// NormalizeHost trims every field and resolves the ID default, so what is
// written is what a reader would have inferred anyway. Env is copied rather
// than aliased: an entry saved from a form must not share a slice with the
// draft that produced it.
func NormalizeHost(host HostConfig) HostConfig {
	normalized := HostConfig{
		ID:       strings.TrimSpace(host.ID),
		Target:   strings.TrimSpace(host.Target),
		Binary:   strings.TrimSpace(host.Binary),
		Config:   strings.TrimSpace(host.Config),
		Disabled: host.Disabled,
	}
	if normalized.ID == "" {
		normalized.ID = normalized.Target
	}
	for _, entry := range host.Env {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		normalized.Env = append(normalized.Env, entry)
	}
	return normalized
}

// ValidateHost reports why an entry may not be saved, or "" when it is fine.
// skipIndex excludes one existing entry from the uniqueness check so editing a
// host does not collide with itself; pass -1 when adding.
//
// The returned string is what a user reads, so it says what is wrong in plain
// language rather than naming a field. Reachability is deliberately not
// questioned: the target is whatever the user's ssh_config resolves, and a host
// that is merely switched off today is still a host worth keeping registered.
func ValidateHost(existing []HostConfig, host HostConfig, skipIndex int) string {
	host = NormalizeHost(host)

	if host.Target == "" {
		return "An SSH target is required"
	}
	if strings.ContainsAny(host.ID, " \t") {
		return "Name cannot contain spaces"
	}
	for _, entry := range host.Env {
		if message := ValidateHostEnv(entry); message != "" {
			return message
		}
	}

	for i, other := range existing {
		if i == skipIndex {
			continue
		}
		// Duplicate names are refused rather than merged. hosts.FromConfig drops
		// the second entry with a given ID silently — first registered keeps the
		// name — so a duplicate saved here would look registered and never
		// connect. A case-only difference is refused too: the ID is what scopes
		// every remote row, and two machines called "book" and "Book" produce
		// rows nobody can tell apart.
		if strings.EqualFold(HostIDFor(other), host.ID) {
			return "A host named " + host.ID + " is already registered"
		}
	}
	return ""
}

// ValidateHostEnv checks one KEY=VALUE entry. Env exists so a proof run can pin
// a host to an isolated tmux server and state tree, which means a malformed
// entry is not a cosmetic problem: it is isolation that silently did not apply.
func ValidateHostEnv(entry string) string {
	entry = strings.TrimSpace(entry)
	name, _, found := strings.Cut(entry, "=")
	if !found {
		return "Environment entries must be KEY=VALUE (got " + entry + ")"
	}
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, " \t") {
		return "Environment entries must be KEY=VALUE (got " + entry + ")"
	}
	return ""
}

// FindHost looks up an entry by the name it is known by, returning its index in
// the list. It is state-free so a caller can resolve a name against a
// configuration it already has rather than loading a second copy.
func FindHost(existing []HostConfig, id string) (HostConfig, int, bool) {
	id = strings.TrimSpace(id)
	for i, host := range existing {
		if strings.EqualFold(HostIDFor(host), id) {
			return host, i, true
		}
	}
	return HostConfig{}, -1, false
}

// AddHost registers a machine and returns the saved entry. It reloads first, so
// a host added here never clobbers a change made to the file since Sidecar
// started.
func AddHost(host HostConfig) (HostConfig, error) {
	host = NormalizeHost(host)
	cfg, err := Load()
	if err != nil {
		return HostConfig{}, err
	}
	if message := ValidateHost(cfg.Hosts.List, host, -1); message != "" {
		return HostConfig{}, hostValueError{message: message}
	}
	cfg.Hosts.List = append(cfg.Hosts.List, host)
	if err := Save(cfg); err != nil {
		return HostConfig{}, err
	}
	return host, nil
}

// UpdateHost applies a change to the host known by id. mutate receives the live
// entry from a freshly loaded configuration; the result is normalized and
// validated before it is written, so an edit cannot save something an add would
// have refused.
func UpdateHost(id string, mutate func(*HostConfig)) (HostConfig, error) {
	cfg, err := Load()
	if err != nil {
		return HostConfig{}, err
	}
	_, index, ok := FindHost(cfg.Hosts.List, id)
	if !ok {
		return HostConfig{}, fmt.Errorf("%w: %s", ErrHostNotFound, id)
	}
	entry := cfg.Hosts.List[index]
	mutate(&entry)
	entry = NormalizeHost(entry)
	if message := ValidateHost(cfg.Hosts.List, entry, index); message != "" {
		return HostConfig{}, hostValueError{message: message}
	}
	cfg.Hosts.List[index] = entry
	if err := Save(cfg); err != nil {
		return HostConfig{}, err
	}
	return entry, nil
}

// RemoveHost drops a machine from the registry. Nothing on that machine is
// touched: the entry described how to watch it, not what runs there.
func RemoveHost(id string) (HostConfig, error) {
	cfg, err := Load()
	if err != nil {
		return HostConfig{}, err
	}
	entry, index, ok := FindHost(cfg.Hosts.List, id)
	if !ok {
		return HostConfig{}, fmt.Errorf("%w: %s", ErrHostNotFound, id)
	}
	cfg.Hosts.List = append(cfg.Hosts.List[:index], cfg.Hosts.List[index+1:]...)
	if err := Save(cfg); err != nil {
		return HostConfig{}, err
	}
	return entry, nil
}
