---
name: feature-flags
description: Creating and using feature flags in sidecar for gating experimental functionality. Covers flag registration, checking flags in code, config file and CLI overrides, and priority resolution. Use when adding feature flags, toggling features, or gating new functionality behind flags.
user-invocable: false
---

# Feature Flags

Feature flags gate experimental functionality behind user-configurable settings, enabling safe rollout (default off), user opt-in, and easy rollback.

## Checking Feature State

```go
import "github.com/marcus/sidecar/internal/features"

if features.IsEnabled("tmux_interactive_input") {
    // Feature-gated code
}
```

## Adding a New Feature Flag

1. Define the feature in `internal/features/features.go`:

```go
var MyNewFeature = Feature{
    Name:        "my_new_feature",
    Default:     false,
    Description: "Description of what this enables",
}
```

2. Add to the `allFeatures` slice:

```go
var allFeatures = []Feature{
    TmuxInteractiveInput,
    MyNewFeature, // Add here
}
```

3. Use the feature check in your code:

```go
if features.IsEnabled("my_new_feature") {
    // New functionality
}
```

That is all a flag needs to be reachable. Configuration → System → **Feature
Flags** derives its list from `features.ListAll()`, so registering a feature
puts a working switch on the page with the registry's own `Name` as the label
and `Description` as the help text. There is no second list to remember.

## Giving a flag better copy (optional)

`previewCopy` in `internal/configui/page_flags.go` overrides the registry's
wording per flag. Only fill in what you want to change:

```go
features.MyNewFeature.Name: {
    label:   "Human-readable name",
    help:    "One sentence on what turning this on does.",
    restart: true, // only if a consumer reads it once at startup
    note:    "An honest scope line for a flag that applies live but not retroactively.",
},
```

`restart` must be checked against what actually consumes the flag, never added
as blanket caution — a flag read at the point of use applies immediately and
must not claim otherwise. Both `restart` and `note` render when both are set.

**A flag that another page already owns as a first-class setting** sets `owner`
and `ownerControl` instead. It is then listed read-only with a jump to the
control that owns it, because two switches over one value is how surfaces start
disagreeing. If the owning page's control means more than the raw flag — Panels'
Conversations switch is the flag *and* the plugin's own `enabled` key — also set
`reads` so the row reports the owner's answer rather than the flag's.

**Keep the page shorter than a small terminal.** The Configuration detail pane
truncates rather than scrolling, and the row cursor still walks onto rows that
were cut. Rows are one line each with the explanation shown only under the
focused row for that reason; `TestFlagsPageFitsAnOrdinaryTerminal` guards it.

## User Configuration

### Config file (`~/.config/sidecar/config.json`)

```json
{
  "features": {
    "flags": {
      "tmux_interactive_input": true
    }
  }
}
```

### CLI override (takes precedence over config)

```bash
sidecar --enable-feature=tmux_interactive_input
sidecar --disable-feature=tmux_interactive_input
sidecar --enable-feature=feature1,feature2   # Multiple features
```

Unknown feature names in CLI flags produce a warning but do not prevent startup.

## Priority Order

Feature state resolves in this order (first match wins):
1. CLI override (`--enable-feature`, `--disable-feature`)
2. Config file (`features.flags` in config.json)
3. Default value (defined in code)

## Available Features

| Feature | Default | Description |
|---------|---------|-------------|
| `tmux_interactive_input` | true | Write support for tmux panes |
| `tmux_full_attach` | false | Suspend Sidecar and `tmux attach-session` |
| `tmux_inline_edit` | true | Inline file editing via tmux in files plugin |
| `notes_plugin` | true | Project Notes while the td panel is enabled |
| `tasks_plugin` | false | Embedded Tasks plugin tab |
| `conversations_plugin` | false | Conversations multi-agent history tab (off = no adapters / session I/O) |
| `workspace_terminal_panel` | true | Workspace Ctrl+T / Alt+T split terminal panel |

## API Reference

```go
features.IsEnabled(name string) bool           // Check if enabled
features.List() map[string]bool                 // All features with current state
features.ListAll() []Feature                    // All features with metadata
features.SetEnabled(name string, enabled bool) error  // Persist to config
features.SetOverride(name string, enabled bool) // Runtime override (not persisted)
features.IsKnownFeature(name string) bool       // Check if registered
```

## Best Practices

- Use `snake_case` for feature names (e.g., `my_new_feature`)
- New experimental features should default to `false`
- Provide clear descriptions for each feature
- Document features in `docs/guides/deprecated/feature-flags.md` when adding them
- Remove feature flags once features are stable
