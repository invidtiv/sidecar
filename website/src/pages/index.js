import React, {useCallback, useEffect, useRef, useState} from 'react';
import Link from '@docusaurus/Link';
import useBaseUrl from '@docusaurus/useBaseUrl';
import Layout from '@theme/Layout';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import SidecarDemo, {SidecarStill} from '@site/src/components/Tui/Demo';
import {THEMES} from '@site/src/components/Tui/theme';
import styles from './index.module.css';

// Real commit history start date for the milestone badge
const FIRST_COMMIT_MONTH = 'December 2025';

const BREW_COMMAND = 'brew install marcus/tap/sidecar';
const CURL_COMMAND =
  'curl -fsSL https://raw.githubusercontent.com/marcus/sidecar/main/scripts/setup.sh | bash';

function QuickStartNote() {
  return (
    <div className={styles.quickNoteWrapper}>
      <svg
        className={styles.noteArrow}
        viewBox="0 0 60 34"
        fill="none"
        xmlns="http://www.w3.org/2000/svg"
        aria-hidden="true">
        <path
          d="M 4 8 C 22 2, 42 6, 52 24"
          stroke="var(--sc-accent)"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeDasharray="3 3.5"
        />
        <path
          d="M 42 23 L 52 24 L 51 14"
          stroke="var(--sc-accent)"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
      <div className={styles.quickNote}>
        <div className={styles.noteTape} aria-hidden="true" />
        <div className={styles.noteHeader}>almost zero setup —</div>
        <div className={styles.noteBody}>
          Just <code className={styles.noteCode}>cd</code> into your project &amp; type{' '}
          <code className={styles.noteCode}>sidecar</code>
        </div>
        <div className={styles.noteFooter}>
          no configs or daemons — it auto-detects your repo, agents &amp; tasks.
        </div>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ atoms */

function CopyCommand({command, prompt = '$'}) {
  const [copied, setCopied] = useState(false);
  const copy = useCallback(() => {
    navigator.clipboard?.writeText(command).then(
      () => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1600);
      },
      () => {},
    );
  }, [command]);

  return (
    <div className={styles.install}>
      <div className={styles.installBox}>
        <span className={styles.installPrompt}>{prompt}</span>
        <span>{command}</span>
      </div>
      <button
        type="button"
        className={`${styles.copyBtn} ${copied ? styles.copyBtnDone : ''}`}
        onClick={copy}
        aria-label={copied ? 'Copied' : 'Copy install command'}>
        {copied ? '✓' : '⧉'}
      </button>
    </div>
  );
}

/**
 * Text whose letters catch the accent one after another every few seconds —
 * a wave slow and low-contrast enough to read as an invitation rather than an
 * alert. It is the only thing telling you the demo above is clickable.
 */
function Shimmer({children}) {
  return (
    <span className={styles.shimmer} aria-label={children}>
      {Array.from(children).map((ch, i) => (
        <span
          // eslint-disable-next-line react/no-array-index-key
          key={i}
          aria-hidden="true"
          style={{'--i': i}}>
          {ch === ' ' ? ' ' : ch}
        </span>
      ))}
    </span>
  );
}

/** A section mockup, pannable rather than shrunken on a narrow screen. */
function Mock(props) {
  return (
    <div className={styles.scrollMock}>
      <SidecarStill {...props} />
    </div>
  );
}

/** Reveals a block once, the first time it enters the viewport. */
function Reveal({as: Tag = 'div', delay = 0, children, ...rest}) {
  const ref = useRef(null);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof IntersectionObserver === 'undefined') {
      if (el) el.dataset.reveal = 'in';
      return undefined;
    }
    const io = new IntersectionObserver(
      ([entry]) => {
        if (!entry.isIntersecting) return;
        el.style.transitionDelay = `${delay}ms`;
        el.dataset.reveal = 'in';
        io.disconnect();
      },
      {rootMargin: '0px 0px -12% 0px'},
    );
    io.observe(el);
    return () => io.disconnect();
  }, [delay]);
  return (
    <Tag ref={ref} data-reveal="" {...rest}>
      {children}
    </Tag>
  );
}

function Feature({kicker, color, title, body, points, children, reverse}) {
  const prose = (
    <div>
      <div className={styles.kicker} style={{'--kicker-color': color}}>
        {kicker}
      </div>
      <h3 className={styles.h3}>{title}</h3>
      <p className={styles.body}>{body}</p>
      {points ? (
        <div className={styles.points}>
          {points.map((p) => (
            <div key={p} className={styles.point} style={{'--point-color': color}}>
              {p}
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
  return (
    <Reveal
      className={`${styles.split} ${reverse ? styles.splitReverse : ''}`}>
      {reverse ? (
        <>
          {children}
          {prose}
        </>
      ) : (
        <>
          {prose}
          {children}
        </>
      )}
    </Reveal>
  );
}

/* --------------------------------------------------------------- sections */

const LOOP = [
  {
    step: '1. See',
    color: 'oklch(0.82 0.13 82)',
    body: 'Sessions brings every agent across every project into one view — tracking live progress, session duration, and notifying you the moment an agent is ready.',
  },
  {
    step: '2. Steer',
    color: 'oklch(0.78 0.10 165)',
    body: 'Jump directly into any agent’s shell with instant keystroke response, full searchable scrollback, and persistent session recovery.',
  },
  {
    step: '3. Review',
    color: 'oklch(0.80 0.09 250)',
    body: 'Inspect changes alongside the conversation — files, diffs, git history, and tasks open automatically in synchronized adjacent panes.',
  },
  {
    step: '4. Merge',
    color: 'oklch(0.78 0.11 350)',
    body: 'Each feature develops in an isolated git worktree. Stage, commit, push, create PRs, and clean up branches with single keystrokes.',
  },
];

const TERMINALS = [
  {
    name: 'Ghostty',
    icon: '/img/terminals/ghostty.png',
    platform: 'macOS · Linux',
  },
  {
    name: 'iTerm2',
    icon: '/img/terminals/iterm2.png',
    platform: 'macOS',
  },
  {
    name: 'Kitty',
    icon: '/img/terminals/kitty.svg',
    platform: 'macOS · Linux',
  },
  {
    name: 'Alacritty',
    icon: '/img/terminals/alacritty.svg',
    platform: 'macOS · Linux · Windows',
  },
  {
    name: 'WezTerm',
    icon: '/img/terminals/wezterm.svg',
    platform: 'macOS · Linux · Windows',
  },
  {
    name: 'Warp',
    icon: '/img/terminals/warp.svg',
    platform: 'macOS · Linux',
  },
  {
    name: 'GNOME Terminal',
    icon: '/img/terminals/gnome-terminal.svg',
    platform: 'Linux',
  },
  {
    name: 'Windows Terminal',
    icon: '/img/terminals/windows-terminal.svg',
    platform: 'Windows · WSL',
  },
];

const AGENTS = [
  ['Claude Code', 'Anthropic’s CLI for Claude'],
  ['Codex', 'OpenAI’s coding agent'],
  ['Cursor', 'cursor-agent, from the Cursor team'],
  ['Gemini CLI', 'Google’s terminal agent'],
  ['Antigravity', 'Google DeepMind’s agentic assistant'],
  ['GitHub Copilot', 'GitHub’s CLI pair programmer'],
  ['xAI Grok', 'xAI’s developer CLI'],
  ['Amp Code', 'Amp’s coding assistant'],
  ['OpenCode', 'Terminal-first open source agent'],
  ['Kiro', 'Amazon’s AI coding assistant'],
  ['Pi', 'Pi agent (OpenClaw)'],
  ['Warp', 'Warp’s terminal assistant'],
];

const DETAILS = [
  [
    'Cross-project context',
    '@',
    'Switch between repositories with zero friction. Active tabs, cursor position, and scrollback are remembered per project.',
  ],
  [
    'Worktree switcher',
    'W',
    'Switch between worktrees instantly inside any repository with state restored automatically.',
  ],
  [
    'Configuration screen',
    ',',
    'Visual settings for appearance, projects, workspaces, agents, terminal behavior, and integrations.',
  ],
  [
    'Setup check',
    'sidecar setup',
    'Analyzes your environment, color support, projects, and agent configurations with automated fixes.',
  ],
  [
    'Fuzzy finder & search',
    'ctrl+p',
    'Fuzzy search filenames or grep code across the workspace, scoped to your active pane.',
  ],
  [
    'Inline editor',
    'e',
    'Open $EDITOR inside the split preview or make quick edits inline without leaving the workspace.',
  ],
  [
    'Cross-project notes',
    'Notes tab',
    'A persistent scratchpad for every project that turns thoughts into tracked tasks and worktree specs.',
  ],
  [
    'Intuitive mouse & vim navigation',
    'drag',
    'Click to focus, scroll anywhere, drag split dividers to resize, with full vim keybinding parity.',
  ],
  [
    'Fast native binary',
    'go',
    'Compiled into a single fast executable with zero runtime dependencies and instant startup.',
  ],
];

function ThemeGallery() {
  const [theme, setTheme] = useState('sidecar-modern');

  useEffect(() => {
    const onThemeChange = (e) => {
      if (e.detail) setTheme(e.detail);
    };
    window.addEventListener('sidecar-theme-change', onThemeChange);
    const stored = localStorage.getItem('sidecar-theme');
    if (stored) setTheme(stored);
    return () => window.removeEventListener('sidecar-theme-change', onThemeChange);
  }, []);

  return (
    <Reveal className={styles.themeSection}>
      <div>
        <div className={styles.kicker}>themes</div>
        <h3 className={styles.h3}>Twenty-one handcrafted themes</h3>
        <p className={styles.body}>
          Twenty-one precision-tuned palettes, each contrast-checked across every
          surface — tabs, diffs, key hints, kanban lanes, and file trees.
          Press <code>#</code> in the app; pick one here and the mockup follows.
        </p>
        <div className={styles.themeList}>
          {THEMES.map((t) => {
            const c = t.colors;
            const active = t.name === theme;
            return (
              <button
                key={t.name}
                type="button"
                className={`${styles.themeBtn} ${active ? styles.themeBtnActive : ''}`}
                style={{'--swatch': c.Primary}}
                onClick={() => setTheme(t.name)}
                aria-pressed={active}>
                <span className={styles.swatch} aria-hidden="true">
                  <span style={{background: c.Primary}} />
                  <span style={{background: c.Info}} />
                  <span style={{background: c.Success}} />
                  <span style={{background: c.Error}} />
                </span>
                {t.displayName}
              </button>
            );
          })}
        </div>
      </div>
      <Mock screen="files" theme={theme} height="clamp(340px, 38vw, 460px)" />
    </Reveal>
  );
}

/* ------------------------------------------------------------------- page */

export default function Home() {
  const {siteConfig} = useDocusaurusContext();
  const githubUrl = siteConfig.customFields.githubUrl;
  const commitCount = siteConfig.customFields.commitCount || 2374;
  const currentVersion = siteConfig.customFields.currentVersion || '1.5';

  return (
    <Layout
      title="A terminal workspace for the agents you have running"
      description="Sidecar gives you complete context for everything you need to develop in one project or across many — agent sessions, git diffs, durable tasks, and notes in a single terminal.">
      <main className={styles.page}>
        {/* ------------------------------------------------------- hero */}
        <div className={styles.grid}>
          <section className={styles.hero}>
            <div className={styles.wrap}>
              <div className={styles.heroTop}>
                <div className={styles.eyebrow}>
                  <span className={styles.eyebrowDot} />
                  Free &amp; open source (MIT) · macOS &amp; Linux
                </div>
                <Link
                  className={styles.milestone}
                  to="https://github.com/marcus/sidecar/releases">
                  <span className={styles.milestoneMark} aria-hidden="true" />
                  <span>
                    <strong>Sidecar {currentVersion} is here</strong>
                    <span className={styles.milestoneMeta}>
                      {commitCount.toLocaleString('en-US')} commits since{' '}
                      {FIRST_COMMIT_MONTH}
                    </span>
                  </span>
                </Link>
              </div>
              <h1 className={styles.h1}>
                Full development context.
                <br />
                In <em>one</em> terminal.
              </h1>
              <p className={styles.lede}>
                Sidecar brings together everything you need to build across all your projects
                in a single terminal. Monitor live agent sessions, inspect git diffs,
                track durable tasks, take notes, and manage your day without losing your place.
              </p>
              <div className={styles.heroActions}>
                <div className={styles.installCol}>
                  <CopyCommand command={BREW_COMMAND} />
                  <div className={styles.installAlt}>
                    <span>Linux and everything else:</span>
                    <Link to="/docs/intro">curl · binary · from source →</Link>
                  </div>
                </div>
                <QuickStartNote />
              </div>
            </div>
          </section>

          <section className={styles.demoBlock}>
            <div className={styles.demoHead}>
              <Shimmer>Try the tabs</Shimmer>
              <span style={{color: 'var(--sc-text-4)'}}>
                instant startup · native performance · 100% offline &amp; private
              </span>
            </div>
            <div className={styles.demoShell}>
              <SidecarDemo />
            </div>
          </section>
        </div>

        {/* --------------------------------------------------- terminals */}
        <section className={styles.terminalsSection}>
          <div className={styles.wrap}>
            <Reveal className={styles.terminalsHeader}>
              <div>
                <div
                  className={styles.kicker}
                  style={{'--kicker-color': 'var(--sc-green)'}}>
                  compatibility
                </div>
                <h2 className={styles.h2} style={{marginTop: 14}}>
                  Works in the terminal you already use
                </h2>
              </div>
              <p className={styles.terminalsSub}>
                Sidecar brings rich multi-pane workspaces, agent orchestration, and
                task management directly into your favorite terminal emulator on macOS,
                Linux, and Windows.
              </p>
            </Reveal>
            <Reveal className={styles.terminalGrid}>
              {TERMINALS.map((t) => (
                <div key={t.name} className={styles.terminalCard}>
                  <div className={styles.terminalIconWrap}>
                    <img
                      src={useBaseUrl(t.icon)}
                      alt={`${t.name} logo`}
                      className={styles.terminalIcon}
                      loading="lazy"
                    />
                  </div>
                  <div className={styles.terminalName}>{t.name}</div>
                  <div className={styles.terminalPlatform}>{t.platform}</div>
                </div>
              ))}
            </Reveal>
          </div>
        </section>

        {/* ------------------------------------------------------- loop */}
        <section className={`${styles.section} ${styles.sectionTint}`}>
          <div className={styles.wrap}>
            <Reveal>
              <h2 className={styles.h2}>A complete development loop in one screen</h2>
            </Reveal>
            <Reveal className={`${styles.cells} ${styles.cells4}`}>
              {LOOP.map((c) => (
                <div key={c.step} className={styles.cell}>
                  <div className={styles.cellStep} style={{'--step-color': c.color}}>
                    {c.step}
                  </div>
                  <p className={styles.cellBody}>{c.body}</p>
                </div>
              ))}
            </Reveal>
          </div>
        </section>

        {/* --------------------------------------------------- sessions */}
        <section className={styles.section}>
          <div className={styles.wrap}>
            <Feature
              kicker="sessions"
              color="oklch(0.82 0.13 82)"
              title="Every repo, every agent, one screen"
              body="Sessions gives you unified mission control for your entire workspace fleet. It gathers agent shells, git worktrees, and project status across all your repositories in one place, sorted by recent activity so you always know what is running, what is complete, and what needs your attention."
              points={[
                'Fleet overview as a list or kanban board — Working, Blocked, Done, Idle, Paused',
                'Live output preview for any agent shell with zero latency',
                'One key opens the full project workspace with tabs, files, and state preserved',
                'Lightweight background monitoring that keeps you informed in real time',
              ]}>
              <Mock screen="sessions" />
            </Feature>
          </div>
        </section>

        {/* ------------------------------------------------------ shell */}
        <section className={styles.section}>
          <div className={styles.wrap}>
            <Reveal className={styles.wideHead}>
              <div>
                <div
                  className={styles.kicker}
                  style={{'--kicker-color': 'oklch(0.78 0.10 165)'}}>
                  shell
                </div>
                <h2 className={styles.h2} style={{marginTop: 14}}>
                  Durable agent shells with persistent session state
                </h2>
              </div>
              <p className={styles.body} style={{maxWidth: '52ch', marginTop: 0}}>
                Sidecar provides resilient, interactive terminal sessions that maintain
                state across restarts. Type commands directly, search scrollback history,
                copy output, and arrange multi-pane splits freely. Close your terminal
                anytime — all agent tasks and sessions keep running in the background and
                restore exactly where you left them.
              </p>
            </Reveal>
            <Reveal style={{marginTop: 40}}>
              <Mock screen="workspaces" height="clamp(340px, 34vw, 440px)" />
            </Reveal>
            <Reveal className={styles.widePoints}>
              {[
                'Direct interactive access to every agent shell inside the TUI',
                'Fast scrollback buffer with search, text selection, and clipboard support',
                'Durable session persistence that survives terminal restarts',
                'Dynamic shell naming reflecting the agent’s live task in real time',
              ].map((p) => (
                <div
                  key={p}
                  className={styles.point}
                  style={{'--point-color': 'oklch(0.78 0.10 165)'}}>
                  {p}
                </div>
              ))}
            </Reveal>
          </div>
        </section>

        {/* -------------------------------------------------- open panes */}
        <section className={`${styles.section} ${styles.sectionTint}`}>
          <div className={styles.wrap}>
            <Reveal>
              <div className={styles.kicker}>sidecar open</div>
              <h2 className={styles.h2} style={{marginTop: 14}}>
                Live context projected directly to your screen
              </h2>
              <p className={styles.body} style={{maxWidth: '70ch'}}>
                Keep conversation and code connected side-by-side. Agents can
                programmatically project files, diffs, and tasks into adjacent panes at
                exact line numbers, giving you immediate visual clarity while the agent
                continues building.
              </p>
            </Reveal>
            <Reveal className={`${styles.cells} ${styles.cells3}`}>
              {[
                [
                  'sidecar open internal/tty/tty.go:212',
                  'A file, revealed at a line.',
                ],
                ['sidecar open td-847b0c', 'A td issue, with its acceptance criteria.'],
                ['sidecar open --diff HEAD~1', 'A diff — working tree, a commit, or a range.'],
              ].map(([cmd, note]) => (
                <div key={cmd} className={styles.cell}>
                  <div
                    className={styles.cellStep}
                    style={{'--step-color': 'oklch(0.72 0.14 155)'}}>
                    $ {cmd}
                  </div>
                  <p className={styles.cellNote}>{note}</p>
                </div>
              ))}
            </Reveal>
          </div>
        </section>

        {/* -------------------------------------------------- worktrees */}
        <section className={styles.section}>
          <div className={styles.wrap}>
            <Feature
              kicker="worktrees"
              color="oklch(0.78 0.11 350)"
              title="Parallel feature development with isolated git worktrees"
              body="Develop multiple features simultaneously with complete isolation. Press 'n' to branch a dedicated git worktree, link a task, start your preferred agent with full context, and track GitHub PR and CI status across branches."
              points={[
                'Instant worktree creation, branch switching, and lifecycle management',
                'Live GitHub PR status, review feedback, and CI check indicators',
                'Streamlined merge workflow: stage, commit, push, and create PRs in one place',
                'Automatic workspace state isolation keeping repositories clean',
              ]}>
              <Mock screen="worktrees" />
            </Feature>
          </div>
        </section>

        {/* -------------------------------------------------------- git */}
        <section className={styles.section}>
          <div className={styles.wrap}>
            <Feature
              reverse
              kicker="git"
              color="oklch(0.72 0.14 155)"
              title="Deep git inspection and inline review"
              body="Inspect everything your agents and team changed with rich syntax-highlighted diffs, commit trees, and hunk staging. Stage changes, author commit messages, and explore history right alongside your running sessions."
              points={[
                'One-key staging, hunk inspection, and streamlined commit authoring',
                'Side-by-side and unified diff views with full-screen expansion',
                'Interactive commit history with per-commit diffs and file statistics',
                'Live auto-refresh whenever agents write changes to disk',
              ]}>
              <Mock screen="git" />
            </Feature>
          </div>
        </section>

        {/* ------------------------------------------------ tasks and td */}
        <section className={`${styles.section} ${styles.sectionTint}`}>
          <div className={styles.wrap}>
            <Feature
              reverse
              kicker="tasks · beta"
              color="oklch(0.80 0.09 250)"
              title="td is where agents keep their work. Tasks is where you keep yours."
              body="Manage agent workflows and your own personal tasks in a single terminal. td provides agents with durable context across prompt compaction, structured progress logs, and verification before closing. Tasks gives you a full personal task manager with lists, tags, priorities, due dates, kanban boards, and daily journals across all your projects."
              points={[
                'Durable agent memory with progress logs, handoffs, and verification reviews',
                'Full personal task manager with tags, priorities, due dates, boards, and journals',
                'Unified cross-project task visibility accessible via both TUI and CLI',
                'Convert personal todos into tracked agent issues with a single keystroke',
              ]}>
              <Mock screen="tasks" />
            </Feature>
          </div>
        </section>

        {/* ----------------------------------------------------- themes */}
        <section className={styles.section}>
          <div className={styles.wrap}>
            <ThemeGallery />
          </div>
        </section>

        {/* ----------------------------------------------------- agents */}
        <section className={`${styles.section} ${styles.sectionTint}`}>
          <div className={styles.wrap}>
            <Reveal
              style={{
                display: 'flex',
                alignItems: 'baseline',
                justifyContent: 'space-between',
                gap: 32,
                flexWrap: 'wrap',
              }}>
              <h2 className={styles.h2}>Built for every modern coding agent</h2>
              <p className={styles.body} style={{maxWidth: '46ch', marginTop: 0}}>
                Sidecar orchestrates and monitors all your favorite CLI agents,
                normalizing their session formats and states into one cohesive developer timeline.
              </p>
            </Reveal>
            <Reveal className={`${styles.cells} ${styles.cells3}`}>
              {AGENTS.map(([name, desc]) => (
                <div key={name} className={styles.agentCell}>
                  <div className={styles.agentName}>{name}</div>
                  <div className={styles.agentDesc}>{desc}</div>
                </div>
              ))}
            </Reveal>
          </div>
        </section>

        {/* ---------------------------------------------------- details */}
        <section className={styles.section}>
          <div className={styles.wrap}>
            <Reveal>
              <h2 className={styles.h2}>Full development power inside your terminal</h2>
            </Reveal>
            <Reveal className={styles.flatGrid}>
              {DETAILS.map(([title, key, body]) => (
                <div key={title}>
                  <div className={styles.flatTitle}>
                    {title}
                    <span className={styles.flatKey}>{key}</span>
                  </div>
                  <p className={styles.flatBody}>{body}</p>
                </div>
              ))}
            </Reveal>
          </div>
        </section>

        {/* -------------------------------------------------------- cta */}
        <section className={`${styles.cta} ${styles.grid}`}>
          <div className={styles.wrapNarrow}>
            <h2 className={styles.h2} style={{fontSize: 'clamp(32px, 5vw, 52px)'}}>
              Set up takes less than a minute
            </h2>
            <div className={styles.ctaInstall}>
              <CopyCommand command={BREW_COMMAND} />
            </div>
            <div className={styles.ctaNote}>
              or{' '}
              <span style={{color: 'var(--sc-text-2)'}}>{CURL_COMMAND}</span>
            </div>
            <div className={styles.installAlt} style={{justifyContent: 'center'}}>
              <Link to="/docs/intro">Getting started →</Link>
              <Link to={githubUrl}>Source on GitHub →</Link>
            </div>
          </div>
        </section>
      </main>
    </Layout>
  );
}
