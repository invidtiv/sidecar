package mobileproto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// ClientCapabilities opts into additive v1 behavior while retaining envelope
// version 0 for existing SSH and WebSocket clients. Omitted bits stay legacy.
type ClientCapabilities struct {
	TerminalEnded   bool `json:"terminal_ended,omitempty"`
	Presence        bool `json:"presence,omitempty"`
	ResetFreeFrames bool `json:"reset_free_frames,omitempty"`
	CoalescedFrames bool `json:"coalesced_frames,omitempty"`
	ServerPaste     bool `json:"server_paste,omitempty"`
	HolderLabels    bool `json:"holder_labels,omitempty"`
}

type Viewer struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

type Holder struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// Presence describes just the attachment named by the request envelope.
type Presence struct {
	Focused bool  `json:"focused"`
	Visible bool  `json:"visible"`
	IdleMS  int64 `json:"idle_ms"`
	Columns int   `json:"columns"`
	Rows    int   `json:"rows"`
}

func SupportedCapabilities() Capabilities {
	c := DefaultCapabilities()
	c.TerminalEnded = true
	c.Presence, c.ResetFreeFrames, c.CoalescedFrames, c.ServerPaste, c.HolderLabels = true, true, true, true, true
	return c
}

func ValidateClientHello(c *ClientCapabilities, v *Viewer) error {
	if c != nil && c.CoalescedFrames && !c.ResetFreeFrames {
		return fmt.Errorf("coalesced_frames requires reset_free_frames")
	}
	if v != nil {
		switch v.Kind {
		case "tui", "browser", "ios", "cli", "unknown":
		default:
			return fmt.Errorf("viewer kind must be tui, browser, ios, cli or unknown")
		}
		if strings.TrimSpace(v.Label) == "" || len(v.Label) > 128 || strings.ContainsFunc(v.Label, unicode.IsControl) {
			return fmt.Errorf("viewer label must be 1..128 bytes without control characters")
		}
	}
	return nil
}

func ValidatePresence(p *Presence) error {
	if p == nil || p.IdleMS < 0 || p.IdleMS > 86400000 || p.Columns < 2 || p.Columns > MaxColumns || p.Rows < 1 || p.Rows > MaxRows {
		return fmt.Errorf("presence requires focused, visible, idle_ms (0..86400000), columns (2..512) and rows (1..256)")
	}
	return nil
}

// UnmarshalJSON requires explicit false values too: omitted focus is not blur.
func (p *Presence) UnmarshalJSON(data []byte) error {
	type plain Presence
	var value plain
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"focused", "visible", "idle_ms", "columns", "rows"} {
		if raw, ok := fields[key]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("presence requires %s", key)
		}
	}
	*p = Presence(value)
	return ValidatePresence(p)
}
