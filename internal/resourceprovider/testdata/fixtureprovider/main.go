// Command fixtureprovider is the reference terminal resource provider used by
// Sidecar's own tests. It is a real executable, built by the test binary, so
// the host's argv, JSON, environment, timeout, and process-lifecycle handling
// are exercised against a child process rather than an in-memory fake.
//
// It describes CASH|GRES|AVATAXUI and resolves deterministic synthetic
// documents. It performs no network access, reads no credentials, and needs
// none.
//
// It also simulates the hostile cases on demand, selected either by the -mode
// flag (which is what an argv-level test uses) or by a `mode:<name>:` prefix on
// the locator (which is what a test driving one configured instance uses).
//
// It lives under testdata/ so `go build ./...` and `go vet ./...` ignore it;
// the test binary builds it explicitly.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const protocol = "sidecar.terminal-resource/v1"

type request struct {
	Protocol   string `json:"protocol"`
	Method     string `json:"method"`
	Instance   string `json:"instance"`
	DeadlineMs int64  `json:"deadlineMs"`
	Host       *struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"host,omitempty"`
	Params *struct {
		Matcher string `json:"matcher"`
		Locator string `json:"locator"`
	} `json:"params,omitempty"`
}

type matcher struct {
	ID       string `json:"id"`
	Pattern  string `json:"pattern"`
	Priority int    `json:"priority,omitempty"`
}

type info struct {
	Kind    string `json:"kind,omitempty"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	DocsURL string `json:"docsUrl,omitempty"`
}

type status struct {
	Label string `json:"label"`
	Tone  string `json:"tone,omitempty"`
}

type field struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Kind  string `json:"kind,omitempty"`
}

type body struct {
	Format string `json:"format,omitempty"`
	Text   string `json:"text"`
}

type document struct {
	Identity        string         `json:"identity"`
	Title           string         `json:"title"`
	Subtitle        string         `json:"subtitle,omitempty"`
	Status          *status        `json:"status,omitempty"`
	Fields          []field        `json:"fields,omitempty"`
	Body            *body          `json:"body,omitempty"`
	SourceURL       string         `json:"sourceUrl,omitempty"`
	UpdatedAt       string         `json:"updatedAt,omitempty"`
	FreshForSeconds float64        `json:"freshForSeconds,omitempty"`
	Extra           map[string]any `json:"aFieldTheHostHasNeverHeardOf,omitempty"`
}

type protocolError struct {
	Code      string `json:"code"`
	Message   string `json:"message,omitempty"`
	Retryable *bool  `json:"retryable,omitempty"`
	SetupHint string `json:"setupHint,omitempty"`
}

type response struct {
	Protocol string         `json:"protocol"`
	Provider *info          `json:"provider,omitempty"`
	Matchers []matcher      `json:"matchers,omitempty"`
	Resource *document      `json:"resource,omitempty"`
	Error    *protocolError `json:"error,omitempty"`
}

func main() {
	mode := flag.String("mode", "", "hostile behaviour to simulate")
	pidfile := flag.String("pidfile", "", "where a forked descendant records its pid")
	sleep := flag.Duration("sleep", 0, "how long the descendant mode sleeps")
	flag.Parse()

	if *mode == "descendant" {
		// A descendant that outlives its parent and keeps holding the inherited
		// stdout pipe. Only a process-group kill ends it.
		time.Sleep(*sleep)
		return
	}

	// Keep the raw request bytes as well as the decoded form: request-echo
	// hands them back so a host test can assert the envelope the host actually
	// wrote, rather than trusting the host's own encoder.
	rawRequest, _ := io.ReadAll(os.Stdin)
	var req request
	// A missing or unreadable request is not fatal: some modes are about what
	// the host does with the output regardless of the request.
	_ = json.Unmarshal(rawRequest, &req)

	effective := *mode
	locator := ""
	if req.Params != nil {
		locator = req.Params.Locator
	}
	if rest, ok := strings.CutPrefix(locator, "mode:"); ok {
		name, tail, _ := strings.Cut(rest, ":")
		effective = name
		locator = tail
	}

	run(effective, req, rawRequest, locator, *pidfile)
}

// resolveOnlyModes misbehave on resolve but describe normally, which is what a
// real provider that is correctly installed and returns a bad document looks
// like. Without this a host test aimed at resolve would never get past describe.
var resolveOnlyModes = map[string]bool{
	"no-identity":         true,
	"no-title":            true,
	"empty-response":      true,
	"over-limit-document": true,
	"hostile-document":    true,
	"request-echo":        true,
	"env-report":          true,
}

func run(mode string, req request, rawRequest []byte, locator, pidfile string) {
	if req.Method == "describe" && resolveOnlyModes[mode] {
		emit(describeResponse())
		return
	}
	switch mode {
	case "malformed":
		fmt.Print(`{"protocol":"sidecar.terminal-resource/v1", "resource": {`)
		return
	case "no-output":
		return
	case "extra-stdout":
		emit(describeResponse())
		fmt.Println(`{"protocol":"sidecar.terminal-resource/v1"}`)
		return
	case "trailing-log":
		emit(describeResponse())
		fmt.Println("provider: describe complete")
		return
	case "oversize":
		// One valid JSON object, far past the response byte limit.
		emit(response{
			Protocol: protocol,
			Resource: &document{
				Identity: "CASH-1",
				Title:    "oversize",
				Body:     &body{Format: "text", Text: strings.Repeat("x", 512*1024)},
			},
		})
		return
	case "bad-protocol":
		emit(response{Protocol: "sidecar.terminal-resource/v99", Provider: &info{Kind: "fixture"}})
		return
	case "no-protocol":
		fmt.Println(`{"provider":{"kind":"fixture"},"matchers":[]}`)
		return
	case "dup-matchers":
		emit(response{
			Protocol: protocol,
			Provider: &info{Kind: "fixture", Name: "Fixture"},
			Matchers: []matcher{
				{ID: "issue-key", Pattern: `\bCASH-[1-9][0-9]*\b`},
				{ID: "issue-key", Pattern: `\bGRES-[1-9][0-9]*\b`},
			},
		})
		return
	case "invalid-re2":
		emit(response{
			Protocol: protocol,
			Provider: &info{Kind: "fixture", Name: "Fixture"},
			Matchers: []matcher{{ID: "broken", Pattern: `([a-z`}},
		})
		return
	case "too-many-matchers":
		resp := response{Protocol: protocol, Provider: &info{Kind: "fixture"}}
		for i := 0; i < 40; i++ {
			resp.Matchers = append(resp.Matchers, matcher{ID: fmt.Sprintf("m%d", i), Pattern: fmt.Sprintf("X%d-[0-9]+", i)})
		}
		emit(resp)
		return
	case "crash":
		os.Exit(3)
	case "crash-after-output":
		emit(describeResponse())
		os.Exit(7)
	case "hang":
		// Not select{}: a Go program whose only goroutine blocks forever
		// panics with a deadlock, which would exercise the crash path instead
		// of the timeout path.
		time.Sleep(10 * time.Minute)
		return
	case "stderr-flood":
		chunk := strings.Repeat("noise ", 1024)
		for i := 0; i < 256; i++ {
			fmt.Fprintln(os.Stderr, chunk)
		}
		emit(answer(req, locator))
		return
	case "fork-descendant":
		forkDescendant(pidfile)
		// The parent exits immediately, without writing a response. The
		// descendant holds the inherited stdout pipe open, so only a
		// process-group kill can end the invocation.
		return
	case "error-response":
		retryable := false
		emit(response{Protocol: protocol, Error: &protocolError{
			Code:      "unauthorized",
			Message:   "fixture credentials are missing",
			Retryable: &retryable,
			SetupHint: "run fixtureprovider configure",
		}})
		return
	case "unknown-code":
		emit(response{Protocol: protocol, Error: &protocolError{Code: "teapot", Message: "unknown code"}})
		return
	case "hostile-document":
		emit(hostileDocument())
		return
	case "no-identity":
		emit(response{Protocol: protocol, Resource: &document{Title: "has a title, no identity"}})
		return
	case "no-title":
		emit(response{Protocol: protocol, Resource: &document{Identity: "CASH-1"}})
		return
	case "empty-response":
		emit(response{Protocol: protocol})
		return
	case "describe-shaped-resolve":
		emit(describeResponse())
		return
	case "resolve-shaped-describe":
		emit(resolveResponse("CASH-1"))
		return
	case "over-limit-document":
		emit(overLimitDocument())
		return
	case "request-echo":
		emit(requestEcho(rawRequest))
		return
	case "env-report":
		emit(envReport())
		return
	case "slow":
		time.Sleep(2 * time.Second)
		emit(answer(req, locator))
		return
	}

	emit(answer(req, locator))
}

func answer(req request, locator string) response {
	switch req.Method {
	case "describe":
		return describeResponse()
	case "resolve":
		return resolveResponse(locator)
	default:
		// An unknown method is an internal error, never a crash.
		return response{Protocol: protocol, Error: &protocolError{
			Code:    "internal",
			Message: "unknown method",
		}}
	}
}

func describeResponse() response {
	return response{
		Protocol: protocol,
		Provider: &info{
			Kind:    "fixture",
			Name:    "Fixture",
			Version: "1.0.0",
			DocsURL: "https://example.test/sidecar-fixture/setup",
		},
		Matchers: []matcher{
			{ID: "issue-key", Pattern: `\b(?:CASH|GRES|AVATAXUI)-[1-9][0-9]*\b`, Priority: 100},
			{ID: "build-id", Pattern: `\bBUILD-[0-9a-f]{7}\b`},
		},
	}
}

func resolveResponse(locator string) response {
	if locator == "" {
		return response{Protocol: protocol, Error: &protocolError{Code: "not_found", Message: "no locator"}}
	}
	if strings.HasPrefix(locator, "MISSING-") {
		return response{Protocol: protocol, Error: &protocolError{Code: "not_found", Message: "no such issue"}}
	}
	project, _, _ := strings.Cut(locator, "-")
	return response{
		Protocol: protocol,
		Resource: &document{
			Identity: locator,
			Title:    "Synthetic " + locator + " for host tests",
			Subtitle: "Task",
			Status:   &status{Label: "IN PROGRESS", Tone: "info"},
			Fields: []field{
				{Label: "Project", Value: project},
				{Label: "Assignee", Value: "Fixture User", Kind: "user"},
				{Label: "Priority", Value: "High"},
				{Label: "Created", Value: "2026-08-01T09:00:00Z", Kind: "timestamp"},
				{Label: "Component", Value: "billing", Kind: "not-a-real-kind"},
			},
			Body: &body{
				Format: "markdown",
				Text:   "Deterministic body for `" + locator + "`.\n\n- one\n- two\n",
			},
			SourceURL:       "https://fixture.example.test/browse/" + locator,
			UpdatedAt:       "2026-08-17T17:31:00Z",
			FreshForSeconds: 60,
			// Every resolve carries an unknown field: forward compatibility is a
			// protocol rule, so a host that chokes on one has a bug.
			Extra: map[string]any{"nested": true},
		},
	}
}

// hostileDocument is a well-formed response whose every string is trying to
// escape the card.
func hostileDocument() response {
	osc := "\x1b]8;;https://evil.test\x1b\\click\x1b]8;;\x1b\\"
	return response{
		Protocol: protocol,
		Resource: &document{
			Identity:        "CASH-1" + osc,
			Title:           "title" + osc + "\x07\x1b[31m",
			Subtitle:        strings.Repeat("s", 1000),
			Status:          &status{Label: "state" + osc, Tone: "chartreuse"},
			Fields:          hostileFields(),
			Body:            &body{Format: "asciidoc", Text: "[x](javascript:alert(1))\n<script>y</script>\n" + osc},
			SourceURL:       "javascript:alert(1)",
			UpdatedAt:       "yesterday",
			FreshForSeconds: 1e9,
		},
	}
}

func hostileFields() []field {
	out := make([]field, 0, 60)
	for i := 0; i < 60; i++ {
		out = append(out, field{
			Label: fmt.Sprintf("label-%d-%s", i, strings.Repeat("L", 200)),
			Value: strings.Repeat("V", 2000),
		})
	}
	return out
}

// overLimitDocument is a well-formed document that is merely too big in every
// dimension the host truncates. It must render, not fail.
func overLimitDocument() response {
	fields := make([]field, 0, 40)
	for i := 0; i < 40; i++ {
		fields = append(fields, field{
			Label: fmt.Sprintf("f%d-%s", i, strings.Repeat("L", 120)),
			Value: strings.Repeat("V", 900),
		})
	}
	return response{
		Protocol: protocol,
		Resource: &document{
			Identity: "CASH-1",
			Title:    strings.Repeat("T", 500),
			Subtitle: strings.Repeat("S", 300),
			Status:   &status{Label: strings.Repeat("P", 200), Tone: "warning"},
			Fields:   fields,
			Body:     &body{Format: "text", Text: strings.Repeat("é", 60*1024)},
		},
	}
}

// requestEcho returns the request the host actually sent, so a host test can
// assert the envelope from the outside rather than trusting its own encoder.
func requestEcho(rawRequest []byte) response {
	return response{
		Protocol: protocol,
		Resource: &document{
			Identity: "request-echo",
			Title:    "request",
			Body:     &body{Format: "text", Text: string(rawRequest)},
		},
	}
}

// envReport returns the child's own execution environment as a document, so
// host tests can assert argv, working directory, and the environment allowlist
// from the outside.
func envReport() response {
	wd, _ := os.Getwd()
	return response{
		Protocol: protocol,
		Resource: &document{
			Identity: "env-report",
			Title:    "environment",
			Body: &body{
				Format: "text",
				Text: "argv=" + strings.Join(os.Args, " ") + "\n" +
					"cwd=" + wd + "\n" +
					"env=" + strings.Join(os.Environ(), "\n"),
			},
		},
	}
}

func forkDescendant(pidfile string) {
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, "-mode=descendant", "-sleep=120s")
	// Inheriting stdout is the point: the host's drain cannot finish while the
	// descendant holds the write end, so only killing the process group ends
	// the invocation.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return
	}
	if pidfile != "" {
		_ = os.WriteFile(pidfile, []byte(fmt.Sprint(cmd.Process.Pid)), 0o644)
	}
	// Deliberately no Wait: the parent leaves the descendant running.
}

func emit(resp response) {
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(resp); err != nil {
		os.Exit(1)
	}
}
