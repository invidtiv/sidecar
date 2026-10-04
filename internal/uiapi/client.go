package uiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// LocalClient talks to a running server over its Local Unix socket, where no
// guard or credential applies. CLI verbs and tests use it.
type LocalClient struct {
	Endpoint Endpoint
	http     *http.Client
}

// localBaseURL is a placeholder: the Local listener ignores Host.
const localBaseURL = "http://sidecar.local"

// NewLocalClient discovers the server for stateDir through endpoint.json.
func NewLocalClient(stateDir string) (*LocalClient, error) {
	endpoint, err := ReadEndpoint(stateDir)
	if err != nil {
		return nil, err
	}
	return NewLocalClientForSocket(endpoint), nil
}

// NewLocalClientForSocket builds a client for an already-known endpoint.
func NewLocalClientForSocket(endpoint Endpoint) *LocalClient {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", endpoint.UnixSocket)
	}}
	return &LocalClient{Endpoint: endpoint, http: &http.Client{Transport: transport, Timeout: 30 * time.Second}}
}

// HTTPClient exposes the socket-bound client, for WebSocket dialing.
func (c *LocalClient) HTTPClient() *http.Client { return c.http }

// URL is the request URL for path on the Local listener.
func (c *LocalClient) URL(path string) string { return localBaseURL + path }

// Do sends one request and decodes a success into out, or returns *APIError.
func (c *LocalClient) Do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.URL(path), reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("reach the Sidecar API at %s: %w", c.Endpoint.UnixSocket, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return err
	}
	if response.StatusCode >= 300 {
		var failure ErrorBody
		if json.Unmarshal(data, &failure) == nil && failure.Error.Code != "" {
			return &APIError{Status: response.StatusCode, ErrorDetail: failure.Error}
		}
		return &APIError{Status: response.StatusCode, ErrorDetail: ErrorDetail{Code: CodeBackend, Message: fmt.Sprintf("the Sidecar API answered %s", response.Status)}}
	}
	if out == nil {
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], data...)
		return nil
	}
	return json.Unmarshal(data, out)
}

// Status reads GET /api/v0/status.
func (c *LocalClient) Status(ctx context.Context) (Status, error) {
	var status Status
	err := c.Do(ctx, http.MethodGet, "/api/v0/status", nil, &status)
	return status, err
}

// PairingCode issues a one-time code for the same-origin UI.
func (c *LocalClient) PairingCode(ctx context.Context, next string) (PairingCode, error) {
	var code PairingCode
	err := c.Do(ctx, http.MethodPost, "/api/v0/pairing/codes", PairingCodeRequest{Next: next}, &code)
	return code, err
}

// PairOrigin registers origin and returns its one-time-visible token.
func (c *LocalClient) PairOrigin(ctx context.Context, origin string, scopes ...string) (OriginRegistration, error) {
	var registration OriginRegistration
	err := c.Do(ctx, http.MethodPost, "/api/v0/origins", OriginRequest{Origin: origin, Scopes: scopes}, &registration)
	return registration, err
}

// ListOrigins lists paired origins without tokens.
func (c *LocalClient) ListOrigins(ctx context.Context) (OriginList, error) {
	var list OriginList
	err := c.Do(ctx, http.MethodGet, "/api/v0/origins", nil, &list)
	return list, err
}

// RevokeSessions drops every browser session, or only origin's when origin
// is not empty.
func (c *LocalClient) RevokeSessions(ctx context.Context, origin string) (SessionRevocation, error) {
	path := "/api/v0/pairing/sessions"
	if origin != "" {
		path += "?" + url.Values{"origin": {origin}}.Encode()
	}
	var revocation SessionRevocation
	err := c.Do(ctx, http.MethodDelete, path, nil, &revocation)
	return revocation, err
}

// RevokeOrigin removes one paired origin.
func (c *LocalClient) RevokeOrigin(ctx context.Context, origin string) (OriginRevocation, error) {
	var revocation OriginRevocation
	err := c.Do(ctx, http.MethodDelete, "/api/v0/origins?"+url.Values{"origin": {origin}}.Encode(), nil, &revocation)
	return revocation, err
}
