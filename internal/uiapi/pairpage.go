package uiapi

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
)

// SessionStorageKey names the legacy bearer key, removed during migration.
// Browser authentication now persists only a non-extractable CryptoKey.
const SessionStorageKey = "sidecar.session"

// pairScript runs on GET /pair. It reads the code and next from the fragment,
// drops them from history, registers a public key, persists the non-extractable
// private key in IndexedDB, and lands on next only if it stays on this origin.
const pairScript = `(async () => {
  const say = (text) => { document.getElementById("status").textContent = text; };
  const params = new URLSearchParams(location.hash.slice(1));
  history.replaceState(null, "", location.pathname);
  const code = params.get("code") || "";
  let db;
  try {
    localStorage.removeItem("sidecar.session");
    if (!code) { say("This pairing link has no code. Run sidecar api open again."); return; }
    // The private half can be cloned into IndexedDB, but never exported.
    const keys = await crypto.subtle.generateKey({name: "ECDSA", namedCurve: "P-256"}, false, ["sign", "verify"]);
    const publicKey = await crypto.subtle.exportKey("jwk", keys.publicKey);
    db = await new Promise((resolve, reject) => {
      const request = indexedDB.open("sidecar-browser-auth", 1);
      request.onupgradeneeded = () => { request.result.createObjectStore("keys"); };
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
    const response = await fetch("/api/v0/pairing/exchange", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Sidecar-Request": "1" },
      body: JSON.stringify({code, next: params.get("next") || "/", public_key: publicKey}),
      credentials: "omit", cache: "no-store",
    });
    const body = await response.json();
    if (!response.ok) { throw new Error((body && body.error && body.error.message) || "Pairing failed."); }
    const target = new URL(body.next, location.origin);
    if (target.origin !== location.origin) { throw new Error("Pairing refused a redirect off this server."); }
    await new Promise((resolve, reject) => {
      const transaction = db.transaction("keys", "readwrite");
      transaction.objectStore("keys").put({registration_id: body.registration_id, origin: location.origin, privateKey: keys.privateKey, publicKey: keys.publicKey}, "active");
      transaction.oncomplete = resolve;
      transaction.onerror = () => reject(transaction.error);
      transaction.onabort = () => reject(transaction.error || new Error("Could not store the browser key."));
    });
    // The exchange's short-lived token dies with this page. The target page
    // obtains its own memory-only bearer through a fresh signed nonce.
    db.close(); db = null;
    location.replace(target.href);
  } catch (err) {
    if (db) db.close();
    say("Could not pair this browser: " + err + ". Run sidecar api open again.");
  }
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
