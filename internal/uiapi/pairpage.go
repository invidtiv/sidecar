package uiapi

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
)

// SessionStorageKey is the localStorage key the pairing page stores the
// browser session token under. localStorage is scoped to scheme, host and
// port, so a page on any other port cannot read it.
const SessionStorageKey = "sidecar.session"

// pairScript runs on GET /pair. It reads the code and next from the fragment,
// drops them from the address bar and history, exchanges the code, stores the
// session token, and lands on next only if next stays on this origin.
const pairScript = `(async () => {
  const say = (text) => { document.getElementById("status").textContent = text; };
  const params = new URLSearchParams(location.hash.slice(1));
  history.replaceState(null, "", location.pathname);
  const code = params.get("code") || "";
  if (!code) { say("This pairing link has no code. Run sidecar api open again."); return; }
  let response, body;
  try {
    response = await fetch("/api/v0/pairing/exchange", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Sidecar-Request": "1" },
      body: JSON.stringify({ code: code, next: params.get("next") || "/" }),
      credentials: "omit",
      cache: "no-store",
    });
    body = await response.json();
  } catch (err) {
    say("Could not reach Sidecar: " + err + ". Run sidecar api open again.");
    return;
  }
  if (!response.ok) { say((body && body.error && body.error.message) || "Pairing failed. Run sidecar api open again."); return; }
  const target = new URL(body.next, location.origin);
  if (target.origin !== location.origin) { say("Pairing refused a redirect off this server."); return; }
  localStorage.setItem("` + SessionStorageKey + `", body.token);
  location.replace(target.href);
})();`

var pairScriptHash = func() string {
	sum := sha256.Sum256([]byte(pairScript))
	return base64.StdEncoding.EncodeToString(sum[:])
}()

const pairPageHead = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Pairing Sidecar</title>
<style>
body { font: 16px/1.5 system-ui, sans-serif; max-width: 40rem; margin: 3rem auto; padding: 0 1rem; color: #1f2328; background: #fff; }
@media (prefers-color-scheme: dark) { body { color: #e6edf3; background: #0d1117; } }
</style>
</head>
<body>
<h1>Pairing this browser with Sidecar</h1>
<p id="status">Pairing…</p>
<noscript><p>Pairing needs JavaScript.</p></noscript>
<script>`

const pairPageTail = `</script>
</body>
</html>
`

// servePairPage writes the pairing page. It never reads the request: the code
// is in the fragment, which the browser does not send.
func servePairPage(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Content-Security-Policy", "default-src 'none'; script-src 'sha256-"+pairScriptHash+"'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(pairPageHead + pairScript + pairPageTail))
}
