package uiapi

import (
	"context"
	"github.com/marcus/sidecar/internal/contentservice"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Force Serve to begin after Shutdown, rather than hoping the scheduler does.
func TestLateServeCannotUnlinkSuccessorSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "fr3-late-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "api.sock")
	old, err := listenPrivateUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cancel: func() {}, dir: filepath.Dir(path)}
	s.addListener(ListenerLocal, old, ListenerInfo{})
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	next, err := listenPrivateUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = next.Close() }()
	if err := s.servers[0].Serve(old); err != http.ErrServerClosed {
		t.Fatalf("late Serve = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("late predecessor removed successor socket: %v", err)
	}
}

type heldContentBackend struct {
	ContentBackend
	entered chan struct{}
	release chan struct{}
}

func (b *heldContentBackend) ReadProject(ctx context.Context, project, workspace string, p contentservice.ReadParams) (contentservice.ReadResult, error) {
	b.entered <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
		return contentservice.ReadResult{}, ctx.Err()
	}
	return contentservice.ReadResult{}, contentservice.Rejected("fixture complete")
}
func (b *heldContentBackend) TreeProject(ctx context.Context, project, workspace string, paths []string) (contentservice.TreeResult, error) {
	b.entered <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
		return contentservice.TreeResult{}, ctx.Err()
	}
	return contentservice.TreeResult{}, contentservice.Rejected("fixture complete")
}
func TestContentHTTPAdmissionIsPerClientAndSharedAcrossReads(t *testing.T) {
	b := &heldContentBackend{entered: make(chan struct{}, 8), release: make(chan struct{})}
	h := newHarness(t, func(o *Options) { o.Content = b })
	token := h.pairOrigin("http://content.example")
	headers := map[string]string{"Authorization": "Bearer " + token}
	paths := []string{"/api/v0/projects/one/content?kind=file&target=a", "/api/v0/projects/two/tree"}
	var wg sync.WaitGroup
	released := false
	defer func() {
		if !released {
			close(b.release)
		}
		wg.Wait()
	}()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			response, data := h.browserDo(req{path: paths[i%2], header: headers})
			expect(t, response, data, 403, "rejected")
		}(i)
		select {
		case <-b.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("request did not enter backend")
		}
	}
	// An over-budget request must return immediately, without entering or waiting.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", h.s.BrowserURL()+paths[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := h.browser.Do(request)
	if err != nil {
		t.Fatalf("over-budget request waited instead of refusing: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	data, _ := io.ReadAll(response.Body)
	expect(t, response, data, 429, "too_many_outstanding")
	// A different credential has independent capacity; cancellation releases it.
	other := h.pairOrigin("http://other-content.example")
	otherCtx, otherCancel := context.WithCancel(context.Background())
	otherRequest, _ := http.NewRequestWithContext(otherCtx, "GET", h.s.BrowserURL()+paths[0], nil)
	otherRequest.Header.Set("Authorization", "Bearer "+other)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, _ := h.browser.Do(otherRequest)
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("other credential blocked")
	}
	otherCancel()
	<-done
	close(b.release)
	released = true
	wg.Wait()
	response, data = h.browserDo(req{path: paths[0], header: headers})
	expect(t, response, data, 403, "rejected")
}

func TestLocalContentAdmissionReleasesDisconnectedRequests(t *testing.T) {
	b := &heldContentBackend{entered: make(chan struct{}, 8), release: make(chan struct{})}
	h := newHarness(t, func(o *Options) { o.Content = b })
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	released := false
	defer func() {
		cancel()
		if !released {
			close(b.release)
		}
		wg.Wait()
	}()
	paths := []string{"/api/v0/projects/one/content?kind=file&target=a", "/api/v0/projects/two/tree"}
	for i := 0; i < 4; i++ {
		request, err := http.NewRequestWithContext(ctx, "GET", "http://local"+paths[i%2], nil)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, _ := h.local.Do(request)
			if response != nil {
				_ = response.Body.Close()
			}
		}()
		select {
		case <-b.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("local request did not enter backend")
		}
	}
	requestCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	request, _ := http.NewRequestWithContext(requestCtx, "GET", "http://local"+paths[0], nil)
	response, err := h.local.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	expect(t, response, data, 429, "too_many_outstanding")
	cancel() // Close all four clients while their reads are in the backend.
	wg.Wait()
	// All four slots must disappear, including requests whose canceled client
	// returned before the server noticed its closed connection.
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.s.contentRequests.mu.Lock()
		remaining := len(h.s.contentRequests.used)
		h.s.contentRequests.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected Local reads retained admission slots")
		}
		time.Sleep(time.Millisecond)
	}
	close(b.release)
	released = true
	request, _ = http.NewRequestWithContext(requestCtx, "GET", "http://local"+paths[1], nil)
	response, err = h.local.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	expect(t, response, data, 403, "rejected")
}
