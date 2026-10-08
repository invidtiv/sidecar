package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/uiapi"
)

func TestResolveTailnetMode(t *testing.T) {
	for _, tc := range []struct {
		explicit   uiapi.TailnetMode
		configured string
		port       int
		want       uiapi.TailnetMode
		fails      bool
	}{
		{want: uiapi.TailnetModeDirect},
		{port: 7862, want: uiapi.TailnetModeServe},
		{configured: "serve", want: uiapi.TailnetModeServe},
		{configured: " Direct ", want: uiapi.TailnetModeDirect},
		{explicit: uiapi.TailnetModeServe, configured: "direct", want: uiapi.TailnetModeServe},
		{explicit: uiapi.TailnetModeDirect, configured: "serve", want: uiapi.TailnetModeDirect},
		{configured: "funnel", fails: true},
		{configured: "direct", port: 7862, fails: true},
	} {
		got, err := resolveTailnetMode(tc.explicit, tc.configured, tc.port)
		if tc.fails != (err != nil) || (!tc.fails && got != tc.want) {
			t.Fatalf("%+v: got %q, %v", tc, got, err)
		}
	}
}

// Direct mode asks Tailscale nothing at start, so a down Tailscale delays only
// the Tailnet listener; logins and the port come from config.
func TestAPITailnetOptionsDirect(t *testing.T) {
	apiStateTree(t, t.TempDir())
	previous := apiTailnetIdentity
	t.Cleanup(func() { apiTailnetIdentity = previous })
	apiTailnetIdentity = func(context.Context) (uiapi.TailnetIdentity, error) {
		t.Fatal("direct mode read the node identity at start")
		return uiapi.TailnetIdentity{}, nil
	}
	options, err := apiTailnetOptions(context.Background(), 0, uiapi.TailnetModeDirect)
	if err != nil || options.Mode != uiapi.TailnetModeDirect || len(options.Logins) != 0 || options.HTTPSPort != 0 {
		t.Fatalf("options = %+v, %v", options, err)
	}
	if err := os.WriteFile(config.ConfigPath(), []byte(`{"api":{"tailnetMode":"direct","tailnetLogins":["a@example.com"],"tailnetHTTPSPort":7867}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	options, err = apiTailnetOptions(context.Background(), 0, uiapi.TailnetModeDirect)
	if err != nil || strings.Join(options.Logins, ",") != "a@example.com" || options.HTTPSPort != 7867 {
		t.Fatalf("configured = %+v, %v", options, err)
	}
	cfg, err := config.Load()
	if err != nil || cfg.API.TailnetMode != "direct" {
		t.Fatalf("config tailnetMode = %q, %v", cfg.API.TailnetMode, err)
	}
	if err := os.WriteFile(config.ConfigPath(), []byte(`{"api":{"tailnetHTTPSPort":70000}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := apiTailnetOptions(context.Background(), 0, uiapi.TailnetModeDirect); err == nil {
		t.Fatal("accepted an out-of-range port")
	}
}

func TestAPIServeTailnetModeUsageErrors(t *testing.T) {
	apiStateTree(t, t.TempDir())
	for _, args := range [][]string{
		{"api", "serve", "--tailnet-mode", "funnel"},
		{"api", "serve", "--tailnet-mode", "direct", "--tailnet-port", "7862"},
		{"api", "serve", "--tailnet-mode"},
	} {
		if code, _, _ := runAPICLI(t, args...); code != 2 {
			t.Fatalf("%v: code %d, want 2", args, code)
		}
	}
}

func TestPrintTailnetDirect(t *testing.T) {
	var out bytes.Buffer
	printTailnetDirect(&uiapi.TailnetStatus{Mode: uiapi.TailnetModeDirect, State: uiapi.TailnetStateListening, Origin: "https://node.example.ts.net:7861",
		Addresses: []string{"100.64.0.10:7861"}, Logins: []string{"owner@example.com"}, Since: time.Now()}, &out)
	if !strings.Contains(out.String(), "https://node.example.ts.net:7861 (direct on 100.64.0.10:7861; logins owner@example.com)") || strings.Contains(out.String(), "tailscale serve") {
		t.Fatalf("listening = %q", out.String())
	}
	out.Reset()
	printTailnetDirect(&uiapi.TailnetStatus{Mode: uiapi.TailnetModeDirect, State: uiapi.TailnetStatePortHeld, Message: "Remove it with `tailscale serve --https=7861 off`."}, &out)
	if !strings.Contains(out.String(), "port_held (direct): Remove it with `tailscale serve --https=7861 off`.") {
		t.Fatalf("port held = %q", out.String())
	}
}
