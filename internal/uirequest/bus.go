package uirequest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

// DefaultTTL is the default time-to-live for a request.
const DefaultTTL = 15000 * time.Millisecond

// NewRequestID generates a sortable unique identifier for a request.
func NewRequestID() string {
	now := time.Now().UTC()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%016x-%s", now.UnixNano(), hex.EncodeToString(b))
}

// HostName is the machine name reported in acks, with a stable fallback.
func HostName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "localhost"
	}
	return host
}

// InstanceID identifies one acknowledging host surface. A single Sidecar
// process hosts more than one surface that can answer a request (the project
// Workspaces plugin and the global Workspaces browser), and each ack is a file
// named after this id — so the surface has to be part of it or one host's ack
// silently overwrites the other's.
func InstanceID(surface string) string {
	return fmt.Sprintf("%s-%d-%s", HostName(), os.Getpid(), surface)
}

// Dir returns and ensures the requests directory under stateDir.
func Dir(stateDir string) (string, error) {
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return "", err
	}
	dir := filepath.Join(stateDir, "requests")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// RequestPath returns the absolute path for a request file.
func RequestPath(stateDir, id string, action Action) string {
	return filepath.Join(stateDir, "requests", fmt.Sprintf("%s-%s.json", id, action))
}

// AcksDirPath returns the directory holding acknowledgements for a request.
func AcksDirPath(stateDir, id string, action Action) string {
	return filepath.Join(stateDir, "requests", fmt.Sprintf("%s-%s.acks", id, action))
}

// WriteRequest atomically creates the request file and its matching acks directory.
func WriteRequest(stateDir string, req Request) (string, error) {
	if _, err := Dir(stateDir); err != nil {
		return "", err
	}

	if req.ID == "" {
		req.ID = NewRequestID()
	}
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	if req.TTLMs <= 0 {
		req.TTLMs = int(DefaultTTL / time.Millisecond)
	}
	if req.Version == 0 {
		req.Version = 1
	}

	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	acksDir := AcksDirPath(stateDir, req.ID, req.Action)
	if err := os.MkdirAll(acksDir, 0755); err != nil {
		return "", fmt.Errorf("create acks directory: %w", err)
	}

	targetPath := RequestPath(stateDir, req.ID, req.Action)
	tmpPath := targetPath + fmt.Sprintf(".tmp.%d", os.Getpid())

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", fmt.Errorf("open tmp request: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("write tmp request: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("sync tmp request: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close tmp request: %w", err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("rename request: %w", err)
	}

	return targetPath, nil
}

// ReadRequest reads and parses a request from path.
func ReadRequest(path string) (Request, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Request{}, err
	}
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return Request{}, fmt.Errorf("unmarshal request %s: %w", path, err)
	}
	return req, nil
}

// WriteAck writes an acknowledgement file for an instance.
func WriteAck(stateDir, reqID string, action Action, ack Ack) error {
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return err
	}
	if ack.At.IsZero() {
		ack.At = time.Now().UTC()
	}
	acksDir := AcksDirPath(stateDir, reqID, action)
	if err := os.MkdirAll(acksDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(ack, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ack: %w", err)
	}

	safeName := sanitizeFileName(ack.Instance)
	if safeName == "" {
		safeName = fmt.Sprintf("inst-%d", os.Getpid())
	}
	ackPath := filepath.Join(acksDir, safeName+".json")
	tmpPath := ackPath + fmt.Sprintf(".tmp.%d", os.Getpid())

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, ackPath)
}

// ReadAcks collects all acks for a given request.
func ReadAcks(stateDir, reqID string, action Action) ([]Ack, error) {
	acksDir := AcksDirPath(stateDir, reqID, action)
	entries, err := os.ReadDir(acksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var acks []Ack
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(acksDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var ack Ack
		if err := json.Unmarshal(data, &ack); err == nil && ack.Instance != "" {
			acks = append(acks, ack)
		}
	}
	return acks, nil
}

// Cleanup removes a request file and its associated acks directory.
func Cleanup(stateDir, reqID string, action Action) error {
	reqPath := RequestPath(stateDir, reqID, action)
	_ = os.Remove(reqPath)

	acksDir := AcksDirPath(stateDir, reqID, action)
	_ = os.RemoveAll(acksDir)
	return nil
}

// Sweep removes expired requests and leftover temporary files.
func Sweep(stateDir string, now time.Time) error {
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return err
	}
	requestsDir := filepath.Join(stateDir, "requests")
	entries, err := os.ReadDir(requestsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(requestsDir, name)

		// Sweep orphan tmp files older than 1 minute
		if strings.Contains(name, ".tmp.") {
			if info, err := entry.Info(); err == nil && now.Sub(info.ModTime()) > time.Minute {
				_ = os.Remove(path)
			}
			continue
		}

		if !entry.IsDir() && strings.HasSuffix(name, ".json") {
			req, err := ReadRequest(path)
			if err != nil {
				// Malformed file older than 1 minute: remove
				if info, statErr := entry.Info(); statErr == nil && now.Sub(info.ModTime()) > time.Minute {
					_ = os.Remove(path)
				}
				continue
			}

			ttl := time.Duration(req.TTLMs) * time.Millisecond
			if ttl <= 0 {
				ttl = DefaultTTL
			}
			if now.Sub(req.CreatedAt) > ttl {
				_ = Cleanup(stateDir, req.ID, req.Action)
			}
		} else if entry.IsDir() && strings.HasSuffix(name, ".acks") {
			// Check if corresponding request json exists
			base := strings.TrimSuffix(name, ".acks")
			reqFile := filepath.Join(requestsDir, base+".json")
			if _, err := os.Stat(reqFile); os.IsNotExist(err) {
				// Orphaned acks dir older than 30s
				if info, statErr := entry.Info(); statErr == nil && now.Sub(info.ModTime()) > 30*time.Second {
					_ = os.RemoveAll(path)
				}
			}
		}
	}
	return nil
}

func sanitizeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
