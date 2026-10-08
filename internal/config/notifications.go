package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DeliveryMode controls when an external notification channel is eligible.
type DeliveryMode string

const (
	DeliveryOff        DeliveryMode = "off"
	DeliveryBackground DeliveryMode = "background"
	DeliveryAlways     DeliveryMode = "always"

	NativeProviderAuto = "auto"
)

// SoundCue is the configured sound behavior for one notification source.
type SoundCue string

const (
	SoundNone      SoundCue = "none"
	SoundEvent     SoundCue = "event"
	SoundAttention SoundCue = "attention"
	SoundDone      SoundCue = "done"
	SoundFailure   SoundCue = "failure"
)

// TerminalNotifier names the outer terminal Sidecar encodes a notification
// sequence for when it runs directly inside an SSH terminal. It selects a fixed
// encoder and nothing else: no command, TTY path, or raw escape is configurable.
type TerminalNotifier string

const (
	TerminalNotifierOff     TerminalNotifier = "off"
	TerminalNotifierAuto    TerminalNotifier = "auto"
	TerminalNotifierGhostty TerminalNotifier = "ghostty"
	TerminalNotifierITerm2  TerminalNotifier = "iterm2"
	TerminalNotifierWezTerm TerminalNotifier = "wezterm"
	TerminalNotifierKitty   TerminalNotifier = "kitty"
)

// NotificationsConfig is the app-level `notifications` section. External
// delivery remains off unless the user explicitly changes a channel mode.
type NotificationsConfig struct {
	Native     NativeNotificationsConfig           `json:"native,omitempty"`
	Sound      SoundNotificationsConfig            `json:"sound,omitempty"`
	QuietHours QuietHoursConfig                    `json:"quietHours,omitempty"`
	SSH        SSHNotificationsConfig              `json:"ssh,omitempty"`
	Sources    map[string]NotificationSourceConfig `json:"sources,omitempty"`
}

// SSHNotificationsConfig covers the two independent remote-work paths. Both
// default off so an upgrade never moves remote notification text onto a local
// desktop, or starts writing escape sequences into a remote terminal, without
// the user asking for it.
type SSHNotificationsConfig struct {
	// ManagedHosts lets a local viewer deliver notifications forwarded by a
	// registered remote host. It controls local consumption and delivery, not
	// whether the read-only remote status stream exists.
	ManagedHosts bool `json:"managedHosts,omitempty"`
	// Terminal is the opt-in direct transport used when Sidecar itself runs in
	// an ordinary SSH terminal with no local viewer attached.
	Terminal TerminalNotifier `json:"terminal,omitempty"`
}

type NativeNotificationsConfig struct {
	Mode     DeliveryMode `json:"mode,omitempty"`
	Provider string       `json:"provider,omitempty"`
}

type SoundNotificationsConfig struct {
	Mode          DeliveryMode `json:"mode,omitempty"`
	AttentionPath string       `json:"attentionPath,omitempty"`
	DonePath      string       `json:"donePath,omitempty"`
	FailurePath   string       `json:"failurePath,omitempty"`
}

type QuietHoursConfig struct {
	Enabled bool   `json:"enabled,omitempty"`
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
}

// NotificationSourceConfig is the per-source override. Pointer booleans keep
// an explicit false distinct from an omitted value that inherits the source
// registry default.
type NotificationSourceConfig struct {
	Toast  *bool    `json:"toast,omitempty"`
	Native *bool    `json:"native,omitempty"`
	Sound  SoundCue `json:"sound,omitempty"`
	Expiry string   `json:"expiry,omitempty"`
}

// DefaultNotificationsConfig is intentionally silent. Source defaults only
// become relevant after a channel is enabled.
func DefaultNotificationsConfig() NotificationsConfig {
	return NotificationsConfig{
		Native:     NativeNotificationsConfig{Mode: DeliveryOff, Provider: NativeProviderAuto},
		Sound:      SoundNotificationsConfig{Mode: DeliveryOff},
		QuietHours: QuietHoursConfig{Start: "22:00", End: "08:00"},
		SSH:        SSHNotificationsConfig{Terminal: TerminalNotifierOff},
	}
}

// StickyExpiry is the sentinel a zero duration carries: a source whose toasts
// have no countdown.
const StickyExpiry time.Duration = 0

// SourceExpiries resolves configured expiry overrides. This remains tolerant
// for direct JSON repair and older files; interactive and CLI writes go
// through ValidateNotifications and reject invalid values before saving.
func (c NotificationsConfig) SourceExpiries() map[string]time.Duration {
	if len(c.Sources) == 0 {
		return nil
	}
	out := make(map[string]time.Duration, len(c.Sources))
	for id, src := range c.Sources {
		raw := strings.TrimSpace(src.Expiry)
		if raw == "" {
			continue
		}
		d, err := ParseNotificationExpiry(raw)
		if err != nil {
			slog.Warn("notifications: ignoring unreadable expiry", "source", id, "expiry", raw)
			continue
		}
		out[id] = d
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParseNotificationExpiry validates the persisted duration vocabulary.
func ParseNotificationExpiry(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "sticky") || strings.EqualFold(raw, "never") || raw == "0" {
		return StickyExpiry, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid notification expiry %q", raw)
	}
	return d, nil
}

// NotificationFieldError is a validation refusal that belongs to one field.
// Field is the dotted path under `notifications` (for example
// `quietHours.start` or `sources.agent.expiry`) so a form can show the
// refusal beside the control that caused it. Error keeps the established
// `notifications.<field>...` wording every surface already prints.
type NotificationFieldError struct {
	Field  string
	Reason string
	text   string
}

func (e *NotificationFieldError) Error() string { return e.text }

// notificationFieldError joins the field path and reason with sep, which is
// ": " for a wrapped cause and " " for a sentence that continues the path.
func notificationFieldError(field, sep, reason string) error {
	return &NotificationFieldError{Field: field, Reason: reason, text: "notifications." + field + sep + reason}
}

// ValidateNotifications validates a prospective targeted save. Custom sound
// paths are resolved only for validation; the user's inspectable spelling is
// retained in config.json. Every refusal is a *NotificationFieldError.
func ValidateNotifications(c NotificationsConfig, configPath string) error {
	for _, check := range []struct {
		name string
		mode DeliveryMode
	}{{"native", c.Native.Mode}, {"sound", c.Sound.Mode}} {
		switch check.mode {
		case DeliveryOff, DeliveryBackground, DeliveryAlways:
		default:
			return notificationFieldError(check.name+".mode", " ", "must be off, background, or always")
		}
	}
	if c.Native.Provider != NativeProviderAuto {
		return notificationFieldError("native.provider", " ", "must be auto")
	}
	if _, err := parseWallClock(c.QuietHours.Start); err != nil {
		return notificationFieldError("quietHours.start", ": ", err.Error())
	}
	if _, err := parseWallClock(c.QuietHours.End); err != nil {
		return notificationFieldError("quietHours.end", ": ", err.Error())
	}
	switch c.SSH.Terminal {
	case TerminalNotifierOff, TerminalNotifierAuto, TerminalNotifierGhostty,
		TerminalNotifierITerm2, TerminalNotifierWezTerm, TerminalNotifierKitty:
	default:
		return notificationFieldError("ssh.terminal", " ", "must be off, auto, ghostty, iterm2, wezterm, or kitty")
	}
	ids := make([]string, 0, len(c.Sources))
	for id := range c.Sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		source := c.Sources[id]
		if source.Sound != "" {
			switch source.Sound {
			case SoundNone, SoundEvent, SoundAttention, SoundDone, SoundFailure:
			default:
				return notificationFieldError("sources."+id+".sound", " ", "is invalid")
			}
		}
		if strings.TrimSpace(source.Expiry) != "" {
			if _, err := ParseNotificationExpiry(source.Expiry); err != nil {
				return notificationFieldError("sources."+id+".expiry", ": ", err.Error())
			}
		}
	}
	for _, check := range []struct{ name, path string }{
		{"attentionPath", c.Sound.AttentionPath},
		{"donePath", c.Sound.DonePath},
		{"failurePath", c.Sound.FailurePath},
	} {
		if err := validateSoundPath(check.path, configPath); err != nil {
			return notificationFieldError("sound."+check.name, ": ", err.Error())
		}
	}
	return nil
}

func parseWallClock(raw string) (int, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(raw))
	if err != nil || parsed.Format("15:04") != strings.TrimSpace(raw) {
		return 0, fmt.Errorf("must be HH:MM")
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

func validateSoundPath(raw, configPath string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	path := raw
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(configPath), path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("not readable: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("not readable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("must resolve to a regular file")
	}
	f, err := os.Open(resolved)
	if err != nil {
		return fmt.Errorf("not readable: %w", err)
	}
	return f.Close()
}
