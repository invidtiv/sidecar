package notifydelivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/uirequest"
)

const (
	ChannelNative = "native"
	ChannelSound  = "sound"
	DefaultLease  = 30 * time.Second
	// RemoteUnavailableReason is stable status text for the deliberately local
	// delivery boundary. Sidecar never substitutes delivery on an SSH host for
	// delivery by a future local client.
	RemoteUnavailableReason = "remote SSH session; external delivery requires a local Sidecar process"
)

var ErrUnsupported = errors.New("notifydelivery: operation unsupported")

// Cue is the provider-neutral sound selected by the notification policy.
type Cue string

const (
	CueAttention Cue = "attention"
	CueDone      Cue = "done"
	CueFailure   Cue = "failure"
)

func (c Cue) priority() int {
	switch c {
	case CueFailure:
		return 3
	case CueAttention:
		return 2
	case CueDone:
		return 1
	default:
		return 0
	}
}

// Capability describes an adapter without exposing its platform command.
type Capability struct {
	Available bool   `json:"available"`
	Provider  string `json:"provider,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Message is the bounded, sanitized input to a native-notification adapter.
// Group and ActivationBundleID are derived by Sidecar, never supplied by a
// notification body or target.
type Message struct {
	NotificationID     string
	Title              string
	Body               string
	Severity           notify.Severity
	Sticky             bool
	Group              string
	ActivationBundleID string
}

// ProviderReceipt is one adapter attempt. Delivered=false with a reason is a
// deliberate provider-level suppression, such as an audio burst losing to a
// higher-priority cue, rather than an invocation error.
type ProviderReceipt struct {
	Provider  string
	Delivered bool
	Reason    string
	At        time.Time
}

type NativeNotifier interface {
	Probe(context.Context) Capability
	Deliver(context.Context, Message) (ProviderReceipt, error)
	Remove(context.Context, string) error
}

type SoundPlayer interface {
	Probe(context.Context) Capability
	Play(context.Context, Cue) (ProviderReceipt, error)
}

type SoundCoordinator interface {
	Probe(context.Context) Capability
	PlayNotification(context.Context, string, Cue) (ProviderReceipt, error)
	Cancel(context.Context, string) error
}

type directSoundCoordinator struct{ SoundPlayer }

func (d directSoundCoordinator) PlayNotification(ctx context.Context, _ string, cue Cue) (ProviderReceipt, error) {
	return d.Play(ctx, cue)
}
func (directSoundCoordinator) Cancel(context.Context, string) error { return nil }

// Coordinator is the shared app/CLI boundary. TUI callers invoke it only from
// a tea.Cmd; CLI fallback calls it after its own fresh store append.
type Coordinator interface {
	Deliver(context.Context, Request) Result
	Remove(context.Context, notify.Notification) error
}

// StatusProvider is the read-only capability side of a Coordinator. It is a
// separate interface so small delivery fakes and alternate hosts do not need
// to claim they can probe providers. Production Service implements both.
type StatusProvider interface {
	Status(context.Context) Status
}

// Status is the provider state reported by Configuration and the CLI. Remote
// explains why both deliberately local external channels are unavailable.
type Status struct {
	Native Capability `json:"native"`
	Sound  Capability `json:"sound"`
	Remote bool       `json:"remote"`
}

// TestEvent is the stable event vocabulary accepted by every explicit-test
// surface.
type TestEvent string

const (
	TestWaiting TestEvent = "waiting"
	TestDone    TestEvent = "done"
	TestFailure TestEvent = "failure"
)

type Request struct {
	Notification notify.Notification
	Discovered   bool
	ExplicitTest bool
	// Channel narrows an explicit test to native or sound. Empty means both.
	Channel string
}

type ChannelResult struct {
	Attempted bool          `json:"attempted"`
	Provider  string        `json:"provider"`
	Delivered bool          `json:"delivered"`
	Reason    notify.Reason `json:"reason,omitempty"`
	Error     string        `json:"error"`
}

type Result struct {
	Native ChannelResult `json:"native"`
	Sound  ChannelResult `json:"sound"`
}

type AttentionResolver interface {
	Foreground(notify.Origin) (bool, error)
}

type ServiceOptions struct {
	Native           NativeNotifier
	Sound            SoundPlayer
	SoundCoordinator SoundCoordinator
	Ledger           func() (Ledger, error)
	Attention        AttentionResolver
	Config           func() notify.ResolvedConfig
	Clock            Clock
	Getenv           func(string) string
	Owner            string
	Lease            time.Duration
}

// Service resolves policy, claims independently, and invokes the two provider
// channels independently. Construction performs no I/O or capability probes.
type Service struct {
	native    NativeNotifier
	sound     SoundCoordinator
	ledgerFn  func() (Ledger, error)
	attention AttentionResolver
	config    func() notify.ResolvedConfig
	clock     Clock
	getenv    func(string) string
	owner     string
	lease     time.Duration

	ledgerMu  sync.Mutex
	ledger    Ledger
	ledgerErr error
}

var _ Coordinator = (*Service)(nil)
var _ StatusProvider = (*Service)(nil)

func NewService(opts ServiceOptions) *Service {
	clock := opts.Clock
	if clock == nil {
		clock = RealClock{}
	}
	configFn := opts.Config
	if configFn == nil {
		configFn = notify.CurrentConfig
	}
	owner := strings.TrimSpace(opts.Owner)
	if owner == "" {
		owner = fmt.Sprintf("%s:%d", uirequest.HostName(), os.Getpid())
	}
	lease := opts.Lease
	if lease <= 0 {
		lease = DefaultLease
	}
	getenv := opts.Getenv
	if getenv == nil {
		// Injecting no environment keeps constructed services deterministic.
		// Production NewDefault explicitly supplies the process environment.
		getenv = func(string) string { return "" }
	}
	sound := opts.SoundCoordinator
	if sound == nil && opts.Sound != nil {
		sound = directSoundCoordinator{SoundPlayer: opts.Sound}
	}
	return &Service{
		native: opts.Native, sound: sound, ledgerFn: opts.Ledger,
		attention: opts.Attention, config: configFn, clock: clock, getenv: getenv,
		owner: owner, lease: lease,
	}
}

// Status probes both adapters without touching the ledger or delivering
// anything. Callers must invoke it asynchronously; construction and rendering
// remain I/O-free.
func (s *Service) Status(ctx context.Context) Status {
	status := Status{Remote: remoteSession(s.getenv)}
	if status.Remote {
		status.Native = Capability{Reason: RemoteUnavailableReason}
		status.Sound = Capability{Reason: RemoteUnavailableReason}
		return status
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if s.native == nil {
			status.Native = Capability{Reason: "no native notification provider"}
			return
		}
		status.Native = s.native.Probe(ctx)
	}()
	go func() {
		defer wg.Done()
		if s.sound == nil {
			status.Sound = Capability{Reason: "no sound player"}
			return
		}
		status.Sound = s.sound.Probe(ctx)
	}()
	wg.Wait()
	return status
}

// ExplicitTestRequest builds the one provider test notification used by the
// TUI and CLI. It is never posted to notify.Store, so testing cannot create an
// unread centre record.
func ExplicitTestRequest(event TestEvent) (Request, error) {
	n := notify.Notification{ID: notify.NewID(), CreatedAt: time.Now().UTC()}
	switch event {
	case TestWaiting:
		n.Source, n.Severity, n.Title = notify.SourceWaiting, notify.SeverityWarning, "Sidecar notification test"
		n.Body = "An agent needs your input."
	case TestDone:
		n.Source, n.Severity, n.Title = notify.SourceSession, notify.SeverityInfo, "Sidecar notification test"
		n.Body = "An agent finished its turn."
	case TestFailure:
		n.Source, n.Severity, n.Title = notify.SourceSession, notify.SeverityError, "Sidecar notification test"
		n.Body = "An agent session ended."
	default:
		return Request{}, fmt.Errorf("unknown notification test event %q", event)
	}
	return Request{Notification: n, ExplicitTest: true}, nil
}

func (s *Service) getLedger() (Ledger, error) {
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	if s.ledger != nil || s.ledgerErr != nil {
		return s.ledger, s.ledgerErr
	}
	if s.ledgerFn == nil {
		s.ledgerErr = errors.New("notifydelivery: no ledger")
		return nil, s.ledgerErr
	}
	s.ledger, s.ledgerErr = s.ledgerFn()
	return s.ledger, s.ledgerErr
}

func (s *Service) Deliver(ctx context.Context, req Request) Result {
	n := req.Notification
	if n.ID == "" {
		return Result{}
	}
	now := s.clock.Now().UTC()
	foreground := false
	if s.attention != nil {
		var err error
		foreground, err = s.attention.Foreground(n.Origin)
		if err != nil {
			// Unresolved visibility is background by product contract.
			slog.Debug("notifydelivery: attention resolution failed", "id", n.ID, "err", err)
		}
	}
	runtime := notify.RuntimeContext{
		Now: now, Foreground: foreground, Discovered: req.Discovered,
		ExplicitTest: req.ExplicitTest,
		Capabilities: notify.CapabilitySet{Native: true, Sound: true},
	}
	remote := remoteSession(s.getenv)
	cfg := s.config()
	decision := notify.ResolveDelivery(n, cfg, runtime)
	decision = selectRequestedChannel(decision, req.Channel)

	var nativeCapability, soundCapability Capability
	var probeWG sync.WaitGroup
	if remote {
		runtime.Capabilities.Native = false
		runtime.Capabilities.Sound = false
	} else if decision.Native.Deliver && s.native != nil {
		probeWG.Add(1)
		go func() {
			defer probeWG.Done()
			nativeCapability = s.native.Probe(ctx)
		}()
	} else if decision.Native.Deliver {
		runtime.Capabilities.Native = false
	}
	if remote {
		// Both capability facts were resolved above without touching either
		// provider. Explicit tests bypass focus and quiet hours, not locality.
	} else if decision.Sound.Deliver && s.sound != nil {
		probeWG.Add(1)
		go func() {
			defer probeWG.Done()
			soundCapability = s.sound.Probe(ctx)
		}()
	} else if decision.Sound.Deliver {
		runtime.Capabilities.Sound = false
	}
	probeWG.Wait()
	if decision.Native.Deliver && s.native != nil {
		runtime.Capabilities.Native = nativeCapability.Available
	}
	if decision.Sound.Deliver && s.sound != nil {
		runtime.Capabilities.Sound = soundCapability.Available
	}
	decision = notify.ResolveDelivery(n, cfg, runtime)
	decision = selectRequestedChannel(decision, req.Channel)
	result := Result{
		Native: ChannelResult{Provider: nativeCapability.Provider, Reason: decision.Native.Reason},
		Sound:  ChannelResult{Provider: soundCapability.Provider, Reason: decision.Sound.Reason},
	}
	if !decision.Native.Deliver && !decision.Sound.Deliver {
		return result
	}

	ledger, err := s.getLedger()
	if err != nil {
		// Failure to coordinate is fail-closed: duplicate desktop effects are
		// worse than losing an external copy of a record retained in Sidecar.
		slog.Debug("notifydelivery: ledger unavailable", "id", n.ID, "err", err)
		applyCoordinationFailure(&result, decision, fmt.Errorf("open delivery ledger: %w", err))
		return result
	}
	if _, err := ledger.ReleaseExpired(now); err != nil {
		slog.Debug("notifydelivery: ledger maintenance failed", "id", n.ID, "err", err)
		applyCoordinationFailure(&result, decision, fmt.Errorf("maintain delivery ledger: %w", err))
		return result
	}

	var wg sync.WaitGroup
	if decision.Native.Deliver {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result.Native = s.deliverNative(ctx, ledger, n, nativeCapability)
		}()
	}
	if decision.Sound.Deliver {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result.Sound = s.deliverSound(ctx, ledger, n, decision.Cue, soundCapability)
		}()
	}
	wg.Wait()
	return result
}

func remoteSession(getenv func(string) string) bool {
	if getenv == nil {
		return false
	}
	return strings.TrimSpace(getenv("SSH_CONNECTION")) != "" || strings.TrimSpace(getenv("SSH_TTY")) != ""
}

func applyCoordinationFailure(result *Result, decision notify.DeliveryDecision, err error) {
	apply := func(channel *ChannelResult, requested bool) {
		if !requested {
			return
		}
		channel.Reason = notify.ReasonCoordination
		channel.Error = err.Error()
	}
	apply(&result.Native, decision.Native.Deliver)
	apply(&result.Sound, decision.Sound.Deliver)
}

func selectRequestedChannel(decision notify.DeliveryDecision, channel string) notify.DeliveryDecision {
	switch channel {
	case ChannelNative:
		decision.Sound = notify.ChannelDecision{Reason: notify.ReasonNotRequested}
	case ChannelSound:
		decision.Native = notify.ChannelDecision{Reason: notify.ReasonNotRequested}
	}
	return decision
}

func (s *Service) deliverNative(ctx context.Context, ledger Ledger, n notify.Notification, capability Capability) ChannelResult {
	result := ChannelResult{Provider: capability.Provider}
	operation := ledger.DeliverNative(n.ID, GroupFor(n), s.owner, s.clock.Now(), s.lease, func() (ProviderReceipt, error) {
		return s.native.Deliver(ctx, NativeMessage(n))
	})
	result.Attempted = operation.Attempted
	if operation.Receipt.Provider != "" {
		result.Provider = operation.Receipt.Provider
	}
	result.Delivered = operation.Receipt.Delivered
	if operation.Reason != "" {
		result.Reason = notify.Reason(operation.Reason)
	}
	if operation.Err != nil {
		result.Error = operation.Err.Error()
		// A failure before provider invocation, or after a provider reported
		// successful delivery, belongs to the coordination boundary. Provider
		// invocation errors remain provider errors.
		if !operation.Attempted || operation.Receipt.Delivered {
			result.Reason = notify.ReasonCoordination
		}
		slog.Debug("notifydelivery: provider failed", "id", n.ID, "channel", ChannelNative, "provider", result.Provider, "err", result.Error)
	}
	return result
}

func (s *Service) deliverSound(ctx context.Context, ledger Ledger, n notify.Notification, policyCue notify.Cue, capability Capability) ChannelResult {
	result := ChannelResult{Provider: capability.Provider}
	won, reason, err := ledger.Claim(n.ID, ChannelSound, s.owner, s.clock.Now(), s.lease)
	if err != nil {
		result.Reason = notify.ReasonCoordination
		result.Error = err.Error()
		return result
	}
	if !won {
		result.Reason = notify.Reason(reason)
		return result
	}
	result.Attempted = true
	receipt, deliveryErr := s.sound.PlayNotification(ctx, n.ID, cueFromPolicy(policyCue))
	result.Provider, result.Delivered = receipt.Provider, receipt.Delivered
	if deliveryErr != nil {
		result.Error = deliveryErr.Error()
	}
	if receipt.Reason != "" {
		result.Reason = notify.Reason(receipt.Reason)
	}
	if err := s.complete(ledger, n.ID, ChannelSound, result, receipt); err != nil {
		result.Reason = notify.ReasonCoordination
		result.Error = errors.Join(deliveryErr, fmt.Errorf("complete delivery receipt: %w", err)).Error()
	}
	return result
}

func (s *Service) complete(ledger Ledger, id, channel string, result ChannelResult, provider ProviderReceipt) error {
	receipt := Receipt{
		Owner: s.owner, Provider: result.Provider, Succeeded: result.Delivered,
		Error: result.Error, CompletedAt: provider.At,
	}
	if receipt.CompletedAt.IsZero() {
		receipt.CompletedAt = s.clock.Now().UTC()
	}
	if err := ledger.Complete(id, channel, receipt); err != nil {
		slog.Debug("notifydelivery: complete receipt failed", "id", id, "channel", channel, "provider", result.Provider, "err", err)
		return err
	}
	if result.Error != "" {
		slog.Debug("notifydelivery: provider failed", "id", id, "channel", channel, "provider", result.Provider, "err", result.Error)
	}
	return nil
}

func (s *Service) Remove(ctx context.Context, n notify.Notification) error {
	var operationErrors []error
	// Sound cancellation has an independent deadline from native delivery. Do
	// it before acquiring the delivery-ledger lock so a bounded native provider
	// call in another process cannot let the sound batch become due meanwhile.
	if s.sound != nil {
		if err := s.sound.Cancel(ctx, n.ID); err != nil {
			operationErrors = append(operationErrors, fmt.Errorf("cancel sound: %w", err))
		}
	}
	ledger, err := s.getLedger()
	if err != nil {
		operationErrors = append(operationErrors, err)
		return errors.Join(operationErrors...)
	}
	group := GroupFor(n)
	var remove func() error
	if s.native != nil && n.Sticky && group != "" {
		remove = func() error {
			capability := s.native.Probe(ctx)
			if !capability.Available {
				return nil
			}
			err := s.native.Remove(ctx, group)
			if errors.Is(err, ErrUnsupported) {
				return nil
			}
			return err
		}
	}
	now := s.clock.Now().UTC()
	if cancelErr := ledger.Cancel(n.ID, group, s.owner, now, remove).Err; cancelErr != nil {
		operationErrors = append(operationErrors, fmt.Errorf("cancel native: %w", cancelErr))
		// Do not make a second lock attempt after a failed cancellation: a
		// contended host lock is already bounded, and sound is safely cancelled.
	} else if _, err := ledger.ReleaseExpired(now); err != nil {
		operationErrors = append(operationErrors, fmt.Errorf("maintain delivery ledger: %w", err))
	}
	return errors.Join(operationErrors...)
}

func cueFromPolicy(cue notify.Cue) Cue {
	switch cue {
	case notify.CueFailure:
		return CueFailure
	case notify.CueAttention:
		return CueAttention
	default:
		return CueDone
	}
}

// NativeMessage applies one sanitization/bounds contract before any provider
// sees OS-retained text.
func NativeMessage(n notify.Notification) Message {
	return Message{
		NotificationID:     n.ID,
		Title:              truncateRunes(sanitizeText(n.Title), 120),
		Body:               truncateRunes(sanitizeText(n.Body), 500),
		Severity:           n.Severity,
		Sticky:             n.Sticky,
		Group:              GroupFor(n),
		ActivationBundleID: HostingTerminalBundle(os.Getenv),
	}
}

func GroupFor(n notify.Notification) string {
	key := n.Origin.StableKey()
	if n.Transition != nil && n.Transition.ReplacementKey != "" {
		key = n.Transition.ReplacementKey
	}
	if key == "" {
		return ""
	}
	return "sidecar-" + key
}

func sanitizeText(raw string) string {
	var out strings.Builder
	for i := 0; i < len(raw); {
		if raw[i] == 0x1b {
			// Strip OSC through BEL or ST, and strip a CSI through its final byte.
			if i+1 < len(raw) && raw[i+1] == ']' {
				i += 2
				for i < len(raw) {
					if raw[i] == 0x07 {
						i++
						break
					}
					if raw[i] == 0x1b && i+1 < len(raw) && raw[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
				continue
			}
			if i+1 < len(raw) && raw[i+1] == '[' {
				i += 2
				for i < len(raw) {
					b := raw[i]
					i++
					if b >= 0x40 && b <= 0x7e {
						break
					}
				}
				continue
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(raw[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		i += size
		if unicode.IsControl(r) {
			out.WriteByte(' ')
			continue
		}
		out.WriteRune(r)
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

func truncateRunes(raw string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(raw)
	if len(runes) <= limit {
		return raw
	}
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

// HostingTerminalBundle recognizes only fixed environment evidence. The
// fallbacks remain useful when tmux overwrites TERM_PROGRAM with "tmux".
func HostingTerminalBundle(getenv func(string) string) string {
	if getenv == nil {
		return ""
	}
	for _, name := range []string{"SIDECAR_TERM_PROGRAM", "TERM_PROGRAM", "LC_TERMINAL", "__CFBundleIdentifier"} {
		value := strings.ToLower(strings.TrimSpace(getenv(name)))
		switch value {
		case "apple_terminal", "com.apple.terminal":
			return "com.apple.Terminal"
		case "iterm.app", "iterm2", "com.googlecode.iterm2":
			return "com.googlecode.iterm2"
		case "ghostty", "com.mitchellh.ghostty":
			return "com.mitchellh.ghostty"
		case "wezterm", "com.github.wez.wezterm":
			return "com.github.wez.wezterm"
		case "warpterminal", "warp", "dev.warp.warp-stable":
			return "dev.warp.Warp-Stable"
		case "kitty", "net.kovidgoyal.kitty":
			return "net.kovidgoyal.kitty"
		case "alacritty", "org.alacritty":
			return "org.alacritty"
		}
	}
	for _, fallback := range []struct{ name, bundle string }{
		{"GHOSTTY_RESOURCES_DIR", "com.mitchellh.ghostty"},
		{"WEZTERM_EXECUTABLE", "com.github.wez.wezterm"},
		{"KITTY_WINDOW_ID", "net.kovidgoyal.kitty"},
		{"ALACRITTY_SOCKET", "org.alacritty"},
		{"WARP_IS_LOCAL_SHELL_SESSION", "dev.warp.Warp-Stable"},
	} {
		if strings.TrimSpace(getenv(fallback.name)) != "" {
			return fallback.bundle
		}
	}
	return ""
}

type stateAttention struct{ stateDir string }

func (a stateAttention) Foreground(origin notify.Origin) (bool, error) {
	records, err := uirequest.ListAttention(a.stateDir)
	if err != nil {
		return false, err
	}
	return uirequest.OriginForeground(uirequest.Origin{
		TmuxSession: origin.TmuxSession, ProjectKey: origin.ProjectKey,
		WorkDir: origin.WorkDir, HostID: origin.HostID,
	}, records), nil
}

// NewDefault constructs the production adapters without touching the cache,
// state tree, PATH, or subprocesses. Those are all lazy first-delivery work.
func NewDefault(stateDir string) Coordinator {
	runner := ExecRunner{}
	embedded := NewEmbeddedAssetCache("")
	cache := NewConfiguredAssetCache(embedded, func() (SoundPaths, error) {
		cfg, err := config.Load()
		if err != nil {
			return SoundPaths{}, err
		}
		return SoundPaths{
			ConfigPath: config.ConfigPath(),
			Attention:  cfg.Notifications.Sound.AttentionPath,
			Done:       cfg.Notifications.Sound.DonePath,
			Failure:    cfg.Notifications.Sound.FailurePath,
		}, nil
	})
	native := NewPlatformNative(runner)
	sound := NewHostSound(stateDir, NewPlatformSound(runner, cache), 75*time.Millisecond, DefaultLease, RealClock{})
	return NewService(ServiceOptions{
		Native: native, SoundCoordinator: sound,
		Ledger:    func() (Ledger, error) { return Open(stateDir) },
		Attention: stateAttention{stateDir: stateDir},
		Getenv:    os.Getenv,
	})
}
