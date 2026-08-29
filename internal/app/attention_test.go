package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/uirequest"
)

type attentionTestPlugin struct {
	nativeTestPlugin
	origin plugin.AttentionOrigin
}

func (p *attentionTestPlugin) ID() string                              { return workspacePluginID }
func (p *attentionTestPlugin) Update(tea.Msg) (plugin.Plugin, tea.Cmd) { return p, nil }
func (p *attentionTestPlugin) AttentionOrigin() (plugin.AttentionOrigin, bool) {
	return p.origin, p.focused
}

func TestAppPublishesOnlyChangedHostAttention(t *testing.T) {
	stateDir := t.TempDir()
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)
	p := &attentionTestPlugin{
		nativeTestPlugin: nativeTestPlugin{focused: true},
		origin:           plugin.AttentionOrigin{TmuxSession: "sidecar-ws-a", ProjectKey: "sidecar", WorkDir: "/repos/sidecar/a"},
	}
	reg := plugin.NewRegistry(nil)
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	m := Model{registry: reg, applicationFocused: true, attentionTracking: true, ui: &UIState{}}
	cmd := m.publishAttentionIfChanged()
	if cmd == nil {
		t.Fatal("first attention state was not published")
	}
	cmd()
	if duplicate := m.publishAttentionIfChanged(); duplicate != nil {
		t.Fatal("unchanged attention state scheduled another write")
	}
	live, err := uirequest.ListAttention(stateDir)
	if err != nil || len(live) != 1 || !live[0].Focused || live[0].VisibleOrigin.TmuxSession != "sidecar-ws-a" {
		t.Fatalf("published attention = %+v, %v", live, err)
	}
	m.applicationFocused = false
	cmd = m.publishAttentionIfChanged()
	if cmd == nil {
		t.Fatal("blur did not schedule an attention update")
	}
	cmd()
	live, err = uirequest.ListAttention(stateDir)
	if err != nil || len(live) != 1 || live[0].Focused || live[0].VisibleOrigin.TmuxSession != "" {
		t.Fatalf("blurred attention = %+v, %v", live, err)
	}
}
