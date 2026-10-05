package uiapi

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// newStaticHandler serves `--ui DIR` with the single-page-app fallback, or the
// plain no-UI page when no directory was given.
func newStaticHandler(dir string) (http.Handler, error) {
	if dir == "" {
		return http.HandlerFunc(serveNoUIPage), nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("--ui %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("--ui %s is not a directory", dir)
	}
	if _, err := os.Stat(filepath.Join(abs, "index.html")); err != nil {
		return nil, fmt.Errorf("--ui %s must contain index.html", dir)
	}
	// os.Root, not os.DirFS: a symlink inside DIR that points outside it must
	// not be followed, because static files on the Browser listener need no
	// credential and so are readable by anything that can reach loopback.
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("--ui %s: %w", dir, err)
	}
	_ = root.Close()
	return &spaHandler{dir: abs}, nil
}

type spaHandler struct {
	dir string
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Builds can remove and recreate DIR. Resolve a fresh root for each
	// request, then keep that descriptor for the whole response so every
	// lookup still has os.Root's symlink-escape protection.
	root, err := os.OpenRoot(h.dir)
	if err != nil {
		http.Error(w, "The UI is being rebuilt; try again shortly.", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = root.Close() }()
	fsys := root.FS()
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" {
		if info, err := fs.Stat(fsys, name); err == nil && !info.IsDir() {
			http.ServeFileFS(w, r, fsys, name)
			return
		}
	}
	// Every other path is a client-side route of the app.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, fsys, "index.html")
}

const noUIPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sidecar API</title>
<style>
body { font: 16px/1.5 system-ui, sans-serif; max-width: 40rem; margin: 3rem auto; padding: 0 1rem; color: #1f2328; background: #fff; }
code { font: 14px ui-monospace, monospace; background: #f3f4f6; padding: 0.1rem 0.3rem; border-radius: 4px; }
@media (prefers-color-scheme: dark) { body { color: #e6edf3; background: #0d1117; } code { background: #161b22; } }
</style>
</head>
<body>
<h1>Sidecar API</h1>
<p>The Sidecar UI API is running. No UI directory is configured.</p>
<p>To serve your built web UI at login, run <code>sidecar api service install --ui DIR</code>, where DIR contains your UI's <code>index.html</code>. Stop a foreground API server before installing the service. Then run <code>sidecar api open</code> to pair this browser and open it.</p>
<p>For a foreground server, use <code>sidecar api serve --ui DIR</code>.</p>
<p>To embed Sidecar in another app, pair its origin with <code>sidecar api pair --origin URL</code>.</p>
<p>The API lives under <code>/api/v0/</code>. Learn how to <a href="https://sidecar.haplab.com/docs/build-your-own-ui">build your own Sidecar UI</a> or <a href="https://sidecar.haplab.com/docs/web-ui">use the web UI</a>.</p>
</body>
</html>
`

func serveNoUIPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(noUIPage))
}
