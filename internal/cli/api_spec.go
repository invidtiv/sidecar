package cli

import (
	"fmt"

	"github.com/marcus/sidecar/internal/uiapi"
)

func apiSpecCommand() *Command {
	return &Command{Name: "spec", Summary: "Print the generated OpenAPI 3.1 and stream schemas", Usage: "sidecar api spec [--json]",
		Long:      "Print the complete OpenAPI 3.1 JSON document generated from the HTTP and terminal Go wire types. No server or tmux is needed. Both forms emit JSON; --json is accepted for consistency with other API commands.",
		Flags:     []Flag{{Name: "--json", Summary: "Write the OpenAPI JSON document", Bool: true}, {Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}},
		ExitCodes: []ExitCode{{Code: 0, Summary: "success"}, {Code: 1, Summary: "could not generate or write the spec"}, {Code: 2, Summary: "usage error"}},
		Examples:  []Example{{Command: "sidecar api spec --json"}},
		Run:       runAPISpec, Agent: AgentDoc{Invocation: "sidecar api spec --json", Summary: "Discover HTTP resources and WebSocket wire schemas"}}
}
func runAPISpec(env Env, args []string) int {
	if len(args) == 1 && isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(apiSpecCommand()))
		return 0
	}
	if _, err := parseAPIFlags(args, []string{"--json"}, nil); err != nil {
		cliErrln(env.Stderr, err)
		return 2
	}
	data, err := uiapi.Spec()
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	if _, err := env.Stdout.Write(data); err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	return 0
}
