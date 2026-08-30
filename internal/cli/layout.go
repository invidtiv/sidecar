package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/uirequest"
)

// layoutCommand is the agent-facing layout surface: read the focused pane
// tree (`get`) or compose panes onto it in one all-or-nothing call (`apply`).
// The CLI creates nothing — it sends one uirequest and reports the ack, whose
// versioned items array carries every requested pane's individual verdict.
func layoutCommand() *Command {
	getCmd := &Command{
		Name:    "get",
		Summary: "Read the current pane layout",
		Usage:   "sidecar layout get [--json] [--sessions [ROW]]",
		Long: "Read the pane layout of the surface showing this Sidecar shell: the grid\n" +
			"projection, every pane's kind, targets and tmux session, geometry, and the\n" +
			"caps and floors an apply would be held to.\n\n" +
			"--sessions addresses the global Sessions surface of a running instance\n" +
			"(optional ROW is a durable inventory ID, then a display name). It is\n" +
			"mutually exclusive with --shell and --project.\n\n" +
			"A layout that escapes the grid vocabulary reports \"grid\": null plus the raw\n" +
			"tree; it is still valid. Human output is a small ASCII sketch plus a table;\n" +
			"--json passes the payload through unchanged, which is the contract.\n\n" +
			"Unlike open, a layout request never queues: when this shell is not on\n" +
			"screen the request declines instead (exit 4), because a stale answer is\n" +
			"worse than a refusal.",
		Flags: []Flag{
			{Name: "--shell", Arg: "NAME", Summary: "Target a registered shell by display name or tmux name"},
			{Name: "--project", Arg: "NAME", Summary: "Target a project's Workspaces surface (slug, basename, or path)"},
			{Name: "--sessions", Arg: "[ROW]", Summary: "Target the global Sessions surface (optional row by ID or display name)"},
			{Name: "--wait", Arg: "DURATION", Summary: "Time to wait for instances to acknowledge (default 1200ms)"},
			{Name: "--json", Summary: "Write the layout payload itself to stdout", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		Args: ArgSpec{Min: 0, Max: 0},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "answered"},
			{Code: 1, Summary: "state failure"},
			{Code: 2, Summary: "usage error"},
			{Code: 3, Summary: "no running instance"},
			{Code: 4, Summary: "declined: the origin shell is not on screen"},
			{Code: 5, Summary: "an unknown --project or --shell"},
		},
		Examples: []Example{
			{Command: "sidecar layout get"},
			{Command: "sidecar layout get --json", Description: "the machine contract: read before you write"},
			{Command: "sidecar layout get --sessions --json", Description: "the selected row on the global Sessions surface"},
		},
		Agent: AgentDoc{
			Invocation: "sidecar layout get --json",
			Summary:    "Read the user's current pane layout before composing onto it",
		},
		Run: runLayoutGet,
	}

	return &Command{
		Name:    "layout",
		Summary: "Read and compose the pane layout agents work beside",
		Usage:   "sidecar layout <command>",
		Long: "Read the current pane layout (`layout get`), open several panes at once in\n" +
			"one atomic call (`layout apply`), or reposition one pane that is already\n" +
			"open (`layout move`). All three act on the surface showing this Sidecar\n" +
			"shell — or, with --sessions, the global Sessions surface — and never queue:\n" +
			"a request whose destination is off screen declines with the reason.",
		Sub: []*Command{applyLayoutSubcommand(), getCmd, moveLayoutSubcommand()},
		Run: func(env Env, args []string) int {
			layoutRoot := RootCommand().FindSubcommand("layout")
			if len(args) == 0 || isHelp(args[0]) {
				_, _ = fmt.Fprint(env.Stdout, RenderHelp(layoutRoot))
				return 0
			}
			sub := layoutRoot.FindSubcommand(args[0])
			if sub != nil && sub.Run != nil {
				return sub.Run(env, args[1:])
			}
			cliErrf(env.Stderr, "unknown layout command %q\n\n%s", args[0], RenderHelp(layoutRoot))
			return 2
		},
	}
}

// applyLayoutSubcommand is `sidecar layout apply`: repeatable --pane
// descriptors or one whole-layout --spec, one atomic call, per-pane verdicts
// in the ack.
func applyLayoutSubcommand() *Command {
	return &Command{
		Name:    "apply",
		Summary: "Open several panes in one all-or-nothing call",
		Usage:   "sidecar layout apply (--spec '<json>' | --pane '<json>' [--pane '<json>' ...]) [--sessions [ROW]]",
		Long: "Compose panes onto the surface showing this Sidecar shell.\n\n" +
			"--spec is a FULL layout, given as columns of stacked panes; it replaces\n" +
			"what is on screen:\n\n" +
			"  {\"columns\":[\n" +
			"    {\"panes\":[{\"kind\":\"primary\"}]},\n" +
			"    {\"panes\":[{\"kind\":\"file\",\"targets\":[\"path:line\",\"path2\",...]},\n" +
			"              {\"kind\":\"issue\",\"targets\":[\"td-xxxxxx\",...]}]},\n" +
			"    {\"panes\":[{\"kind\":\"shell\",\"run\":\"...\",\"name\":\"...\"}]}\n" +
			"  ]}\n\n" +
			"A spec needs exactly one \"primary\" pane and must CARRY every live leaf\n" +
			"already on screen exactly as `layout get` prints them: the primary as\n" +
			"{\"kind\":\"primary\"}, a split terminal as {\"kind\":\"shell\",\"session\":\n" +
			"\"<tmux-session>\"}. A spec omitting a live terminal declines naming the\n" +
			"session — apply never destroys one. Passive panes not named are closed\n" +
			"freely (their content re-opens). Pass `-` to read the spec from stdin.\n\n" +
			"--pane opens panes ADDITIVELY without closing anything. Each value is one\n" +
			"descriptor as its JSON object verbatim:\n\n" +
			"  {\"kind\":\"file\",\"targets\":[\"path:line\",...],\"at\":\"2.1\"}\n" +
			"  {\"kind\":\"issue\",\"targets\":[\"td-xxxxxx\"]}\n" +
			"  {\"kind\":\"note\",\"targets\":[\"nt-xxxxxx\"]}\n" +
			"  {\"kind\":\"diff\",\"targets\":[\"spec\"]}   no targets = the working tree\n" +
			"  {\"kind\":\"resource\",\"provider\":\"<instance>\",\"targets\":[\"LOCATOR\"]}\n" +
			"  {\"kind\":\"shell\",\"run\":\"...\",\"type\":\"...\",\"name\":\"...\"}\n\n" +
			"The first target opens a pane; the rest join it as tabs of the same kind.\n" +
			"\"at\" is an optional grid cell col.row (1-based) and is a requirement, not a\n" +
			"preference: an unreachable cell declines rather than landing elsewhere.\n" +
			"File paths are workspace-relative; diffs re-resolve host-side; providers are\n" +
			"validated against the live matcher snapshot.\n\n" +
			"Either form is validated and fit-tested before anything changes: it all\n" +
			"happens, or nothing changes and the decline names the first violation.\n\n" +
			"The ack's items array lists EVERY requested pane with verdict opened,\n" +
			"retargeted, carried (a live leaf the spec kept rather than created), or\n" +
			"declined, plus its landed cell — so one round trip shows everything wrong\n" +
			"with a refused spec. Like get, apply never queues.",
		Flags: []Flag{
			{Name: "--spec", Arg: "JSON", Summary: "A full layout replacing the screen: columns of stacked panes (- reads stdin)"},
			{Name: "--pane", Arg: "JSON", Summary: "One pane descriptor to add (repeatable); see above for the object shape"},
			{Name: "--shell", Arg: "NAME", Summary: "Target a registered shell by display name or tmux name"},
			{Name: "--project", Arg: "NAME", Summary: "Target a project's Workspaces surface (slug, basename, or path)"},
			{Name: "--sessions", Arg: "[ROW]", Summary: "Target the global Sessions surface (optional row by ID or display name)"},
			{Name: "--wait", Arg: "DURATION", Summary: "Time to wait for instances to acknowledge (default 1200ms)"},
			{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		Args: ArgSpec{Min: 0, Max: 0},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "applied (or every pane retargeted an existing one)"},
			{Code: 1, Summary: "state failure"},
			{Code: 2, Summary: "usage or validation error"},
			{Code: 3, Summary: "no running instance"},
			{Code: 4, Summary: "declined host-side; the reason names the first violation"},
			{Code: 5, Summary: "an unknown --project or --shell"},
		},
		Examples: []Example{
			{Command: `sidecar layout get --json`, Description: "read before you write"},
			{Command: `sidecar layout apply --spec '{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"file","targets":["README.md"]},{"kind":"issue","targets":["td-756c34"]}]}]}'`, Description: "a full layout: primary left, file over issue right"},
			{Command: `sidecar layout apply --spec - <layout.json`, Description: "apply a spec from stdin"},
			{Command: `sidecar layout apply --pane '{"kind":"file","targets":["internal/palette/list.go:112","internal/palette/state.go"]}' --pane '{"kind":"shell","run":"make dev","name":"dev server"}'`, Description: "add two panes, auto-placed"},
			{Command: `sidecar layout apply --pane '{"kind":"file","targets":["README.md"],"at":"2.1"}' --json`, Description: "explicit cell, structured result"},
		},
		Agent: AgentDoc{
			Invocation: `sidecar layout apply --spec '{"columns":[{"panes":[...]}]}' | --pane '{"kind":"file|issue|note|diff|resource","targets":[...],"at":"col.row"}' [--pane ...] | --pane '{"kind":"shell","run":"...","name":"..."}'`,
			Summary:    "Apply a full layout from a spec, or add panes atomically; learn exactly why nothing changed",
		},
		Mutates: true,
		Run:     runLayoutApply,
	}
}

// moveLayoutSubcommand is `sidecar layout move`: reposition one pane that is
// already open. It is a verb over the planner the reposition modal and the M
// key already call, not a second way to rearrange a layout — `layout get` plus
// `layout apply --spec` could always do this, at the cost of reconstructing
// every pane on screen to move one of them.
func moveLayoutSubcommand() *Command {
	return &Command{
		Name:    "move",
		Summary: "Reposition one open pane",
		Usage:   "sidecar layout move (CELL | --focused) --to (CELL | COLUMN | left|right|up|down) [--sessions [ROW]]",
		Long: "Move a pane that is already open to another place in the grid. The pane is\n" +
			"pulled out and grafted back at the destination: its content, tabs, scroll\n" +
			"position and any live terminal travel with it, and so does the share of the\n" +
			"box you dragged it to.\n\n" +
			"Name the pane to move by its grid cell (`2.1`, column.row, 1-based) or with\n" +
			"--focused. Addresses are read against the layout AS IT STANDS — run\n" +
			"`layout get` and use the cells it prints; you never compensate for the\n" +
			"source column collapsing.\n\n" +
			"--to takes three forms:\n\n" +
			"  1.2      a cell in the current grid. An occupied cell is an insert:\n" +
			"           the pane lands there and the occupant moves down.\n" +
			"  3        a column number. The pane lands at the BOTTOM of that column,\n" +
			"           and a number one past the last column opens a new one.\n" +
			"  left     the direction rule the modal's h/j/k/l use. up and down step\n" +
			"  right    one row within the column; left and right append at the bottom\n" +
			"  up       of the column beside this one, and open a new outer column when\n" +
			"  down     there is none — including a new leftmost column, which no cell\n" +
			"           address can name.\n\n" +
			"It all happens or nothing does. Caps (a 4x4 grid), per-kind floors, and a\n" +
			"window too small for the result decline with that reason and leave the\n" +
			"layout untouched. A move with nothing to do — the pane is already there, or\n" +
			"a direction with no room beyond it — is a SUCCESS reported as \"unchanged\",\n" +
			"never as moved and never as a refusal.\n\n" +
			"With --sessions this changes the pane tree of the Sessions viewer on THIS\n" +
			"machine, including for a row whose workspace lives on another one: no\n" +
			"layout mutation is sent to the remote host. The acknowledgement names the\n" +
			"surface it changed. Like get and apply, move never queues.",
		Flags: []Flag{
			{Name: "--focused", Summary: "Move the surface's focused pane instead of naming a cell", Bool: true},
			{Name: "--to", Arg: "DEST", Summary: "Destination: a cell (1.2), a column (3), or left|right|up|down"},
			{Name: "--shell", Arg: "NAME", Summary: "Target a registered shell by display name or tmux name"},
			{Name: "--project", Arg: "NAME", Summary: "Target a project's Workspaces surface (slug, basename, or path)"},
			{Name: "--sessions", Arg: "[ROW]", Summary: "Target the global Sessions surface (optional row by ID or display name)"},
			{Name: "--wait", Arg: "DURATION", Summary: "Time to wait for instances to acknowledge (default 1200ms)"},
			{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true},
			{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true},
		},
		Args: ArgSpec{Min: 0, Max: 1},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "moved, or already in the requested place"},
			{Code: 1, Summary: "state failure"},
			{Code: 2, Summary: "usage error"},
			{Code: 3, Summary: "no running instance"},
			{Code: 4, Summary: "declined host-side; the reason names the refusal"},
			{Code: 5, Summary: "an unknown --project or --shell"},
		},
		Examples: []Example{
			{Command: "sidecar layout get --json", Description: "read the cells before you move one"},
			{Command: "sidecar layout move 2.1 --to 1.2", Description: "put the pane at 2.1 below the one at 1.1"},
			{Command: "sidecar layout move --focused --to right", Description: "the direction rule the modal's l uses"},
			{Command: "sidecar layout move 2.1 --to 3", Description: "append to column 3, opening it one past the end"},
			{Command: "sidecar layout move 1.1 --to left --json", Description: "open a new leftmost column, structured result"},
			{Command: "sidecar layout move --focused --to up --sessions", Description: "the global Sessions viewer's own layout"},
		},
		Agent: AgentDoc{
			Invocation: "sidecar layout move (2.1 | --focused) --to (1.2 | 3 | left|right|up|down)",
			Summary:    "Reposition one open pane without rebuilding the layout around it",
		},
		Mutates: true,
		Run:     runLayoutMove,
	}
}

// layoutPaneFlag validates one --pane descriptor CLI-side: JSON shape, a known
// kind, a well-formed cell, and the fields each kind actually takes. Semantic
// resolution (paths, diffs, provider matchers) stays host-side where the
// workspace root and live matchers are.
func layoutPaneFlag(value string) (uirequest.LayoutPane, int, string) {
	var pane uirequest.LayoutPane
	if err := json.Unmarshal([]byte(value), &pane); err != nil {
		return pane, 2, fmt.Sprintf("--pane is not valid JSON: %v", err)
	}
	kind, ok := panelayout.KindByName(strings.TrimSpace(pane.Kind))
	if !ok {
		return pane, 2, fmt.Sprintf("--pane kind %q is not one of: file, issue, note, diff, resource, shell", pane.Kind)
	}
	if kind == panelayout.Primary {
		return pane, 2, "the primary pane is the host's own terminal and cannot be opened"
	}
	if pane.At != "" {
		if _, ok := panelayout.ParseCell(pane.At); !ok {
			return pane, 2, fmt.Sprintf("--pane \"at\" %q is not a grid cell like 2.1", pane.At)
		}
	}
	if kind == panelayout.Shell {
		if len(pane.Targets) > 0 {
			return pane, 2, "a shell pane takes run/type/name, not targets"
		}
		if strings.TrimSpace(pane.Session) != "" {
			// Carrying a live terminal by session is --spec's grammar: a batch
			// closes nothing, so there is no leaf to carry and the field would
			// be silently ignored. Say so rather than opening a second shell
			// the caller did not ask for.
			return pane, 2, "\"session\" carries a live terminal into a full --spec layout; --pane only adds, so drop it (run/type/name open a new split)"
		}
		return pane, 0, ""
	}
	if kind == panelayout.Resource && strings.TrimSpace(pane.Provider) == "" {
		return pane, 2, "a resource pane needs its configured \"provider\" instance"
	}
	if len(pane.Targets) == 0 && kind != panelayout.Diff {
		return pane, 2, fmt.Sprintf("a %s pane needs at least one target", kind.Name())
	}
	return pane, 0, ""
}
