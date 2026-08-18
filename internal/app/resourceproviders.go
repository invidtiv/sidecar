package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/resourceprovider"
	"github.com/marcus/sidecar/internal/startuptrace"
)

// Terminal resource providers must never enter the first-frame path. Not
// plugin.Init, not config loading, not app construction, not a render path: no
// LookPath, no subprocess, no provider config read, no network before the first
// frame is on screen.
//
// Bubble Tea's command timing is not a strong enough guarantee for that. A
// command returned from Init can start running before the first render, so
// "start it from Init" would mean "start it at some point that is usually, but
// not always, after the frame". The app therefore owns an explicit one-shot
// latch, closes it from the same branch of View that marks `first ready frame`,
// and the describe command's first act is to wait on it.
//
// The consequence is worth stating: a provider that never becomes ready simply
// contributes no matcher. Nothing about startup waits for one.

// readyLatch is closed exactly once, by the first ready frame.
type readyLatch struct {
	once sync.Once
	ch   chan struct{}
}

func newReadyLatch() *readyLatch { return &readyLatch{ch: make(chan struct{})} }

func (l *readyLatch) close() { l.once.Do(func() { close(l.ch) }) }

func (l *readyLatch) wait() <-chan struct{} { return l.ch }

// firstReadyFrameLatch is package-level for the same reason firstReadyFrame is:
// View has a value receiver, so it cannot store anything, and the latch has to
// outlive every copy of the model.
var firstReadyFrameLatch = newReadyLatch()

// resourceProviderHost owns the manager and the lifetime of the work started
// after the latch opens. It is created lazily, inside the command, so nothing
// about it exists during construction or rendering.
var resourceProviderHost struct {
	mu      sync.Mutex
	manager *resourceprovider.Manager
	cancel  context.CancelFunc
}

// ResourceProvidersDescribedMsg reports the outcome of a describe pass. In M0
// it is diagnostics only: nothing in the TUI changes shape because of it.
type ResourceProvidersDescribedMsg struct {
	Statuses []resourceprovider.Status
	// SnapshotError is the error from a refused snapshot replacement, if any.
	// The previous snapshot stays live when this is set.
	SnapshotError error
}

// ResourceProviderManager returns the live manager, or nil before the first
// describe pass has started. M1 injects its read-only snapshot and Resolve into
// both workspace surfaces through this.
func ResourceProviderManager() *resourceprovider.Manager {
	resourceProviderHost.mu.Lock()
	defer resourceProviderHost.mu.Unlock()
	return resourceProviderHost.manager
}

// ShutdownResourceProviders cancels any provider work still in flight. Queued
// work is cancellable, and an invocation in progress has its process group
// killed, so nothing survives the app.
func ShutdownResourceProviders() {
	resourceProviderHost.mu.Lock()
	cancel := resourceProviderHost.cancel
	resourceProviderHost.cancel = nil
	resourceProviderHost.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// describeResourceProvidersCmd returns the command that describes every
// configured provider — after the first ready frame, never before it.
//
// It returns nil when there is nothing to do, so the ordinary case adds no
// goroutine and no waiting command at all.
func describeResourceProvidersCmd(cfg *config.Config) tea.Cmd {
	if cfg == nil {
		return nil
	}
	if !features.IsEnabled(features.TerminalResourceProviders.Name) {
		return nil
	}
	// Reading the already-parsed config struct is not I/O, but it still happens
	// inside the command rather than here, so that the decision and the work
	// sit on the same side of the latch.
	section := cfg.TerminalResources
	if len(section.Providers) == 0 {
		return nil
	}

	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())

		resourceProviderHost.mu.Lock()
		if prev := resourceProviderHost.cancel; prev != nil {
			prev()
		}
		resourceProviderHost.cancel = cancel
		resourceProviderHost.mu.Unlock()

		select {
		case <-firstReadyFrameLatch.wait():
		case <-ctx.Done():
			return nil
		}

		defer startuptrace.Begin("terminal resource providers: describe")()

		providers, disabled, err := resourceprovider.FromConfig(section, resourceprovider.Options{
			Dir: providerWorkingDir(),
			Log: slog.Default(),
		})
		if err != nil {
			slog.Warn("terminal resource providers: configuration refused", "error", err)
			return ResourceProvidersDescribedMsg{}
		}

		manager := resourceprovider.NewManager(resourceprovider.ManagerOptions{Log: slog.Default()})
		manager.SetProviders(providers, disabled)

		resourceProviderHost.mu.Lock()
		resourceProviderHost.manager = manager
		resourceProviderHost.mu.Unlock()

		statuses := manager.DescribeAll(ctx)
		if ctx.Err() != nil {
			return nil
		}
		return ResourceProvidersDescribedMsg{Statuses: statuses, SnapshotError: manager.SnapshotError()}
	}
}

// logResourceProviderStatuses records one metadata-only line per instance.
func logResourceProviderStatuses(msg ResourceProvidersDescribedMsg) {
	if msg.SnapshotError != nil {
		// A refused replacement keeps the previous snapshot live, so this is a
		// warning about what did not change, not about links disappearing.
		slog.Warn("terminal resource providers: matcher snapshot refused, keeping the previous one",
			"error", msg.SnapshotError)
	}
	for _, st := range msg.Statuses {
		attrs := []any{
			"instance", st.Instance,
			"state", string(st.State),
			"matchers", st.MatcherCount,
			"duration_ms", st.Duration.Milliseconds(),
			"outcome", st.LastOutcome,
		}
		if st.LastError != nil {
			attrs = append(attrs, "code", string(st.LastError.Code))
		}
		slog.Debug("terminal resource provider status", attrs...)
	}
}

// providerWorkingDir is the neutral directory every provider child runs in: the
// Sidecar config directory, never the selected repository. It is read, not
// created — a child whose cwd does not exist fails to spawn, which is a
// diagnosable configuration problem, not a reason to make directories from a
// background command.
func providerWorkingDir() string {
	path := config.ConfigPath()
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}
