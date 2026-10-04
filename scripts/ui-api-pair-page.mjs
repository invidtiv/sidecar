// Runs the pairing page's own script, as served by GET /pair, in Node with the
// handful of browser globals it touches, against a live `sidecar api serve`.
// Used by scripts/ui-api-proof.sh when node is installed.
//
// usage: node scripts/ui-api-pair-page.mjs PAGE_HTML PAIRING_URL
// Prints {"stored": <token>, "navigated": <url>} or exits non-zero.
import { readFileSync } from "node:fs";

const [page, link] = process.argv.slice(2);
const html = readFileSync(page, "utf8");
const match = html.match(/<script>([\s\S]*)<\/script>/);
if (!match) throw new Error("no inline script in the pairing page");
const url = new URL(link);

const stored = {};
let navigated = null;
let status = "";
const done = new Promise((resolve) => {
  globalThis.location = {
    hash: url.hash,
    pathname: url.pathname,
    origin: url.origin,
    replace: (target) => { navigated = target; resolve(); },
  };
  globalThis.history = { replaceState: () => { globalThis.location.hash = ""; } };
  globalThis.document = { getElementById: () => ({ set textContent(v) { status = v; resolve(); } }) };
  globalThis.localStorage = { setItem: (k, v) => { stored[k] = v; } };
  // A browser resolves the relative URL against the page and adds Origin.
  const realFetch = globalThis.fetch;
  globalThis.fetch = (path, init) => realFetch(new URL(path, url.origin), {
    ...init, headers: { ...init.headers, Origin: url.origin },
  });
});
new Function(match[1])();
await done;
if (!navigated) {
  console.error("pairing page stopped: " + status);
  process.exit(1);
}
console.log(JSON.stringify({ stored: stored["sidecar.session"] || null, navigated }));
