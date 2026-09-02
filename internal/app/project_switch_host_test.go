package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/uirequest"
)

func TestHandleSwitchProjectRequest_UnknownProject(t *testing.T) {
	dir := t.TempDir()
	config.SetTestConfigPath(filepath.Join(dir, "config.json"))
	config.SetTestStateDir(filepath.Join(dir, "state"))
	t.Cleanup(config.ResetTestConfigPath)
	t.Cleanup(config.ResetTestStateDir)

	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	m, _ := scopeBaselineModel(t, "git")
	req := uirequest.Request{
		ID:     "switch-test-unknown",
		Action: uirequest.ActionSwitchProject,
		Target: uirequest.Target{Kind: "project", Value: "non-existent-proj"},
	}

	cmd := m.handleSwitchProjectRequest(req)
	if cmd != nil {
		t.Errorf("expected nil cmd on unknown project, got %v", cmd)
	}

	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil || len(acks) != 1 {
		t.Fatalf("expected 1 ack, got %d (err: %v)", len(acks), err)
	}
	if acks[0].Status != uirequest.StatusDeclined {
		t.Errorf("expected StatusDeclined, got %s", acks[0].Status)
	}
}

func TestHandleSwitchProjectRequest_PathDoesNotExist(t *testing.T) {
	dir := t.TempDir()
	config.SetTestConfigPath(filepath.Join(dir, "config.json"))
	config.SetTestStateDir(filepath.Join(dir, "state"))
	t.Cleanup(config.ResetTestConfigPath)
	t.Cleanup(config.ResetTestStateDir)

	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "ghost", Path: filepath.Join(dir, "does-not-exist")},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	m, _ := scopeBaselineModel(t, "git")
	req := uirequest.Request{
		ID:     "switch-test-missing-dir",
		Action: uirequest.ActionSwitchProject,
		Target: uirequest.Target{Kind: "project", Value: "ghost"},
	}

	cmd := m.handleSwitchProjectRequest(req)
	if cmd != nil {
		t.Errorf("expected nil cmd on missing path, got %v", cmd)
	}

	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil || len(acks) != 1 {
		t.Fatalf("expected 1 ack, got %d (err: %v)", len(acks), err)
	}
	if acks[0].Status != uirequest.StatusDeclined {
		t.Errorf("expected StatusDeclined, got %s", acks[0].Status)
	}
}

func TestHandleSwitchProjectRequest_AlreadyShowing(t *testing.T) {
	dir := t.TempDir()
	config.SetTestConfigPath(filepath.Join(dir, "config.json"))
	config.SetTestStateDir(filepath.Join(dir, "state"))
	t.Cleanup(config.ResetTestConfigPath)
	t.Cleanup(config.ResetTestStateDir)

	projDir := filepath.Join(dir, "myproj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "myproj", Path: projDir},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	m, _ := scopeBaselineModel(t, "git")
	m.ui.WorkDir = projDir

	req := uirequest.Request{
		ID:     "switch-test-already-showing",
		Action: uirequest.ActionSwitchProject,
		Target: uirequest.Target{Kind: "project", Value: "myproj"},
	}

	cmd := m.handleSwitchProjectRequest(req)
	if cmd != nil {
		t.Errorf("expected nil cmd when already showing, got %v", cmd)
	}

	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil || len(acks) != 1 {
		t.Fatalf("expected 1 ack, got %d (err: %v)", len(acks), err)
	}
	if acks[0].Status != uirequest.StatusUnchanged {
		t.Errorf("expected StatusUnchanged, got %s (reason: %q)", acks[0].Status, acks[0].Reason)
	}
}

func TestHandleSwitchProjectRequest_Success(t *testing.T) {
	dir := t.TempDir()
	config.SetTestConfigPath(filepath.Join(dir, "config.json"))
	config.SetTestStateDir(filepath.Join(dir, "state"))
	t.Cleanup(config.ResetTestConfigPath)
	t.Cleanup(config.ResetTestStateDir)

	projA := filepath.Join(dir, "proj-a")
	projB := filepath.Join(dir, "proj-b")
	if err := os.MkdirAll(projA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projB, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "proj-a", Path: projA},
		{Name: "proj-b", Path: projB},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	m, _ := scopeBaselineModel(t, "git")
	m.ui.WorkDir = projA

	req := uirequest.Request{
		ID:     "switch-test-success",
		Action: uirequest.ActionSwitchProject,
		Target: uirequest.Target{Kind: "project", Value: "proj-b"},
	}

	cmd := m.handleSwitchProjectRequest(req)
	if cmd == nil {
		t.Fatal("expected non-nil switch command")
	}

	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil || len(acks) != 1 {
		t.Fatalf("expected 1 ack, got %d (err: %v)", len(acks), err)
	}
	if acks[0].Status != uirequest.StatusOpened {
		t.Errorf("expected StatusOpened, got %s", acks[0].Status)
	}
}

func TestHandleSwitchProjectRequest_FromOverview(t *testing.T) {
	dir := t.TempDir()
	config.SetTestConfigPath(filepath.Join(dir, "config.json"))
	config.SetTestStateDir(filepath.Join(dir, "state"))
	t.Cleanup(config.ResetTestConfigPath)
	t.Cleanup(config.ResetTestStateDir)

	projA := filepath.Join(dir, "proj-a")
	projB := filepath.Join(dir, "proj-b")
	if err := os.MkdirAll(projA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projB, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{
		{Name: "proj-a", Path: projA},
		{Name: "proj-b", Path: projB},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	m, _ := scopeBaselineModel(t, "git")
	m.ui.WorkDir = projA
	_ = m.enterOverview()

	req := uirequest.Request{
		ID:     "switch-test-from-overview",
		Action: uirequest.ActionSwitchProject,
		Target: uirequest.Target{Kind: "project", Value: "proj-b"},
	}

	cmd := m.handleSwitchProjectRequest(req)
	if cmd == nil {
		t.Fatal("expected non-nil switch command")
	}

	acks, err := uirequest.ReadAcks(config.StateDir(), req.ID, req.Action)
	if err != nil || len(acks) != 1 {
		t.Fatalf("expected 1 ack, got %d (err: %v)", len(acks), err)
	}
	if acks[0].Status != uirequest.StatusOpened {
		t.Errorf("expected StatusOpened, got %s", acks[0].Status)
	}
	if m.inGlobalScope() {
		t.Errorf("expected to leave global overview scope after switch")
	}
}
