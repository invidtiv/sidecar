import React, {useCallback, useEffect, useRef, useState} from 'react';
import Link from '@docusaurus/Link';
import Layout from '@theme/Layout';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import SidecarDemo, {SidecarStill} from '@site/src/components/Tui/Demo';
import {THEMES} from '@site/src/components/Tui/theme';
import styles from './index.module.css';

// Real numbers, so the badge is a fact rather than a flourish. Refresh with
// `git rev-list --count HEAD` and `git log --reverse --format=%ad --date=short`.
const COMMIT_COUNT = 1934;
const FIRST_COMMIT_MONTH = 'December 2025';

const BREW_COMMAND = 'brew install marcus/tap/sidecar';
const CURL_COMMAND =
  'curl -fsSL https://raw.githubusercontent.com/marcus/sidecar/main/scripts/setup.sh | bash';

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
    body: 'Sessions puts every agent in every repo on one screen — what it is on, how long it has been there, and which one is stuck waiting for you.',
  },
  {
    step: '2. Steer',
    color: 'oklch(0.78 0.10 165)',
    body: 'Enter drops you into that agent’s shell and you just type. It is a real tmux session underneath, and you never have to know that.',
  },
  {
    step: '3. Review',
    color: 'oklch(0.80 0.09 250)',
    body: 'The agent opens the file it wants you to look at in a pane beside the conversation. Diffs, history and td issues land the same way.',
  },
  {
    step: '4. Merge',
    color: 'oklch(0.78 0.11 350)',
    body: 'Each workspace is its own worktree and branch. One key commits, pushes, opens the PR and cleans the tree up behind you.',
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
    'Project switcher',
    '@',
    'Jump between repos without restarting. Active tab, cursor and scroll come back exactly where you left them, per project.',
  ],
  [
    'Worktree switcher',
    'W',
    'Move between worktrees inside a repo. Sidecar remembers which one you were in and restores it next time.',
  ],
  [
    'Configuration screen',
    ',',
    'Appearance, projects, workspaces, agents, terminal and integrations — a real settings surface, not a JSON file you have to guess at.',
  ],
  [
    'Setup check',
    'sidecar setup',
    'Looks at tmux, terminal colour support, your projects and your AGENTS.md, then offers to fix what it finds.',
  ],
  [
    'File finder and search',
    'ctrl+p',
    'Fuzzy find by name or grep the tree by content, scoped to the pane you are in.',
  ],
  [
    'Edit in place',
    'e',
    'Open $EDITOR inside the preview pane instead of taking over the screen, or edit inline without leaving the tree.',
  ],
  [
    'Notes',
    'Notes tab',
    'A project scratchpad that converts straight into a td issue or a worktree spec when the thought turns into work.',
  ],
  [
    'Mouse, all of it',
    'drag',
    'Click to focus, scroll anywhere, drag a divider to resize a split. Vim keys do everything too.',
  ],
  [
    'Single binary, MIT',
    'go',
    'No runtime, no daemon, no telemetry. It starts in milliseconds and the source is yours to read.',
  ],
];

function ThemeGallery() {
  const [theme, setTheme] = useState('sidecar-modern');
  return (
    <Reveal className={styles.themeSection}>
      <div>
        <div className={styles.kicker}>themes</div>
        <h3 className={styles.h3}>Twenty-one themes, all of them finished</h3>
        <p className={styles.body}>
          Not a light mode and a dark mode. Twenty-one full palettes, each one
          contrast-checked against every surface — tabs, diffs, key hints, kanban
          lanes, blame ages. Press <code>#</code> in the app; pick one here and
          the mockup follows.
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

  return (
    <Layout
      title="A terminal workspace for the agents you have running"
      description="Sidecar shows every coding agent across every repo, drops you into any of their shells without touching tmux, and lets them open files and tasks in a pane beside the conversation.">
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
                    <strong>Sidecar 1.0 is here</strong>
                    <span className={styles.milestoneMeta}>
                      {COMMIT_COUNT.toLocaleString('en-US')} commits since{' '}
                      {FIRST_COMMIT_MONTH}
                    </span>
                  </span>
                </Link>
              </div>
              <h1 className={styles.h1}>
                You might never open
                <br />
                your <em>editor</em> again.
              </h1>
              <p className={styles.lede}>
                Sidecar is a terminal workspace for the agents you already have
                running. See every session across every repo, drop into any of
                their shells without ever touching tmux, and let an agent put a
                file or a <code>td</code> task in a pane right beside the
                conversation.
              </p>
              <CopyCommand command={BREW_COMMAND} />
              <div className={styles.installAlt}>
                <span>Linux and everything else:</span>
                <Link to="/docs/intro">curl · binary · from source →</Link>
              </div>
            </div>
          </section>

          <section className={styles.demoBlock}>
            <div className={styles.demoHead}>
              <Shimmer>Try the tabs</Shimmer>
              <span style={{color: 'var(--sc-text-4)'}}>
                single binary · starts in milliseconds · no telemetry
              </span>
            </div>
            <div className={styles.demoShell}>
              <SidecarDemo />
            </div>
          </section>
        </div>

        {/* ------------------------------------------------------- loop */}
        <section className={`${styles.section} ${styles.sectionTint}`}>
          <div className={styles.wrap}>
            <Reveal>
              <h2 className={styles.h2}>The loop, once you stop context switching</h2>
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
              body="Sessions is the view most people live in. It gathers the agent shells and worktrees from every project you have configured, sorts them by what moved most recently, and shows you which ones are working, which are done, and which are blocked on an answer from you."
              points={[
                'The same fleet as a list or as a kanban board — Working, Blocked, Done, Idle, Paused',
                'Live output preview for the selected shell, without attaching to it',
                'Enter opens the workspace; the project scope and its tabs come with it',
                'Polls on a schedule that backs off when nothing is moving',
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
                  A full agent session, without ever meeting tmux
                </h2>
              </div>
              <p className={styles.body} style={{maxWidth: '52ch', marginTop: 0}}>
                Every shell Sidecar starts is a real tmux session, and you never
                have to know it. You type into the pane, scroll its history,
                select and copy from it, and resize it by dragging — no prefix
                keys, no attach, no detach. Close Sidecar and the agents keep
                running; open it tomorrow and they are all still there.
              </p>
            </Reveal>
            <Reveal style={{marginTop: 40}}>
              <Mock screen="workspaces" height="clamp(340px, 34vw, 440px)" />
            </Reveal>
            <Reveal className={styles.widePoints}>
              {[
                'Type straight into any agent shell from inside the TUI',
                'Lazy scrollback, search, text selection and paste',
                'Shells survive Sidecar restarts and are recovered by name',
                'Agents rename their own shell to describe what they are doing',
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
                Agents can put things in front of you
              </h2>
              <p className={styles.body} style={{maxWidth: '70ch'}}>
                An agent that wants you to look at something does not have to
                describe a path and hope. It runs one command and the thing
                appears in a pane next to the conversation, at the right line,
                where you can read it while the agent keeps going. You can open
                the same things yourself by clicking a path in the shell output.
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
              title="Run four features at once without a single git command"
              body="Press n. Sidecar creates a git worktree in a sibling directory, branches it, links a td task to it, starts the agent you picked and hands it the task as context. Each one is a real isolated checkout, so four agents can build four features without touching each other's tree."
              points={[
                'Create, switch, merge and delete a worktree with one key each',
                'PR status and CI checks read straight from GitHub',
                'Merge workflow: commit, push, open the PR, then clean up the tree',
                'Sidecar keeps its own state files out of your repo automatically',
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
              title="Read what the agent actually changed"
              body="Staged, unstaged and untracked on the left; a syntax-highlighted diff on the right. Stage a hunk, write the commit message, walk back through history, or blame a line — the review happens where the work happened."
              points={[
                'Stage and unstage with one key, commit without leaving the tab',
                'Unified or side-by-side diffs, full-screen when you need the room',
                'Commit history with per-commit diffs and file stats',
                'Refreshes itself when the agent writes to disk',
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
              body="td is built for agents: durable context across compaction, progress logs, handoffs, and a review before anything closes. Tasks is the other half — a full personal task manager in the same window, with lists, priorities, tags, due dates, a board and a journal. Hand one of your tasks to an agent and it crosses over into td, then comes back as something to review."
              points={[
                'td: focused task, status-filtered board, session activity, one-key review',
                'Tasks: lists, priorities, tags, due dates, kanban, journal, undo',
                'Both read the same store the td CLI does — nothing is trapped in the UI',
                'Tasks is in beta behind the tasks_plugin flag',
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
              <h2 className={styles.h2}>Whichever agent you use</h2>
              <p className={styles.body} style={{maxWidth: '46ch', marginTop: 0}}>
                Sidecar launches and watches agents by name, and normalises their
                very different session formats into one timeline. Swapping agents
                costs you nothing here.
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
              <h2 className={styles.h2}>Built for the terminal you already know</h2>
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
              Start it in the repo you are already in
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
