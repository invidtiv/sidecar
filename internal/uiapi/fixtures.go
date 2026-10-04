package uiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspacewire"
)

// FixtureBackend serves recorded resource data through the real server and
// runs the real terminal protocol Service with a synthetic capture adapter.
type FixtureBackend struct {
	workspace *workspacewire.Workspace
	catalog   mobileproto.CatalogSnapshot
	Status    Status
	targets   map[string]mobileproto.TargetIdentity
	terminal  mobile.EchoTerminal
	content   map[string]contentservice.ReadResult
	tree      contentservice.TreeResult
}

// LoadFixtures requires a project-ordered sessions.json and status.json. Files
// are bounded and decoded strictly; malformed fixture authority fails at start.
func LoadFixtures(dir string) (*FixtureBackend, error) {
	backend := &FixtureBackend{targets: map[string]mobileproto.TargetIdentity{}}
	inputs := map[string]any{"sessions.json": &backend.catalog, "status.json": &backend.Status}
	if _, err := os.Stat(filepath.Join(dir, "workspace.json")); err == nil {
		backend.workspace = &workspacewire.Workspace{}
		inputs["workspace.json"] = backend.workspace
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for name, into := range inputs {
		file, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("load UI API fixture %s: %w", name, err)
		}
		reader := &io.LimitedReader{R: file, N: mobileproto.MaxLineBytes + 1}
		decoder := json.NewDecoder(reader)
		decoder.DisallowUnknownFields()
		err = decoder.Decode(into)
		if err == nil {
			var extra any
			if e := decoder.Decode(&extra); e != io.EOF {
				err = fmt.Errorf("expected exactly one JSON document")
			}
		}
		if err == nil && reader.N == 0 {
			err = fmt.Errorf("fixture exceeds %d-byte limit", mobileproto.MaxLineBytes)
		}
		_ = file.Close()
		if err != nil {
			return nil, fmt.Errorf("load UI API fixture %s: %w", name, err)
		}
	}
	if backend.workspace != nil && !backend.workspace.ValidRemoteResult() {
		return nil, fmt.Errorf("workspace.json requires project, catalog and shell records")
	}
	q := backend.catalog.Query
	if q.Sort != "project" || q.Search != "" || len(q.Hosts)+len(q.Providers)+len(q.States) > 0 || q.ShowIdleSessions != nil {
		return nil, fmt.Errorf("sessions.json must use unfiltered project order")
	}
	for _, section := range backend.catalog.Sections {
		for _, row := range section.Rows {
			if row.AttachState == "ready" && row.Target != "" && row.ExpectedTarget != nil {
				if row.ExpectedTarget.OwnerHostID != backend.catalog.OwnerHostID || row.ExpectedTarget.OwnerConfigGeneration != backend.catalog.OwnerConfigGeneration {
					return nil, fmt.Errorf("ready fixture targets must belong to the fixture owner/configuration")
				}
				want := mobile.FixtureIdentity(row.ExpectedTarget.HubID, row.ExpectedTarget.OwnerHostID, row.ExpectedTarget.OwnerConfigGeneration, row.ExpectedTarget.WorkspaceID, row.ExpectedTarget.Session, row.ExpectedTarget.Pane)
				if want != *row.ExpectedTarget {
					return nil, fmt.Errorf("fixture target %q must use the deterministic echo identity", row.Target)
				}
				if _, exists := backend.targets[row.Target]; exists {
					return nil, fmt.Errorf("duplicate fixture target %q", row.Target)
				}
				backend.targets[row.Target] = want
			}
		}
	}
	if _, err := backend.Sessions(context.Background(), backend.catalog.Query); err != nil {
		return nil, fmt.Errorf("validate sessions.json: %w", err)
	}
	if err := backend.loadContent(dir); err != nil {
		return nil, err
	}
	return backend, nil
}
func (b *FixtureBackend) Sessions(_ context.Context, query mobileproto.CatalogQuery) (mobileproto.CatalogSnapshot, error) {
	c := b.catalog
	now, err := time.Parse(time.RFC3339Nano, c.ObservedAt)
	if err != nil {
		return mobileproto.CatalogSnapshot{}, err
	}
	sources := []mobile.CatalogSource{}
	for _, host := range c.Hosts {
		if host.State == "online" {
			owner := c
			owner.OwnerHostID = host.ID
			owner.Sections = nil
			owner.Failures = nil
			for _, section := range c.Sections {
				rows := []mobileproto.CatalogRow{}
				for _, row := range section.Rows {
					if row.OwnerHostID == host.ID {
						rows = append(rows, row)
					}
				}
				if len(rows) > 0 {
					copy := section
					copy.Rows = rows
					owner.Sections = append(owner.Sections, copy)
				}
			}
			sources = append(sources, mobile.CatalogSource{OwnerHostID: host.ID, Snapshot: owner})
		}
	}
	return mobile.ComposeCatalog(query, mobile.CatalogIdentity{HubID: c.HubID, OwnerHostID: c.OwnerHostID, OwnerConfigGeneration: c.OwnerConfigGeneration}, now, c.Hosts, sources, c.Failures)
}
func (b *FixtureBackend) ServeTerminal(ctx context.Context, input io.Reader, output io.Writer) error {
	c := b.catalog
	service, err := mobile.New(mobile.Config{Input: input, Output: output, Resolver: mobile.FixtureResolver(b.targets),
		HubID: c.HubID, OwnerHostID: c.OwnerHostID, OwnerConfigGeneration: c.OwnerConfigGeneration, Terminal: &b.terminal,
		Revalidator: func(_ context.Context, target mobile.ResolvedTarget) (mobile.ResolvedTarget, error) {
			return target, nil
		},
		HistoryCapturer: fixtureHistory, CatalogQuery: b.Sessions})
	if err != nil {
		return err
	}
	return service.Run(ctx)
}

func fixtureHistory(string, int, int, int) (tty.CaptureRange, error) {
	return tty.CaptureRange{}, &mobile.ResolveError{Code: mobileproto.ErrorUnsupported, Message: "History is unavailable in deterministic echo fixtures."}
}
