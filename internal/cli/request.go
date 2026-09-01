package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/uirequest"
)

func requestCommand() *Command {
	jsonFlag := Flag{Name: "--json", Summary: "Write the structured result object to stdout (required for the machine contract)", Bool: true}
	helpFlag := Flag{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}

	ackCmd := &Command{
		Name:    "ack",
		Summary: "Write one acknowledgement into a host request's *.acks directory",
		Usage:   "sidecar request ack --id ID --action open|layout --status STATUS [--reason TEXT] [--surface TEXT] [--pane N] --json",
		Long: "Write an acknowledgement for a UI request file this machine already holds.\n\n" +
			"This is the mutation seam a viewing Sidecar uses to ack a relayed open or layout\n" +
			"request into the host *.acks directory. It is not a public targeting flag and not\n" +
			"a serve write: serve still does not write acks or apply requests.\n\n" +
			"--json writes the machine contract.",
		Flags: []Flag{
			{Name: "--id", Arg: "ID", Summary: "Request id to acknowledge"},
			{Name: "--action", Arg: "ACTION", Summary: "Request action (open or layout)"},
			{Name: "--status", Arg: "STATUS", Summary: "Ack status (opened, declined, retargeted, queued, moved, unchanged)"},
			{Name: "--reason", Arg: "TEXT", Summary: "Decline or no-op reason"},
			{Name: "--surface", Arg: "TEXT", Summary: "Surface that handled the request"},
			{Name: "--pane", Arg: "N", Summary: "Pane id that received the open, when any"},
			jsonFlag,
			helpFlag,
		},
		Args: ArgSpec{Min: 0, Max: 0},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "acknowledged"},
			{Code: 1, Summary: "state failure"},
			{Code: 2, Summary: "usage error"},
		},
		Examples: []Example{
			{Command: "sidecar request ack --id req-1 --action open --status opened --json"},
		},
		Mutates: true,
		Run:     runRequestAck,
	}

	return &Command{
		Name:    "request",
		Summary: "Host-side UI request bus verbs a viewing Sidecar invokes",
		Usage:   "sidecar request <command>",
		Long: "Acknowledge UI requests into this machine's request bus.\n\n" +
			"This is an internal transport endpoint, not a public open-on-host surface.",
		Sub: []*Command{ackCmd},
		Run: runRequestRoot,
	}
}

func runRequestRoot(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("request")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	sub := cmd.FindSubcommand(args[0])
	if sub != nil && sub.Run != nil {
		return sub.Run(env, args[1:])
	}
	cliErrf(env.Stderr, "unknown request command %q\n\n%s", args[0], RenderHelp(cmd))
	return 2
}

func runRequestAck(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("request").FindSubcommand("ack")
	help := RenderHelp(cmd)
	var (
		id, action, status, reason, surface string
		pane                                int
		jsonOutput                          bool
		paneSet                             bool
	)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			jsonOutput = true
		case arg == "--id" || strings.HasPrefix(arg, "--id="):
			val, next, ok := takeFlagArg(arg, args, i, "--id")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--id requires a request id\n\n%s", help)
				return 2
			}
			id = val
			i = next
		case arg == "--action" || strings.HasPrefix(arg, "--action="):
			val, next, ok := takeFlagArg(arg, args, i, "--action")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--action requires an action\n\n%s", help)
				return 2
			}
			action = val
			i = next
		case arg == "--status" || strings.HasPrefix(arg, "--status="):
			val, next, ok := takeFlagArg(arg, args, i, "--status")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--status requires a status\n\n%s", help)
				return 2
			}
			status = val
			i = next
		case arg == "--reason" || strings.HasPrefix(arg, "--reason="):
			val, next, ok := takeFlagArg(arg, args, i, "--reason")
			if !ok {
				cliErrf(env.Stderr, "--reason requires a value\n\n%s", help)
				return 2
			}
			reason = val
			i = next
		case arg == "--surface" || strings.HasPrefix(arg, "--surface="):
			val, next, ok := takeFlagArg(arg, args, i, "--surface")
			if !ok {
				cliErrf(env.Stderr, "--surface requires a value\n\n%s", help)
				return 2
			}
			surface = val
			i = next
		case arg == "--pane" || strings.HasPrefix(arg, "--pane="):
			val, next, ok := takeFlagArg(arg, args, i, "--pane")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--pane requires a pane id\n\n%s", help)
				return 2
			}
			n, err := strconv.Atoi(val)
			if err != nil {
				cliErrf(env.Stderr, "invalid pane id %q\n\n%s", val, help)
				return 2
			}
			pane = n
			paneSet = true
			i = next
		case strings.HasPrefix(arg, "-"):
			cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
			return 2
		default:
			cliErrf(env.Stderr, "request ack takes no positional arguments\n\n%s", help)
			return 2
		}
	}
	if id == "" {
		cliErrf(env.Stderr, "--id is required\n\n%s", help)
		return 2
	}
	if action == "" {
		cliErrf(env.Stderr, "--action is required\n\n%s", help)
		return 2
	}
	if status == "" {
		cliErrf(env.Stderr, "--status is required\n\n%s", help)
		return 2
	}
	if !jsonOutput {
		cliErrf(env.Stderr, "--json is required\n\n%s", help)
		return 2
	}

	ack := uirequest.Ack{
		Instance: uirequest.InstanceID("ack"),
		Host:     uirequest.HostName(),
		PID:      os.Getpid(),
		Status:   uirequest.Status(status),
		Reason:   reason,
		Surface:  surface,
		At:       time.Now().UTC(),
	}
	if paneSet {
		ack.Pane = pane
	}
	if err := uirequest.WriteAck(env.StateDir, id, uirequest.Action(action), ack); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	result := uirequest.AckResult{ID: id, Action: uirequest.Action(action), Status: ack.Status, Reason: reason, Surface: surface, Pane: ack.Pane}
	if err := json.NewEncoder(env.Stdout).Encode(result); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}
