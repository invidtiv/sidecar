package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mobileproto"
)

func TestMobileSessionsReadinessFitsTheNativeRequestBudget(t *testing.T) {
	if mobileInitialOwnerWait <= 0 || mobileInitialOwnerWait >= mobileSessionsTimeout {
		t.Fatalf("initial wait %v is outside total %v", mobileInitialOwnerWait, mobileSessionsTimeout)
	}
	if mobileSessionsTimeout >= 15*time.Second {
		t.Fatalf("one-shot catalog timeout %v exceeds the native request budget", mobileSessionsTimeout)
	}
}

func TestMobileRegisteredOwnersPreserveConfigOrderDisabledStateAndFirstIdentity(t *testing.T) {
	cfg := &config.Config{Hosts: config.HostsConfig{List: []config.HostConfig{
		{ID: "book", Target: "book.local", Binary: "/opt/sidecar", Config: "/tmp/book.json", Env: []string{"A=1"}},
		{ID: "off", Target: "off.local", Disabled: true},
		{ID: "book", Target: "replacement"},
		{Target: "plain.local"},
	}}}
	owners := mobileRegisteredOwners(cfg)
	if len(owners) != 3 || owners[0].Host.ID != "book" || owners[0].Host.Target != "book.local" || owners[0].Disabled ||
		owners[1].Host.ID != "off" || !owners[1].Disabled || owners[2].Host.ID != "plain.local" {
		t.Fatalf("registered owners = %+v", owners)
	}
}

func TestMobileDirectoryProviderReloadsConfigAndLocalOwnerAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	config.SetTestConfigPath(path)
	t.Cleanup(config.ResetTestConfigPath)
	if err := os.WriteFile(path, []byte(`{"features":{"flags":{"sidecar_remote_hosts":true}},"hosts":{"list":[{"id":"book","target":"book.local"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := Env{Ctx: context.Background(), FeatureOverrides: map[string]bool{}}
	provider := mobileDirectoryProvider(env)
	first, err := provider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Remotes) != 1 || first.Remotes[0].Host.ID != "book" || first.LocalRegistrationFingerprint == "" {
		t.Fatalf("first directory = %+v", first)
	}
	owner, err := first.Local.Bind(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if owner.Authority.OwnerHostID != first.Identity.OwnerHostID || owner.Authority.RegistrationFingerprint != first.LocalRegistrationFingerprint {
		t.Fatalf("local owner authority = %+v directory=%+v", owner.Authority, first)
	}
	if err := os.WriteFile(path, []byte(`{"features":{"flags":{"sidecar_remote_hosts":true}},"hosts":{"list":[{"id":"other","target":"other.local"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := provider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Identity.OwnerConfigGeneration == first.Identity.OwnerConfigGeneration || len(second.Remotes) != 1 || second.Remotes[0].Host.ID != "other" {
		t.Fatalf("reloaded directory = %+v, first=%+v", second, first)
	}
	if err := owner.Validate(context.Background()); err == nil {
		t.Fatal("local route survived owner config replacement")
	}
}

func TestMobileServeOwnerOnlyAcceptsTheFixedRemoteInvocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	config.SetTestConfigPath(path)
	t.Cleanup(config.ResetTestConfigPath)
	if err := os.WriteFile(path, []byte(`{"projects":{"list":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(mobileproto.Request{Version: mobileproto.Version, Type: mobileproto.RequestHello, RequestID: "hello"})
	var output, stderr bytes.Buffer
	code := runMobileServe(Env{Ctx: context.Background(), Stdin: bytes.NewReader(append(request, '\n')), Stdout: &output, Stderr: &stderr, StateDir: t.TempDir()}, []string{"--stdio", "--owner-only"})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("owner-only serve code=%d stderr=%q", code, stderr.String())
	}
	var response mobileproto.Response
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &response); err != nil || response.Type != mobileproto.ResponseHello || response.RequestID != "hello" {
		t.Fatalf("owner-only hello=%+v err=%v output=%q", response, err, output.String())
	}
}
