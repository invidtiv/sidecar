// Package grok provides an adapter for Grok Build TUI sessions.
//
// Sessions live under $GROK_HOME/sessions (default ~/.grok/sessions), organized
// by URL-encoded working directory:
//
//	~/.grok/sessions/<encoded-cwd>/<session-id>/
//	  summary.json         // metadata index (title, timestamps, model, parent)
//	  chat_history.jsonl   // structured conversation for display
//	  events.jsonl         // runtime telemetry
//	  updates.jsonl        // ACP restore log
//
// Encoding matches Python urllib.parse.quote(path, safe=''): every non-unreserved
// byte is percent-encoded, so "/" becomes "%2F".
//
// Resume CLI: grok --resume <session-id>
package grok
