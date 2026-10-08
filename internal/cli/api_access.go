package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/uiapi"
)

// The access verbs are the CLI projection of the UI API's access-request and
// device routes. Every rule (limits, code matching, who may approve) lives in
// the server; these verbs only call it over the Local socket and print.

const apiRefusedExit = 4

var apiAccessExitCodes = []ExitCode{
	{Code: 0, Summary: "success"},
	{Code: 1, Summary: "no server running, or it could not be reached"},
	{Code: 2, Summary: "usage error"},
	{Code: apiRefusedExit, Summary: "the server refused; stderr (and with --json, stdout) names the code"},
}

func apiAccessCommands() []*Command {
	help := Flag{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}
	jsonFlag := Flag{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true}
	requests := &Command{
		Name: "requests", Summary: "List browsers waiting for approval", Usage: "sidecar api requests [--json]",
		Long:      "List the browsers that asked the running UI API for access and are waiting for someone to approve them, oldest first. A browser with no credential asks from Sidecar's UI page and shows a six-character code; the list deliberately omits that code, because approving means typing the code the browser shows, which stops a nearby device from racing a request in alongside yours. Each row shows the request id (for `sidecar api deny`), the device name the browser claims for itself (a claim, not proof), the address it connected from, its origin and when it expires. Requests last five minutes and do not survive an API restart; the browser asks again on its own. Behind a proxy every request shows the same address, so with more than one waiting the list says so plainly: only the code on your own screen tells them apart.",
		Flags:     []Flag{jsonFlag, help},
		ExitCodes: apiAccessExitCodes,
		Examples:  []Example{{Command: "sidecar api requests"}, {Command: "sidecar api requests --json"}},
		Agent:     AgentDoc{Invocation: "sidecar api requests --json", Summary: "See browsers waiting for access to this machine's Sidecar UI"},
		Run:       runAPIRequests,
	}
	approve := &Command{
		Name: "approve", Summary: "Let in the browser that shows CODE", Usage: "sidecar api approve CODE [--json]",
		Long:      "Approve the waiting browser whose code is CODE. Type the code exactly as the new browser shows it, such as K7Q-4MX; case, hyphens and spaces do not matter, and O reads as 0 and I or L as 1. Approval registers that browser's own key for its origin, recorded as approved via cli; it hands out no token or link, and the browser signs itself in within a few seconds. Five wrong codes in a minute lock approval for the rest of that minute. When more than one browser was waiting it says so after approving, so you can check the code was the one on your screen. Refusals: access_code_invalid (no waiting browser shows that code), access_request_expired (that browser's request expired or was replaced and it now shows a new code; not counted as a wrong code), too_many_attempts, invalid_request (not a six-character code).",
		Args:      ArgSpec{Min: 1, Max: 1, Description: "CODE: the code the new browser shows"},
		Flags:     []Flag{jsonFlag, help},
		ExitCodes: apiAccessExitCodes,
		Examples:  []Example{{Command: "sidecar api approve K7Q-4MX"}, {Command: "sidecar api approve k7q4mx --json"}},
		Agent:     AgentDoc{Invocation: "sidecar api approve CODE", Summary: "Let in a browser waiting for access, by the code it shows (ask the person for it)"},
		Mutates:   true, Run: runAPIApprove,
	}
	deny := &Command{
		Name: "deny", Summary: "Refuse one waiting browser", Usage: "sidecar api deny REQUEST_ID [--json]",
		Long:      "Refuse the waiting browser with REQUEST_ID, from `sidecar api requests`. The browser is told it was denied and may ask again. Refusal: access_request_not_found (no such request is waiting).",
		Args:      ArgSpec{Min: 1, Max: 1, Description: "REQUEST_ID: from sidecar api requests"},
		Flags:     []Flag{jsonFlag, help},
		ExitCodes: apiAccessExitCodes,
		Examples:  []Example{{Command: "sidecar api deny Xk3vQ0pL9aBcDeFg"}},
		Mutates:   true, Run: runAPIDeny,
	}
	revoke := &Command{
		Name: "revoke", Summary: "Sign out one browser", Usage: "sidecar api devices revoke ID [--json]",
		Long:      "Sign out one browser registration: its tokens stop working, its open terminals and event streams close with 4401, and it must be approved again. ID is the device id from `sidecar api devices`, or a unique prefix of at least 6 characters. Other browsers are unaffected; `sidecar api pair --revoke-sessions` signs out all of them. Refusals: device_not_found, invalid_request (a prefix that is too short or matches more than one device).",
		Args:      ArgSpec{Min: 1, Max: 1, Description: "ID: a device id or unique prefix"},
		Flags:     []Flag{jsonFlag, help},
		ExitCodes: apiAccessExitCodes,
		Examples:  []Example{{Command: "sidecar api devices revoke 3f9a1c2b7d4e"}},
		Mutates:   true, Run: runAPIDevicesRevoke,
	}
	devices := &Command{
		Name: "devices", Summary: "List browsers that can sign in, or revoke one", Usage: "sidecar api devices [--json] | sidecar api devices revoke ID [--json]",
		Long:      "List the browsers registered with the running UI API, most recently used first: device id, the name the browser claimed when it asked, its origin, how it was approved (link for `sidecar api open`, cli, tui, browser:<device> or tailnet:<login>) and when, and when it was last used. Registrations expire 30 days after last use and at most 180 days after creation. `sidecar api devices revoke ID` signs one out.",
		Flags:     []Flag{jsonFlag, help},
		ExitCodes: apiAccessExitCodes,
		Examples:  []Example{{Command: "sidecar api devices"}, {Command: "sidecar api devices --json"}, {Command: "sidecar api devices revoke 3f9a1c2b7d4e"}},
		Agent:     AgentDoc{Invocation: "sidecar api devices --json", Summary: "See which browsers can sign in to this machine's Sidecar UI"},
		Sub:       []*Command{revoke},
		Run:       runAPIDevices,
	}
	return []*Command{requests, approve, deny, devices}
}

// apiAccessFailure reports a failed access call: a named server refusal
// exits 4 with its code; anything else (no server, transport) exits 1.
func apiAccessFailure(env Env, err error, asJSON bool) int {
	var refusal *uiapi.APIError
	if errors.As(err, &refusal) {
		if asJSON {
			_ = writeCLIJSON(env, uiapi.ErrorBody{Error: refusal.ErrorDetail})
		}
		cliErrf(env.Stderr, "%s: %s\n", refusal.Code, refusal.Message)
		return apiRefusedExit
	}
	cliErrln(env.Stderr, err)
	return 1
}

// apiAccessArgs parses flags and the expected positional arguments.
func apiAccessArgs(env Env, cmd *Command, args []string, positional int) (apiFlags, []string, bool) {
	var rest []string
	var flagArgs []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			flagArgs = append(flagArgs, arg)
		} else {
			rest = append(rest, arg)
		}
	}
	flags, err := parseAPIFlags(flagArgs, []string{"--json"}, nil)
	if err == nil && len(rest) != positional {
		if positional == 0 {
			err = fmt.Errorf("unexpected argument %q", rest[0])
		} else {
			err = fmt.Errorf("%s takes %d argument", cmd.Name, positional)
		}
	}
	if err != nil {
		cliErrf(env.Stderr, "%v\n\n%s", err, RenderHelp(cmd))
		return flags, nil, false
	}
	return flags, rest, true
}

func helpRequested(env Env, cmd *Command, args []string) bool {
	for _, arg := range args {
		if isHelp(arg) {
			_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
			return true
		}
	}
	return false
}

func runAPIRequests(env Env, args []string) int {
	cmd := apiSubcommand("requests")
	if helpRequested(env, cmd, args) {
		return 0
	}
	flags, _, ok := apiAccessArgs(env, cmd, args, 0)
	if !ok {
		return 2
	}
	client, code := apiLocalClient(env)
	if client == nil {
		return code
	}
	ctx, cancel := apiContext(env)
	defer cancel()
	list, err := client.ListAccessRequests(ctx)
	if err != nil {
		return apiAccessFailure(env, err, flags.bools["--json"])
	}
	if flags.bools["--json"] {
		return writeCLIJSON(env, list)
	}
	printAccessRequests(env.Stdout, list.Requests, time.Now())
	return 0
}

func printAccessRequests(out io.Writer, requests []uiapi.AccessRequestInfo, now time.Time) {
	if len(requests) == 0 {
		_, _ = fmt.Fprintln(out, "No browsers are waiting for approval.")
		return
	}
	if warning := uiapi.MultipleWaiting(len(requests)); warning != "" {
		_, _ = fmt.Fprintln(out, warning)
	}
	_, _ = fmt.Fprintf(out, "%d browser(s) waiting for approval:\n", len(requests))
	for _, r := range requests {
		_, _ = fmt.Fprintf(out, "  %s  %s  from %s via %s, asked %s ago, expires in %s\n", r.RequestID, deviceName(r.Label), r.Address, r.Origin,
			shortDuration(now.Sub(r.CreatedAt)), shortDuration(r.ExpiresAt.Sub(now)))
	}
	_, _ = fmt.Fprintln(out, "Approve one by typing the code that browser shows: sidecar api approve CODE")
}

// deviceName quotes a browser's claimed name, so it reads as a claim.
func deviceName(label string) string {
	if label == "" {
		return "(unnamed browser)"
	}
	return fmt.Sprintf("%q", label)
}

func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Truncate(time.Second).String()
}

func runAPIApprove(env Env, args []string) int {
	cmd := apiSubcommand("approve")
	if helpRequested(env, cmd, args) {
		return 0
	}
	flags, rest, ok := apiAccessArgs(env, cmd, args, 1)
	if !ok {
		return 2
	}
	client, code := apiLocalClient(env)
	if client == nil {
		return code
	}
	ctx, cancel := apiContext(env)
	defer cancel()
	// Count who was waiting first: with more than one, the person should
	// know the code they typed is all that told them apart.
	waiting := 0
	if list, err := client.ListAccessRequests(ctx); err == nil {
		waiting = len(list.Requests)
	}
	approval, err := client.ApproveAccess(ctx, rest[0], "cli")
	if err != nil {
		return apiAccessFailure(env, err, flags.bools["--json"])
	}
	warning := ""
	if waiting > 1 {
		warning = fmt.Sprintf("%d browsers were waiting; make sure the code you typed is the one on your screen.", waiting)
	}
	if flags.bools["--json"] {
		if warning != "" {
			_, _ = fmt.Fprintln(env.Stderr, warning)
		}
		return writeCLIJSON(env, approval)
	}
	_, _ = fmt.Fprintf(env.Stdout, "Approved %s from %s via %s. It signs itself in within a few seconds.\n", deviceName(approval.Label), approval.Address, approval.Origin)
	if warning != "" {
		_, _ = fmt.Fprintln(env.Stdout, warning)
	}
	return 0
}

func runAPIDeny(env Env, args []string) int {
	cmd := apiSubcommand("deny")
	if helpRequested(env, cmd, args) {
		return 0
	}
	flags, rest, ok := apiAccessArgs(env, cmd, args, 1)
	if !ok {
		return 2
	}
	client, code := apiLocalClient(env)
	if client == nil {
		return code
	}
	ctx, cancel := apiContext(env)
	defer cancel()
	denial, err := client.DenyAccess(ctx, rest[0])
	if err != nil {
		return apiAccessFailure(env, err, flags.bools["--json"])
	}
	if flags.bools["--json"] {
		return writeCLIJSON(env, denial)
	}
	_, _ = fmt.Fprintf(env.Stdout, "Denied request %s.\n", denial.RequestID)
	return 0
}

func runAPIDevices(env Env, args []string) int {
	cmd := apiSubcommand("devices")
	if len(args) > 0 && args[0] == "revoke" {
		return runAPIDevicesRevoke(env, args[1:])
	}
	if helpRequested(env, cmd, args) {
		return 0
	}
	flags, _, ok := apiAccessArgs(env, cmd, args, 0)
	if !ok {
		return 2
	}
	client, code := apiLocalClient(env)
	if client == nil {
		return code
	}
	ctx, cancel := apiContext(env)
	defer cancel()
	list, err := client.ListDevices(ctx)
	if err != nil {
		return apiAccessFailure(env, err, flags.bools["--json"])
	}
	if flags.bools["--json"] {
		return writeCLIJSON(env, list)
	}
	if len(list.Devices) == 0 {
		_, _ = fmt.Fprintln(env.Stdout, "No browsers are registered. A new browser asks for access from Sidecar's UI; approve it with `sidecar api approve CODE`.")
		return 0
	}
	for _, d := range list.Devices {
		_, _ = fmt.Fprintf(env.Stdout, "  %s  %s  %s  approved via %s %s, last used %s\n", shortDeviceID(d.ID), deviceName(d.Label), d.Origin, d.ApprovedVia,
			d.ApprovedAt.Local().Format(time.RFC3339), d.LastUsedAt.Local().Format(time.RFC3339))
	}
	_, _ = fmt.Fprintln(env.Stdout, "Sign one out with: sidecar api devices revoke ID")
	return 0
}

func shortDeviceID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

const minDevicePrefix = 6

// resolveDeviceID turns a full id or unique prefix into a device id.
func resolveDeviceID(devices []uiapi.Device, input string) (string, error) {
	var matches []string
	for _, d := range devices {
		if d.ID == input {
			return d.ID, nil
		}
		if strings.HasPrefix(d.ID, input) {
			matches = append(matches, d.ID)
		}
	}
	switch {
	case len(input) < minDevicePrefix:
		return "", &uiapi.APIError{Status: 400, ErrorDetail: uiapi.ErrorDetail{Code: uiapi.CodeInvalidRequest, Message: fmt.Sprintf("Give at least %d characters of the device id; list them with `sidecar api devices`.", minDevicePrefix)}}
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		return "", &uiapi.APIError{Status: 400, ErrorDetail: uiapi.ErrorDetail{Code: uiapi.CodeInvalidRequest, Message: fmt.Sprintf("%q matches %d devices; give more of the id.", input, len(matches))}}
	}
	return "", &uiapi.APIError{Status: 404, ErrorDetail: uiapi.ErrorDetail{Code: uiapi.CodeDeviceNotFound, Message: "No browser registration has that id; list them with `sidecar api devices`."}}
}

func runAPIDevicesRevoke(env Env, args []string) int {
	cmd := apiSubcommand("devices").FindSubcommand("revoke")
	if helpRequested(env, cmd, args) {
		return 0
	}
	flags, rest, ok := apiAccessArgs(env, cmd, args, 1)
	if !ok {
		return 2
	}
	client, code := apiLocalClient(env)
	if client == nil {
		return code
	}
	ctx, cancel := apiContext(env)
	defer cancel()
	asJSON := flags.bools["--json"]
	list, err := client.ListDevices(ctx)
	if err != nil {
		return apiAccessFailure(env, err, asJSON)
	}
	id, err := resolveDeviceID(list.Devices, rest[0])
	if err != nil {
		return apiAccessFailure(env, err, asJSON)
	}
	label := ""
	for _, d := range list.Devices {
		if d.ID == id {
			label = d.Label
		}
	}
	revocation, err := client.RevokeDevice(ctx, id)
	if err != nil {
		return apiAccessFailure(env, err, asJSON)
	}
	if asJSON {
		return writeCLIJSON(env, revocation)
	}
	_, _ = fmt.Fprintf(env.Stdout, "Signed out %s %s and closed %d terminal(s). It must be approved again to sign in.\n", shortDeviceID(id), deviceName(label), revocation.TerminalsClosed)
	return 0
}
