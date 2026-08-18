package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
)

func TestReadyLatchOpensOnce(t *testing.T) {
	latch := newReadyLatch()
	select {
	case <-latch.wait():
		t.Fatal("a fresh latch is already open")
	default:
	}
	latch.close()
	latch.close() // must not panic
	select {
	case <-latch.wait():
	default:
		t.Fatal("the latch did not open")
	}
}

func providerConfig(t *testing.T, argv0 string) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.TerminalResources.Providers = []config.TerminalResourceProviderConfig{
		{ID: "fixture", Command: []string{argv0}, Enabled: true},
	}
	return cfg
}

// The command must not exist at all when there is nothing to do, so the
// ordinary startup adds no waiting goroutine.
func TestDescribeResourceProvidersCmdIsAbsentWhenThereIsNothingToDo(t *testing.T) {
	if cmd := describeResourceProvidersCmd(nil); cmd != nil {
		t.Fatal("a nil config produced a command")
	}

	features.Init(config.Default())
	features.SetOverride(features.TerminalResourceProviders.Name, true)
	t.Cleanup(func() { features.SetOverride(features.TerminalResourceProviders.Name, false) })

	if cmd := describeResourceProvidersCmd(config.Default()); cmd != nil {
		t.Fatal("a config with no providers produced a command")
	}
}

// The feature flag is the master switch: with it off, no command exists even
// when providers are configured, so no provider process can start.
func TestDescribeResourceProvidersCmdIsGatedByTheFeatureFlag(t *testing.T) {
	features.Init(config.Default())
	features.SetOverride(features.TerminalResourceProviders.Name, false)

	cfg := providerConfig(t, "/bin/echo")
	if cmd := describeResourceProvidersCmd(cfg); cmd != nil {
		t.Fatal("the disabled feature still produced a describe command")
	}
}

// The command's first act is to wait on the latch. Until the first ready frame
// closes it, the command must not have run a provider — which, for a command
// whose only observable effect in M0 is the manager it publishes, means the
// manager must not exist yet.
func TestDescribeWaitsForTheFirstReadyFrame(t *testing.T) {
	features.Init(config.Default())
	features.SetOverride(features.TerminalResourceProviders.Name, true)
	t.Cleanup(func() { features.SetOverride(features.TerminalResourceProviders.Name, false) })

	// A command that would take a visible amount of time if it ever ran.
	marker := filepath.Join(t.TempDir(), "provider-ran")
	script := filepath.Join(t.TempDir(), "provider.sh")
	body := "#!/bin/sh\ntouch " + marker + "\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	latch := newReadyLatch()
	restore := firstReadyFrameLatch
	firstReadyFrameLatch = latch
	t.Cleanup(func() {
		firstReadyFrameLatch = restore
		ShutdownResourceProviders()
		resourceProviderHost.mu.Lock()
		resourceProviderHost.manager = nil
		resourceProviderHost.mu.Unlock()
	})

	cmd := describeResourceProvidersCmd(providerConfig(t, script))
	if cmd == nil {
		t.Fatal("no describe command was produced")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		cmd()
	}()

	// Give the command every chance to misbehave.
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a provider process ran before the first ready frame")
	}
	if ResourceProviderManager() != nil {
		t.Fatal("the provider manager was built before the first ready frame")
	}

	latch.close()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the describe command never finished after the latch opened")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the provider never ran after the first ready frame")
	}
	if ResourceProviderManager() == nil {
		t.Fatal("the manager was not published")
	}
}

// Shutdown while the command is still waiting must stop it without ever
// starting a provider.
func TestShutdownBeforeTheFirstFrameStartsNothing(t *testing.T) {
	features.Init(config.Default())
	features.SetOverride(features.TerminalResourceProviders.Name, true)
	t.Cleanup(func() { features.SetOverride(features.TerminalResourceProviders.Name, false) })

	marker := filepath.Join(t.TempDir(), "provider-ran")
	script := filepath.Join(t.TempDir(), "provider.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	latch := newReadyLatch()
	restore := firstReadyFrameLatch
	firstReadyFrameLatch = latch
	t.Cleanup(func() {
		firstReadyFrameLatch = restore
		resourceProviderHost.mu.Lock()
		resourceProviderHost.manager = nil
		resourceProviderHost.mu.Unlock()
	})

	cmd := describeResourceProvidersCmd(providerConfig(t, script))
	done := make(chan struct{})
	go func() {
		defer close(done)
		cmd()
	}()

	time.Sleep(100 * time.Millisecond)
	ShutdownResourceProviders()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not release the waiting command")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a provider ran after shutdown")
	}
}
