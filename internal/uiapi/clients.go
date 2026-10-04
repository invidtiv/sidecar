package uiapi

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
)

// caller is who a request was trusted as.
type caller struct {
	listener Listener
	auth     string // local, session, bearer, ticket, tailnet
	origin   string
	login    string
	// client identifies one credential holder for per-client limits: a
	// browser session, a paired origin, a tailnet login, or local.
	client string
}

// maxTerminalsPerClient bounds the terminal WebSockets one client may hold
// open at once. Local callers are trusted like the tmux socket and are not
// limited.
const maxTerminalsPerClient = 16

// ClientInfo is one connected client in status.
type ClientInfo struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Listener Listener  `json:"listener"`
	Auth     string    `json:"auth"`
	Origin   string    `json:"origin,omitempty"`
	Login    string    `json:"login,omitempty"`
	Since    time.Time `json:"since"`
}

// TerminalInfo is one open terminal attachment and whether its client holds
// control. It is observed from the stream, never decided here.
type TerminalInfo struct {
	ClientID    string `json:"client_id"`
	OwnerHostID string `json:"owner_host_id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Session     string `json:"session,omitempty"`
	Pane        string `json:"pane,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Control     bool   `json:"control"`
}

type clientRegistry struct {
	mu      sync.Mutex
	now     func() time.Time
	next    uint64
	clients map[string]*trackedClient
	changes eventSignals
}

type trackedClient struct {
	info ClientInfo
	key  string
	mu   sync.Mutex
	term TerminalInfo
	open bool
	// lastErrorCode is the code of the most recent outbound error envelope
	// that was the latest message on the stream, used to pick a close code.
	lastErrorCode string
	changed       func()
}

func newClientRegistry(now func() time.Time) *clientRegistry {
	return &clientRegistry{now: now, clients: map[string]*trackedClient{}}
}

// add registers a client, refusing one more terminal than its limit.
func (r *clientRegistry) add(kind string, c caller) (*trackedClient, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.listener != ListenerLocal {
		held := 0
		for _, existing := range r.clients {
			if existing.key == c.client && existing.info.Kind == kind {
				held++
			}
		}
		if held >= maxTerminalsPerClient {
			return nil, false
		}
	}
	r.next++
	id := "c" + strconv.FormatUint(r.next, 10)
	client := &trackedClient{info: ClientInfo{ID: id, Kind: kind, Listener: c.listener, Auth: c.auth, Origin: c.origin, Login: c.login, Since: r.now().UTC()}}
	client.term.ClientID = id
	client.key = c.client
	client.changed = r.changes.signal
	r.clients[id] = client
	return client, true
}

func (r *clientRegistry) remove(client *trackedClient) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, client.info.ID)
	r.changes.signal()
}

func (r *clientRegistry) snapshot() ([]ClientInfo, []TerminalInfo) {
	r.mu.Lock()
	list := make([]*trackedClient, 0, len(r.clients))
	for _, client := range r.clients {
		list = append(list, client)
	}
	r.mu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		if !list[i].info.Since.Equal(list[j].info.Since) {
			return list[i].info.Since.Before(list[j].info.Since)
		}
		return list[i].info.ID < list[j].info.ID
	})
	clients := make([]ClientInfo, 0, len(list))
	terminals := make([]TerminalInfo, 0)
	for _, client := range list {
		clients = append(clients, client.info)
		client.mu.Lock()
		if client.open {
			terminals = append(terminals, client.term)
		}
		client.mu.Unlock()
	}
	return clients, terminals
}

var framePrefix = []byte(`{"version":0,"type":"frame"`)

// observe reads one outbound protocol line for status. Frames are skipped by
// prefix: they carry the whole screen and say nothing about control.
func (c *trackedClient) observe(line []byte) {
	if bytes.HasPrefix(line, framePrefix) {
		c.mu.Lock()
		c.lastErrorCode = ""
		c.mu.Unlock()
		return
	}
	var response struct {
		Type   string `json:"type"`
		Target *struct {
			OwnerHostID string `json:"owner_host_id"`
			WorkspaceID string `json:"workspace_id"`
			Session     string `json:"session"`
			Pane        string `json:"pane"`
			DisplayName string `json:"display_name"`
		} `json:"target"`
		Control bool               `json:"control"`
		Reason  string             `json:"reason"`
		Error   *mobileproto.Error `json:"error"`
	}
	if json.Unmarshal(line, &response) != nil {
		return
	}
	c.mu.Lock()
	wasOpen, before := c.open, c.term
	defer func() {
		changed := wasOpen != c.open || before != c.term
		c.mu.Unlock()
		if changed && c.changed != nil {
			c.changed()
		}
	}()
	c.lastErrorCode = ""
	if response.Target != nil {
		c.term.OwnerHostID, c.term.WorkspaceID = response.Target.OwnerHostID, response.Target.WorkspaceID
		c.term.Session, c.term.Pane, c.term.DisplayName = response.Target.Session, response.Target.Pane, response.Target.DisplayName
	}
	switch response.Type {
	case mobileproto.ResponseOpened, mobileproto.ResponseReconnected:
		c.open, c.term.Control = true, false
	case mobileproto.ResponseControl, mobileproto.ResponseResized, mobileproto.ResponseAccepted, mobileproto.ResponseHeartbeat:
		c.term.Control = response.Control
	case mobileproto.ResponseReleased:
		c.term.Control = false
	case mobileproto.ResponseClosed:
		c.open, c.term.Control = false, false
	case mobileproto.ResponseReset:
		// A deliberate resize keeps control; every other reset revokes it.
		if response.Reason != "resize" {
			c.term.Control = false
		}
	case mobileproto.ResponseError:
		if response.Error != nil {
			c.lastErrorCode = response.Error.Code
		}
	}
}

func (c *trackedClient) finalErrorCode() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErrorCode
}

func (r *clientRegistry) terminalsFor(key string) []TerminalInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	terms := make([]TerminalInfo, 0)
	for _, client := range r.clients {
		if client.key != key {
			continue
		}
		client.mu.Lock()
		if client.open {
			terms = append(terms, client.term)
		}
		client.mu.Unlock()
	}
	sort.Slice(terms, func(i, j int) bool { return terms[i].ClientID < terms[j].ClientID })
	return terms
}
