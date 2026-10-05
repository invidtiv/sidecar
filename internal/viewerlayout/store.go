package viewerlayout

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/marcus/sidecar/internal/mobileproto"

	"github.com/marcus/sidecar/internal/panecodec"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
)

// Document uses the existing codec's tree vocabulary. It deliberately
// contains no viewer identifier or cell geometry; auth selects the viewer.
type Document struct {
	Layout *state.PaneLayoutJSON `json:"layout" jsonschema:"nullable"`
}

func validateLayout(n *state.PaneLayoutJSON, depth int, count *int) error {
	if n == nil {
		return nil
	}
	*count++
	if depth > 16 || *count > 127 {
		return fmt.Errorf("layout exceeds 16 levels or 127 nodes")
	}
	if n.Root != "" || n.Surface != "" || n.HostID != "" || n.ProjectKey != "" || n.ProjectRoot != "" || n.WorkspaceID != "" || n.WorkspaceKind != "" || n.WorkspaceKey != "" || n.Open || n.Issue != "" {
		return fmt.Errorf("layout contains host-only or legacy fields")
	}
	if n.Attachment != nil {
		if n.Split != nil || (n.Kind != panecodec.KindTerminal && n.Kind != panecodec.KindShell) || n.Session == "" {
			return fmt.Errorf("attachment is only valid on a terminal/shell leaf with a session")
		}
		if err := validateAttachment(n.Attachment); err != nil {
			return err
		}
	}
	tabs := len(n.Tabs) + len(n.IssueTabs) + len(n.NoteTabs) + len(n.DiffTabs) + len(n.ResourceTabs)
	if n.Split != nil {
		split := n.Split
		if n.Kind != "" || tabs != 0 || split.A == nil || split.B == nil || (split.Axis != "cols" && split.Axis != "rows") || split.Ratio != panelayout.ClampRatio(split.Ratio) {
			return fmt.Errorf("a split needs cols/rows, ratio 15..85 and two children")
		}
		if err := validateLayout(split.A, depth+1, count); err != nil {
			return err
		}
		return validateLayout(split.B, depth+1, count)
	}
	expected := 0
	switch n.Kind {
	case panecodec.KindTerminal, panecodec.KindShell:
		if tabs != 0 || n.Active != 0 {
			return fmt.Errorf("terminal leaves cannot carry content tabs")
		}
		return nil
	case panecodec.KindDoc:
		expected = len(n.Tabs)
	case panecodec.KindIssue:
		expected = len(n.IssueTabs)
	case panecodec.KindNote:
		expected = len(n.NoteTabs)
	case panecodec.KindDiff:
		expected = len(n.DiffTabs)
	case panecodec.KindResource:
		expected = len(n.ResourceTabs)
	default:
		return fmt.Errorf("unknown layout kind %q", n.Kind)
	}
	if tabs != expected || expected == 0 || expected > 64 || n.Active < 0 || n.Active >= expected {
		return fmt.Errorf("content leaf needs 1..64 matching tabs and a valid active index")
	}
	// The shared codec verifies reference shapes; never silently lose a tab.
	decoded, _ := panecodec.Decode(n, panecodec.Options{})
	if decoded.Root == nil || decoded.Root.Pane == nil || len(decoded.Root.Pane.Tabs) != expected {
		return fmt.Errorf("layout contains invalid content references")
	}
	return nil
}

func layoutETag(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

var ErrRequired = errors.New("read the layout first and send its ETag as If-Match")
var ErrChanged = errors.New("the layout changed; read it again before saving")
var ErrInvalid = errors.New("invalid layout")

// Store is the presentation-neutral persistence seam for viewer preferences.
// FileStore serializes read/compare/write across processes with an OS lock.
type Store interface {
	Get(viewer, project string) (Document, string, error)
	Put(viewer, project, match string, document Document) (Document, string, error)
}

type FileStore struct{ Dir string }

func (s FileStore) Get(viewer, project string) (Document, string, error) {
	return s.access(viewer, project, "", nil)
}
func (s FileStore) Put(viewer, project, match string, doc Document) (Document, string, error) {
	if match == "" {
		return Document{}, "", ErrRequired
	}
	count := 0
	if err := validateLayout(doc.Layout, 0, &count); err != nil {
		return Document{}, "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return s.access(viewer, project, match, &doc)
}

func (s FileStore) access(viewer, project, match string, next *Document) (Document, string, error) {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return Document{}, "", err
	}
	key := sha256.Sum256([]byte(viewer + "\x00" + project))
	path := filepath.Join(s.Dir, hex.EncodeToString(key[:])+".json")
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Document{}, "", err
	}
	defer func() { _ = lock.Close() }()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return Document{}, "", err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data = []byte("{\"layout\":null}\n")
		err = nil
	}
	if err != nil {
		return Document{}, "", err
	}
	var doc Document
	if err = json.Unmarshal(data, &doc); err != nil {
		return Document{}, "", err
	}
	etag := layoutETag(data)
	if next == nil {
		return doc, etag, nil
	}
	if match != etag {
		return Document{}, etag, ErrChanged
	}
	data, err = json.Marshal(next)
	if err != nil {
		return Document{}, etag, err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(s.Dir, ".layout-*")
	if err != nil {
		return Document{}, etag, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return Document{}, etag, err
	}
	return *next, layoutETag(data), nil
}

// Validate checks the shared viewer layout grammar without writing storage.
func Validate(doc Document) error {
	count := 0
	if err := validateLayout(doc.Layout, 0, &count); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

// MaxAttachmentBytes bounds the compact JSON encoding of one untrusted hint.
const MaxAttachmentBytes = 8192

func validateAttachment(a *state.PaneAttachmentJSON) error {
	data, err := json.Marshal(a)
	if err != nil || len(data) > MaxAttachmentBytes {
		return fmt.Errorf("attachment exceeds %d JSON bytes", MaxAttachmentBytes)
	}
	if len(a.Selector) == 0 || len(a.Selector) > mobileproto.MaxTargetBytes {
		return fmt.Errorf("attachment selector needs 1..%d UTF-8 bytes", mobileproto.MaxTargetBytes)
	}
	t := a.ExpectedTarget
	for _, value := range []string{a.Selector, t.HubID, scopedKeyText(t.OwnerHostID), t.OwnerConfigGeneration, scopedKeyText(t.WorkspaceID), t.WorkspaceKind, t.Session, t.Pane, t.ServerIncarnation, t.TargetGeneration} {
		if value == "" || !utf8.ValidString(value) || stringsControl(value) {
			return fmt.Errorf("attachment needs complete UTF-8 identity fields without control characters")
		}
	}
	return nil
}

// scopedKeyText allows the one unit separator (\x1f) a hub puts between a host
// and its key (hosts.ScopedKey), which /sessions rows carry in owner_host_id
// and workspace_id. It starts no terminal sequence; every other control
// character is still refused, and so is an empty side.
func scopedKeyText(value string) string {
	host, key, ok := strings.Cut(value, "\x1f")
	if !ok {
		return value
	}
	if host == "" || key == "" {
		return ""
	}
	return host + "/" + key
}
func stringsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
