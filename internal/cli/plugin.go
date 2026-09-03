package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/plugins/assembly"
)

// `sidecar plugin` is the non-interactive surface for the plugin ecosystem.
//
// Hosting plugins is a capability Sidecar owns rather than a pleasant view over
// somebody else's, so the standing "presentation layer, no CLI parity"
// exception does not apply: every operation gets a non-interactive path from
// the first milestone. `list` is that path today; the verbs that talk to an
// external plugin process arrive with the protocol host.

type pluginJSONItem struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Class      string   `json:"class"`
	Scope      string   `json:"scope"`
	Placements []string `json:"placements"`
	Enabled    bool     `json:"enabled"`
}

type pluginListJSON struct {
	Plugins []pluginJSONItem `json:"plugins"`
}

func pluginCommand() *Command {
	jsonFlag := Flag{Name: "--json", Summary: "Write one structured result object to stdout", Bool: true}
	helpFlag := Flag{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}

	listCmd := &Command{
		Name:    "list",
		Summary: "List the plugins Sidecar can host",
		Usage:   "sidecar plugin list [--json]",
		Long: "List every plugin Sidecar knows about, in the order the header paints them:\n" +
			"the project tabs first, then the global-space tabs.\n\n" +
			"Each row reports the plugin's class (who renders it), scope (project plugins are\n" +
			"rebuilt on a project switch, global ones are built once), the placements its\n" +
			"content can occupy, and whether it is enabled.\n\n" +
			"Enablement is plugins.<id>.enabled. Two deprecated feature flags, tasks_plugin\n" +
			"and notes_plugin, still answer for their plugin while that key is absent.\n\n" +
			"This reads configuration directly and runs no plugin: it does not require a\n" +
			"running Sidecar and starts no subprocess.",
		Flags: []Flag{jsonFlag, helpFlag},
		Args:  ArgSpec{Min: 0, Max: 0},
		ExitCodes: []ExitCode{
			{Code: 0, Summary: "success"},
			{Code: 1, Summary: "configuration read failure"},
			{Code: 2, Summary: "usage error"},
		},
		Examples: []Example{
			{Command: "sidecar plugin list"},
			{Command: "sidecar plugin list --json"},
		},
		Agent: AgentDoc{
			Invocation: "sidecar plugin list --json",
			Summary:    "See which plugins this Sidecar hosts, their scope, and whether each is on",
		},
		Run: runPluginList,
	}

	return &Command{
		Name:    "plugin",
		Summary: "Inspect the plugins Sidecar hosts",
		Usage:   "sidecar plugin <command> [options]",
		Long: "Inspect the plugins Sidecar hosts. A plugin is either embedded (compiled into\n" +
			"Sidecar, with its own UI) or, once the protocol host lands, an external\n" +
			"executable Sidecar renders.",
		Sub: []*Command{listCmd},
		Run: runPluginRoot,
	}
}

func runPluginRoot(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("plugin")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	sub := cmd.FindSubcommand(args[0])
	if sub != nil && sub.Run != nil {
		return sub.Run(env, args[1:])
	}
	cliErrf(env.Stderr, "unknown plugin command %q\n\n%s", args[0], RenderHelp(cmd))
	return 2
}

func runPluginList(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("plugin").FindSubcommand("list")
	help := RenderHelp(cmd)

	jsonOutput := false
	for _, arg := range args {
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			jsonOutput = true
		default:
			if strings.HasPrefix(arg, "-") {
				cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
				return 2
			}
			cliErrf(env.Stderr, "plugin list takes no positional arguments\n\n%s", help)
			return 2
		}
	}

	cfg, err := config.Load()
	if err != nil {
		cliErrln(env.Stderr, err)
		return 1
	}
	// The TUI initializes the feature manager after this dispatch point, so a
	// CLI process has to do it itself: without this, the deprecated flags a
	// descriptor still reads as aliases would answer from their built-in
	// defaults rather than from the user's configuration.
	features.Init(cfg)

	items := pluginItems(assembly.Descriptors(), cfg)

	if jsonOutput {
		if err := json.NewEncoder(env.Stdout).Encode(pluginListJSON{Plugins: items}); err != nil {
			cliErrln(env.Stderr, err)
			return 1
		}
		return 0
	}

	idWidth, classWidth, scopeWidth := 0, 0, 0
	for _, item := range items {
		idWidth = max(idWidth, len(item.ID))
		classWidth = max(classWidth, len(item.Class))
		scopeWidth = max(scopeWidth, len(item.Scope))
	}
	for _, item := range items {
		state := "off"
		if item.Enabled {
			state = "on"
		}
		_, _ = fmt.Fprintf(env.Stdout, "%-*s  %-*s  %-*s  %-8s  %s\n",
			idWidth, item.ID,
			classWidth, item.Class,
			scopeWidth, item.Scope,
			strings.Join(item.Placements, ","),
			state)
	}
	return 0
}

// pluginItems projects the descriptor catalog onto the reported rows. It is a
// pure function of the catalog and the configuration, so the CLI's answer and
// the settings page's cannot disagree about what is enabled.
func pluginItems(descriptors []plugin.Descriptor, cfg *config.Config) []pluginJSONItem {
	items := make([]pluginJSONItem, 0, len(descriptors))
	for _, d := range descriptors {
		placements := make([]string, 0, len(d.Placements))
		for _, p := range d.Placements {
			placements = append(placements, string(p))
		}
		items = append(items, pluginJSONItem{
			ID:         d.ID,
			Name:       d.Name,
			Class:      string(d.Class),
			Scope:      string(d.Scope),
			Placements: placements,
			Enabled:    d.IsEnabled(cfg),
		})
	}
	return items
}
