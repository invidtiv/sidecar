package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/contentservice"
)

// contentCommand is the read-only content contract a viewing Sidecar invokes
// on a host. It is an internal transport endpoint, not a general file browser
// and not a public `sidecar open --host`.
func contentCommand() *Command {
	jsonFlag := Flag{Name: "--json", Summary: "Write the structured result object to stdout (required for the machine contract)", Bool: true}
	helpFlag := Flag{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}

	describeCmd := &Command{
		Name:    "describe",
		Summary: "Describe this host's terminal resource providers",
		Usage:   "sidecar content describe [--if-revision REV] [--json]",
		Long: "Describe configured terminal resource providers on this machine and return validated ordered descriptors.\n\n" +
			"The fingerprint is a deterministic hash of that wire content, never a process-local snapshot generation.\n" +
			"--if-revision returns a small notModified object when the descriptors are unchanged.\n\n" +
			"--json writes the machine contract.",
		Flags: []Flag{
			{Name: "--if-revision", Arg: "REV", Summary: "Skip the descriptors when they still have this fingerprint"},
			jsonFlag,
			helpFlag,
		},
		Args: ArgSpec{Min: 0, Max: 0},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "described, or notModified"},
			{Code: 1, Summary: "internal or load failure"},
			{Code: 2, Summary: "usage error"},
		},
		Examples: []Example{
			{Command: "sidecar content describe --json"},
			{Command: "sidecar content describe --if-revision v1:abc --json"},
		},
		Run: runContentDescribe,
	}

	resolveCmd := &Command{
		Name:    "resolve",
		Summary: "Resolve a file, issue, note, diff, or resource target to identity and metadata",
		Usage:   "sidecar content resolve --workspace ID --kind file|issue|note|diff|resource --target VALUE [--provider ID --matcher ID] [--json]",
		Long: "Resolve a file, issue, note, git spec, or resource locator against a durable workspace identity on this machine.\n\n" +
			"This is the read-only content contract a viewing Sidecar invokes on a host, not a general file browser.\n" +
			"The workspace id is re-resolved to its authoritative root on every request; the target is a hint, never authority.\n" +
			"Relative file paths cannot escape that root. Explicit absolute and ~/ targets keep local Sidecar's rule: a regular readable file outside the project is allowed.\n" +
			"Issue and note targets are identity only: the id is normalized without consulting td.\n" +
			"Diff targets are git specs: wt, a commit, or A..B / A...B. The host rev-parses commit and range specs.\n" +
			"Resource targets need --provider and --matcher; resolve is identity only and does not invoke the provider.\n\n" +
			"--json writes the machine contract.",
		Flags: []Flag{
			{Name: "--workspace", Arg: "ID", Summary: "Unscoped durable workspace id (projectKey:shell:name or projectKey:worktree:path)"},
			{Name: "--kind", Arg: "KIND", Summary: "Content kind (file, issue, note, diff, or resource)"},
			{Name: "--target", Arg: "VALUE", Summary: "File path, issue/note id, git spec, or resource locator as the viewer saw it"},
			{Name: "--provider", Arg: "ID", Summary: "Resource provider instance id"},
			{Name: "--matcher", Arg: "ID", Summary: "Resource matcher id"},
			jsonFlag,
			helpFlag,
		},
		Args: ArgSpec{Min: 0, Max: 0},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "resolved"},
			{Code: 1, Summary: "internal or load failure"},
			{Code: 2, Summary: "usage error or unknown kind"},
			{Code: 5, Summary: "value rejected: unknown workspace, containment, or not found"},
		},
		Examples: []Example{
			{Command: "sidecar content resolve --workspace /home/me/api:shell:sidecar-sh-1 --kind file --target README.md --json"},
		},
		Run: runContentResolve,
	}

	readCmd := &Command{
		Name:    "read",
		Summary: "Read bounded file, issue, note, diff, or resource content",
		Usage:   "sidecar content read --workspace ID --kind file|issue|note|diff|resource --operation OP --target VALUE [--provider ID --matcher ID] [--path PATH] [--parent HASH] [--offset N] [--limit N] [--if-revision REV] [--refresh] [--json]",
		Long: "Read a file document, issue card, note, git diff operation, or resource document from a durable workspace identity on this machine.\n\n" +
			"This is the read-only content contract a viewing Sidecar invokes on a host, not a general file browser.\n" +
			"--if-revision returns a small notModified object when the content is unchanged, so a refresh is one round trip.\n" +
			"The encoded JSON is capped under 768KiB; a payload that would blow that cap is truncated or returned as a structured oversize object rather than invalid JSON.\n" +
			"Issue fallback candidates come from this host's configured projects.\n" +
			"Diff operations are enumerated: working-tree, working-tree-file, commit, range, commit-file, full-file.\n" +
			"Resource reads return the provider wire document; the viewer sanitizes again. --refresh bypasses the host manager cache.\n\n" +
			"--json writes the machine contract.",
		Flags: []Flag{
			{Name: "--workspace", Arg: "ID", Summary: "Unscoped durable workspace id (projectKey:shell:name or projectKey:worktree:path)"},
			{Name: "--kind", Arg: "KIND", Summary: "Content kind (file, issue, note, diff, or resource)"},
			{Name: "--operation", Arg: "OP", Summary: "Read operation (document, card, note, resource, working-tree, working-tree-file, commit, range, commit-file, or full-file)"},
			{Name: "--target", Arg: "VALUE", Summary: "File path, issue/note id, git spec, or resource locator as resolved or as the viewer saw it"},
			{Name: "--provider", Arg: "ID", Summary: "Resource provider instance id"},
			{Name: "--matcher", Arg: "ID", Summary: "Resource matcher id"},
			{Name: "--path", Arg: "PATH", Summary: "Diff file path for working-tree-file, commit-file, and full-file"},
			{Name: "--parent", Arg: "HASH", Summary: "Merge parent hash for commit-file and full-file"},
			{Name: "--offset", Arg: "N", Summary: "Full-file page offset in lines"},
			{Name: "--limit", Arg: "N", Summary: "Full-file page size in lines"},
			{Name: "--if-revision", Arg: "REV", Summary: "Skip the body when the content still has this revision"},
			{Name: "--refresh", Summary: "Bypass the host resource cache", Bool: true},
			jsonFlag,
			helpFlag,
		},
		Args: ArgSpec{Min: 0, Max: 0},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "read, or notModified"},
			{Code: 1, Summary: "internal or load failure"},
			{Code: 2, Summary: "usage error or unknown kind"},
			{Code: 5, Summary: "value rejected: unknown workspace, containment, or not found"},
		},
		Examples: []Example{
			{Command: "sidecar content read --workspace /home/me/api:shell:sidecar-sh-1 --kind file --operation document --target README.md --json"},
			{Command: "sidecar content read --workspace /home/me/api:shell:sidecar-sh-1 --kind file --operation document --target README.md --if-revision v1:abc --json"},
		},
		Run: runContentRead,
	}

	return &Command{
		Name:    "content",
		Summary: "Read-only content contract a viewing Sidecar invokes on a host",
		Usage:   "sidecar content <command>",
		Long: "Resolve and read files, issues, notes, diffs, and resources for a viewing Sidecar over the existing host request seam.\n\n" +
			"This is an internal transport endpoint, not a general file browser and not a public open-on-host surface.\n" +
			"Every verb is non-interactive, read-only, and strictly enumerated.",
		Sub: []*Command{describeCmd, readCmd, resolveCmd},
		Run: runContentRoot,
	}
}

func runContentRoot(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("content")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	sub := cmd.FindSubcommand(args[0])
	if sub != nil && sub.Run != nil {
		return sub.Run(env, args[1:])
	}
	cliErrf(env.Stderr, "unknown content command %q\n\n%s", args[0], RenderHelp(cmd))
	return 2
}

type contentFlags struct {
	workspace  string
	kind       string
	target     string
	operation  string
	ifRevision string
	path       string
	parent     string
	provider   string
	matcher    string
	offset     int
	limit      int
	json       bool
	refresh    bool
}

func parseContentFlags(env Env, help string, args []string, allowRead bool) (contentFlags, int, bool) {
	var flags contentFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprint(env.Stdout, help)
			return flags, 0, false
		case arg == "--json":
			flags.json = true
		case arg == "--workspace" || strings.HasPrefix(arg, "--workspace="):
			val, next, ok := takeFlagArg(arg, args, i, "--workspace")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--workspace requires an id\n\n%s", help)
				return flags, 2, false
			}
			flags.workspace = val
			i = next
		case arg == "--kind" || strings.HasPrefix(arg, "--kind="):
			val, next, ok := takeFlagArg(arg, args, i, "--kind")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--kind requires a kind\n\n%s", help)
				return flags, 2, false
			}
			flags.kind = val
			i = next
		case arg == "--target" || strings.HasPrefix(arg, "--target="):
			val, next, ok := takeFlagArg(arg, args, i, "--target")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--target requires a value\n\n%s", help)
				return flags, 2, false
			}
			flags.target = val
			i = next
		case allowRead && (arg == "--operation" || strings.HasPrefix(arg, "--operation=")):
			val, next, ok := takeFlagArg(arg, args, i, "--operation")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--operation requires an operation\n\n%s", help)
				return flags, 2, false
			}
			flags.operation = val
			i = next
		case allowRead && (arg == "--if-revision" || strings.HasPrefix(arg, "--if-revision=")):
			val, next, ok := takeFlagArg(arg, args, i, "--if-revision")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--if-revision requires a revision\n\n%s", help)
				return flags, 2, false
			}
			flags.ifRevision = val
			i = next
		case arg == "--provider" || strings.HasPrefix(arg, "--provider="):
			val, next, ok := takeFlagArg(arg, args, i, "--provider")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--provider requires an id\n\n%s", help)
				return flags, 2, false
			}
			flags.provider = val
			i = next
		case arg == "--matcher" || strings.HasPrefix(arg, "--matcher="):
			val, next, ok := takeFlagArg(arg, args, i, "--matcher")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--matcher requires an id\n\n%s", help)
				return flags, 2, false
			}
			flags.matcher = val
			i = next
		case allowRead && arg == "--refresh":
			flags.refresh = true
		case allowRead && (arg == "--path" || strings.HasPrefix(arg, "--path=")):
			val, next, ok := takeFlagArg(arg, args, i, "--path")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--path requires a path\n\n%s", help)
				return flags, 2, false
			}
			flags.path = val
			i = next
		case allowRead && (arg == "--parent" || strings.HasPrefix(arg, "--parent=")):
			val, next, ok := takeFlagArg(arg, args, i, "--parent")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--parent requires a hash\n\n%s", help)
				return flags, 2, false
			}
			flags.parent = val
			i = next
		case allowRead && (arg == "--offset" || strings.HasPrefix(arg, "--offset=")):
			val, next, ok := takeFlagArg(arg, args, i, "--offset")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--offset requires a number\n\n%s", help)
				return flags, 2, false
			}
			n, err := strconv.Atoi(val)
			if err != nil {
				cliErrf(env.Stderr, "--offset must be an integer\n\n%s", help)
				return flags, 2, false
			}
			flags.offset = n
			i = next
		case allowRead && (arg == "--limit" || strings.HasPrefix(arg, "--limit=")):
			val, next, ok := takeFlagArg(arg, args, i, "--limit")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--limit requires a number\n\n%s", help)
				return flags, 2, false
			}
			n, err := strconv.Atoi(val)
			if err != nil {
				cliErrf(env.Stderr, "--limit must be an integer\n\n%s", help)
				return flags, 2, false
			}
			flags.limit = n
			i = next
		case strings.HasPrefix(arg, "-"):
			cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
			return flags, 2, false
		default:
			cliErrf(env.Stderr, "content takes no positional arguments\n\n%s", help)
			return flags, 2, false
		}
	}
	if flags.workspace == "" {
		cliErrf(env.Stderr, "--workspace is required\n\n%s", help)
		return flags, 2, false
	}
	if flags.kind == "" {
		cliErrf(env.Stderr, "--kind is required\n\n%s", help)
		return flags, 2, false
	}
	if flags.target == "" {
		cliErrf(env.Stderr, "--target is required\n\n%s", help)
		return flags, 2, false
	}
	if flags.kind == contentservice.KindResource {
		if flags.provider == "" {
			cliErrf(env.Stderr, "--provider is required for kind resource\n\n%s", help)
			return flags, 2, false
		}
		if flags.matcher == "" {
			cliErrf(env.Stderr, "--matcher is required for kind resource\n\n%s", help)
			return flags, 2, false
		}
	}
	return flags, 0, true
}

func parseDescribeFlags(env Env, help string, args []string) (contentFlags, int, bool) {
	var flags contentFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprint(env.Stdout, help)
			return flags, 0, false
		case arg == "--json":
			flags.json = true
		case arg == "--if-revision" || strings.HasPrefix(arg, "--if-revision="):
			val, next, ok := takeFlagArg(arg, args, i, "--if-revision")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--if-revision requires a revision\n\n%s", help)
				return flags, 2, false
			}
			flags.ifRevision = val
			i = next
		case strings.HasPrefix(arg, "-"):
			cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
			return flags, 2, false
		default:
			cliErrf(env.Stderr, "content describe takes no positional arguments\n\n%s", help)
			return flags, 2, false
		}
	}
	return flags, 0, true
}

func contentCtx(env Env) context.Context {
	if env.Ctx != nil {
		return env.Ctx
	}
	return context.Background()
}

func writeContentJSON(env Env, raw []byte) int {
	if _, err := env.Stdout.Write(raw); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}

func contentExit(env Env, err error) int {
	if err == nil {
		return 0
	}
	cliErrln(env.Stderr, err)
	var coded *contentservice.Error
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return 1
}

func runContentDescribe(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("content").FindSubcommand("describe")
	help := RenderHelp(cmd)
	flags, code, ok := parseDescribeFlags(env, help, args)
	if !ok {
		return code
	}
	result, err := contentservice.Default().Describe(contentCtx(env), flags.ifRevision)
	if err != nil {
		return contentExit(env, err)
	}
	if flags.json {
		raw, err := contentservice.EncodeDescribeResult(result)
		if err != nil {
			return contentExit(env, err)
		}
		return writeContentJSON(env, raw)
	}
	if result.NotModified {
		_, _ = fmt.Fprintf(env.Stdout, "not modified %s\n", result.Fingerprint)
		return 0
	}
	_, _ = fmt.Fprintf(env.Stdout, "describe %s %d providers\n", result.Fingerprint, len(result.Descriptors))
	return 0
}

func runContentResolve(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("content").FindSubcommand("resolve")
	help := RenderHelp(cmd)
	flags, code, ok := parseContentFlags(env, help, args, false)
	if !ok {
		return code
	}
	result, err := contentservice.Default().ResolveParams(contentCtx(env), contentservice.ResolveParams{
		WorkspaceID: flags.workspace, Kind: flags.kind, Target: flags.target,
		Provider: flags.provider, Matcher: flags.matcher,
	})
	if err != nil {
		return contentExit(env, err)
	}
	if flags.json {
		raw, err := contentservice.EncodeResolveResult(result)
		if err != nil {
			return contentExit(env, err)
		}
		return writeContentJSON(env, raw)
	}
	_, _ = fmt.Fprintf(env.Stdout, "%s %s\n", result.Kind, result.Display)
	return 0
}

func runContentRead(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("content").FindSubcommand("read")
	help := RenderHelp(cmd)
	flags, code, ok := parseContentFlags(env, help, args, true)
	if !ok {
		return code
	}
	result, err := contentservice.Default().ReadParams(contentCtx(env), contentservice.ReadParams{
		WorkspaceID: flags.workspace, Kind: flags.kind, Operation: flags.operation,
		Target: flags.target, IfRevision: flags.ifRevision, Path: flags.path, Parent: flags.parent,
		Offset: flags.offset, Limit: flags.limit, Provider: flags.provider, Matcher: flags.matcher,
		Refresh: flags.refresh,
	})
	if err != nil {
		return contentExit(env, err)
	}
	if flags.json {
		raw, err := contentservice.EncodeReadResult(result)
		if err != nil {
			return contentExit(env, err)
		}
		return writeContentJSON(env, raw)
	}
	if result.NotModified {
		_, _ = fmt.Fprintf(env.Stdout, "not modified %s\n", result.Revision)
		return 0
	}
	_, _ = fmt.Fprintf(env.Stdout, "%s %s\n", result.Kind, result.Display)
	return 0
}
