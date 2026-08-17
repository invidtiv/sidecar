import themes from '@site/src/data/themes.json';

/**
 * The 21 themes shipped in the app, extracted from internal/styles so the site
 * and the binary cannot drift. Order matches canonicalThemeOrder.
 */
export const THEMES = themes;
export const DEFAULT_THEME = 'sidecar-modern';

export function findTheme(name) {
  return THEMES.find((t) => t.name === name) || THEMES[0];
}

/**
 * Map a Sidecar palette onto the CSS variables the mockups read. Anything the
 * app derives (lane colours, diff fills) is passed through rather than guessed,
 * so a theme looks here exactly as it looks in the terminal.
 */
export function themeVars(theme) {
  const c = theme.colors;
  const gradA = c.GradientBorderActive || [c.BorderActive, c.BorderActive];
  const gradN = c.GradientBorderNormal || [c.BorderNormal, c.BorderMuted];
  return {
    '--tui-bg': c.BgPrimary,
    '--tui-bg-secondary': c.BgSecondary,
    '--tui-bg-tertiary': c.BgTertiary,
    '--tui-bar': c.BgSecondary,
    '--tui-titlebar': c.BgSecondary,
    '--tui-raised': c.SurfaceRaised,
    '--tui-chrome-edge': c.BorderMuted,
    '--tui-text': c.TextPrimary,
    '--tui-text-bright': c.TextHighlight || c.TextPrimary,
    '--tui-text-selected': c.TextSelection || '#ffffff',
    '--tui-muted': c.TextMuted,
    '--tui-subtle': c.TextSubtle,
    '--tui-primary': c.Primary,
    '--tui-key': c.KeyHintFg || c.Primary,
    '--tui-tab-inactive': c.TabTextInactive || c.TextMuted,
    '--tui-info': c.Info,
    '--tui-success': c.Success,
    '--tui-warning': c.Warning,
    '--tui-error': c.Error,
    '--tui-done': c.LaneDone || c.Info,
    '--tui-idle': c.LaneIdle || c.TextMuted,
    '--tui-paused': c.LanePaused || c.TextSubtle,
    '--tui-border-a': gradN[0],
    '--tui-border-b': gradN[1] || gradN[0],
    '--tui-border-active-a': gradA[0],
    '--tui-border-active-b': gradA[1] || gradA[0],
    '--tui-border-muted': c.BorderMuted,
    '--tui-diff-add-fg': c.DiffAddFg,
    '--tui-diff-add-bg': c.DiffAddBg,
    '--tui-diff-del-fg': c.DiffRemoveFg,
    '--tui-diff-del-bg': c.DiffRemoveBg,
    '--tui-agent-claude': (c.AgentColors && c.AgentColors.claude) || c.Warning,
    '--tui-agent-codex': (c.AgentColors && c.AgentColors.codex) || c.TextSecondary,
    '--tui-agent-cursor': (c.AgentColors && c.AgentColors.cursor) || c.Info,
    '--tui-agent-gemini': (c.AgentColors && c.AgentColors.gemini) || c.Link,
    '--tui-project-1': (c.ProjectHues || [])[0] || c.Primary,
    '--tui-project-2': (c.ProjectHues || [])[1] || c.Info,
    '--tui-project-3': (c.ProjectHues || [])[2] || c.Success,
    '--tui-project-4': (c.ProjectHues || [])[3] || c.Warning,
  };
}
