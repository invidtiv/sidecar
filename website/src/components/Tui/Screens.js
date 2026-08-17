import React from 'react';
import clsx from 'clsx';
import {
  Cursor,
  Field,
  Row,
  Rule,
  Spacer,
  TuiHandle,
  TuiModal,
  TuiPane,
  TuiPanes,
  tui,
} from './index';

/*
 * One function per Sidecar surface. The content is representative rather than
 * captured, but the chrome, glyphs, column order and key hints are transcribed
 * from the running app at 200x52.
 */

/* -------------------------------------------------------------- Sessions */

const SESSION_GROUPS = [
  {
    project: 'sidecar',
    hue: 'var(--tui-project-1)',
    rows: [
      {
        glyph: '●',
        state: 'live',
        name: 'preview tabs',
        agent: 'claude',
        agentColor: 'var(--tui-agent-claude)',
        meta: 'td-847b0c · working',
        age: '4m',
        selected: true,
      },
      {
        glyph: '●',
        state: 'live',
        name: 'wheel boundaries',
        agent: 'codex',
        agentColor: 'var(--tui-agent-codex)',
        meta: 'td-bcfe53 · working',
        age: '22m',
      },
      {
        glyph: '◆',
        state: 'blocked',
        name: 'focus geometry',
        agent: 'claude',
        agentColor: 'var(--tui-agent-claude)',
        meta: 'td-0818ef · needs you',
        age: '1h',
      },
    ],
  },
  {
    project: 'td',
    hue: 'var(--tui-project-2)',
    rows: [
      {
        glyph: '✓',
        state: 'done',
        name: 'notes public api',
        agent: 'cursor',
        agentColor: 'var(--tui-agent-cursor)',
        meta: 'PR #212 · ready to merge',
        age: '3h',
      },
    ],
  },
  {
    project: 'braid',
    hue: 'var(--tui-project-3)',
    rows: [
      {
        glyph: '○',
        state: 'idle',
        name: 'main',
        agent: 'shell',
        agentColor: 'var(--tui-idle)',
        meta: 'no agent',
        age: '2d',
      },
    ],
  },
];

const STATE_COLOR = {
  live: 'var(--tui-success)',
  blocked: 'var(--tui-warning)',
  done: 'var(--tui-done)',
  idle: 'var(--tui-idle)',
};

export function SessionsScreen() {
  return (
    <TuiPanes>
      <TuiPane
        title="Workspaces"
        chips={['⇅ Activity', '+']}
        focused
        grow={0}
        basis="38%">
        {SESSION_GROUPS.map((group) => (
          <React.Fragment key={group.project}>
            <Row>
              <span style={{color: group.hue}}>▌</span>
              <span className={tui.bright}>{group.project}</span>
              <span className={tui.subtle}>({group.rows.length})</span>
              <Spacer />
              <span className={tui.subtle}>+</span>
            </Row>
            {group.rows.map((r) => (
              <React.Fragment key={r.name}>
                <Row selected={r.selected}>
                  <span
                    className={r.state === 'live' ? tui.live : undefined}
                    style={{color: STATE_COLOR[r.state]}}>
                    {r.glyph}
                  </span>
                  <span className={tui.truncate}>{r.name}</span>
                  <Spacer />
                  <span className={tui.subtle}>{r.age}</span>
                </Row>
                <Row selected={r.selected}>
                  <span style={{paddingLeft: '2ch', color: r.agentColor}}>
                    ◆ {r.agent}
                  </span>
                  <span className={tui.subtle}>{r.meta}</span>
                </Row>
              </React.Fragment>
            ))}
            <Row>&nbsp;</Row>
          </React.Fragment>
        ))}
      </TuiPane>
      <TuiHandle />
      <TuiPane title="preview tabs" titleDim="sidecar · claude" chips={['Diff']}>
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.gold}>td-847b0c</span>
          <span className={tui.bright}>
            Overview: drop refreshing flash; show agent icons on kanban cards
          </span>
        </Row>
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.dim}>~/code/sidecar-preview-tabs</span>
          <span className={tui.subtle}>·</span>
          <span className={tui.teal}>feat/preview-tabs</span>
        </Row>
        <Rule />
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.dim}>› Reading</span>
          <span>internal/overview/preview_tabs.go</span>
        </Row>
        <Row>
          <span className={tui.dim}>› Edited</span>
          <span>internal/overview/preview.go</span>
          <span className={tui.green}>+34</span>
          <span className={tui.red}>-11</span>
        </Row>
        <Row>
          <span className={tui.dim}>› Ran</span>
          <span>go test ./internal/overview/</span>
          <span className={tui.green}>ok 2.4s</span>
        </Row>
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.green}>●</span>
          <span>Now wiring the agent icon into the card renderer.</span>
        </Row>
        <Row>
          <span className={tui.dim}>❯</span>
          <Cursor />
        </Row>
      </TuiPane>
    </TuiPanes>
  );
}

export const SESSIONS_KEYS = [
  ['n', 'New'],
  ['ctrl+n', 'Shell'],
  ['V', 'Kanban'],
  ['d', 'Diff'],
  ['T', 'Task'],
  ['R', 'Rename'],
  ['/', 'Filter'],
  ['8/9', 'global'],
];

/* -------------------------------------------------------------- Activity */

const LANES = [
  {
    key: 'working',
    glyph: '●',
    label: 'Working',
    color: 'var(--tui-success)',
    cards: [
      {
        project: 'sidecar',
        hue: 'var(--tui-project-1)',
        title: 'preview tabs',
        agent: 'claude',
        meta: 'td-847b0c · 4m',
      },
      {
        project: 'sidecar',
        hue: 'var(--tui-project-1)',
        title: 'wheel boundaries',
        agent: 'codex',
        meta: 'td-bcfe53 · 22m',
      },
      {
        project: 'clara-home',
        hue: 'var(--tui-project-4)',
        title: 'adapter seam',
        agent: 'gemini',
        meta: 'td-19cc02 · 38m',
      },
    ],
  },
  {
    key: 'blocked',
    glyph: '◆',
    label: 'Blocked',
    color: 'var(--tui-warning)',
    cards: [
      {
        project: 'sidecar',
        hue: 'var(--tui-project-1)',
        title: 'focus geometry',
        agent: 'claude',
        meta: 'waiting on you · 1h',
      },
    ],
  },
  {
    key: 'done',
    glyph: '✓',
    label: 'Done',
    color: 'var(--tui-done)',
    cards: [
      {
        project: 'td',
        hue: 'var(--tui-project-2)',
        title: 'notes public api',
        agent: 'cursor',
        meta: 'PR #212 · 3h',
      },
    ],
  },
  {
    key: 'idle',
    glyph: '○',
    label: 'Idle',
    color: 'var(--tui-idle)',
    cards: [
      {
        project: 'braid',
        hue: 'var(--tui-project-3)',
        title: 'main',
        agent: 'shell',
        meta: '2d',
      },
    ],
  },
  {
    key: 'paused',
    glyph: '⏸',
    label: 'Paused',
    color: 'var(--tui-paused)',
    cards: [
      {
        project: 'td',
        hue: 'var(--tui-project-2)',
        title: 'db repair',
        agent: 'claude',
        meta: 'td-95b98a · 1d',
      },
    ],
  },
];

export function ActivityScreen() {
  const agents = LANES.reduce((n, l) => n + l.cards.length, 0);
  return (
    <TuiPanes>
      <TuiPane
        title="Agent Overview"
        focused
        chips={[`4 projects · ${agents} agents`]}>
        <div className={tui.lanes}>
          {LANES.map((lane) => (
            <div key={lane.key} className={tui.lane}>
              <div className={tui.laneHead}>
                <span style={{color: lane.color}}>{lane.glyph}</span>
                <span className={tui.bright}>{lane.label}</span>
                <span className={tui.subtle}>{lane.cards.length}</span>
              </div>
              {lane.cards.map((card) => (
                <div
                  key={card.project + card.title}
                  className={tui.card}
                  style={{'--card-accent': lane.color}}>
                  <div className={clsx(tui.row, tui.truncate)}>
                    <span style={{color: card.hue}}>{card.project}</span>
                    <span className={tui.subtle}>·</span>
                    <span className={tui.dim}>{card.agent}</span>
                  </div>
                  <div className={clsx(tui.row, tui.truncate)}>
                    <span className={tui.bright}>{card.title}</span>
                  </div>
                  <div className={clsx(tui.row, tui.truncate)}>
                    <span className={tui.subtle}>{card.meta}</span>
                  </div>
                </div>
              ))}
            </div>
          ))}
        </div>
      </TuiPane>
    </TuiPanes>
  );
}

export const ACTIVITY_KEYS = [
  ['h/l', 'Lane'],
  ['j/k', 'Card'],
  ['enter', 'Open'],
  ['V', 'List'],
  ['a', 'Agent'],
  ['r', 'Refresh'],
  ['8/9', 'global'],
];

/* ----------------------------------------------------------------- Tasks */

const TASK_LISTS = [
  {name: 'Today', count: 5, selected: true},
  {name: 'This week', count: 12},
  {name: 'Sidecar launch', count: 8},
  {name: 'House', count: 3},
  {name: 'Someday', count: 41},
];

const TASKS = [
  {
    done: true,
    p: 'P1',
    title: 'Cut v1.0 release notes',
    tag: '@launch',
    due: 'today',
  },
  {
    p: 'P1',
    title: 'Reply to the Homebrew tap PR',
    tag: '@launch',
    due: 'today',
    selected: true,
  },
  {p: 'P2', title: 'Record the 90-second demo', tag: '@launch', due: 'tomorrow'},
  {p: 'P2', title: 'Book the dentist', tag: '@house', due: 'Fri'},
  {p: 'P3', title: 'Read the Bubble Tea v2 changelog', tag: '@reading'},
  {p: 'P3', title: 'Move the standing desk cable run', tag: '@house'},
];

const TASK_AGENDA = [
  ['09:30', 'Standup', 'var(--tui-info)'],
  ['13:00', 'Launch review with Nina', 'var(--tui-primary)'],
  ['16:30', 'Collect the spare keys', 'var(--tui-idle)'],
];

const P_COLOR = {P1: 'var(--tui-error)', P2: 'var(--tui-warning)', P3: 'var(--tui-idle)'};

export function TasksScreen() {
  return (
    <TuiPanes>
      <TuiPane title="Tasks" titleDim="beta" grow={0} basis="26%">
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.subtle}>LISTS</span>
        </Row>
        {TASK_LISTS.map((l) => (
          <Row key={l.name} selected={l.selected}>
            <span className={l.selected ? tui.gold : tui.dim}>
              {l.selected ? '❯' : ' '}
            </span>
            <span className={l.selected ? tui.bright : undefined}>{l.name}</span>
            <Spacer />
            <span className={tui.subtle}>{l.count}</span>
          </Row>
        ))}
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.subtle}>VIEWS</span>
        </Row>
        <Row>
          <span className={tui.dim}> </span>
          <span>Board</span>
        </Row>
        <Row>
          <span className={tui.dim}> </span>
          <span>Journal</span>
        </Row>
        <Row>
          <span className={tui.dim}> </span>
          <span>Agenda</span>
        </Row>
      </TuiPane>
      <TuiHandle />
      <TuiPane title="Today" titleDim="5 open · 1 done" focused chips={['+ Add']}>
        <Row>&nbsp;</Row>
        {TASKS.map((t) => (
          <Row key={t.title} selected={t.selected}>
            <span className={t.done ? tui.green : tui.dim}>
              {t.done ? '✓' : '○'}
            </span>
            <span style={{color: P_COLOR[t.p]}}>{t.p}</span>
            <span
              className={t.done ? tui.subtle : tui.bright}
              style={t.done ? {textDecoration: 'line-through'} : undefined}>
              {t.title}
            </span>
            <span className={tui.teal}>{t.tag}</span>
            <Spacer />
            <span className={t.due === 'today' ? tui.gold : tui.subtle}>
              {t.due || ''}
            </span>
          </Row>
        ))}
        <Row>&nbsp;</Row>
        <Rule />
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.subtle}>AGENDA</span>
        </Row>
        {TASK_AGENDA.map(([time, what, color]) => (
          <Row key={time}>
            <span style={{color}}>{time}</span>
            <span className={tui.dim}>{what}</span>
          </Row>
        ))}
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.subtle}>HANDED TO AGENTS</span>
        </Row>
        <Row>
          <span className={tui.purple}>[REV]</span>
          <span className={tui.gold}>td-847b0c</span>
          <span className={tui.dim}>Overview: agent icons on kanban cards</span>
          <Spacer />
          <span className={tui.subtle}>claude</span>
        </Row>
        <Row>
          <span className={tui.teal}>[WIP]</span>
          <span className={tui.gold}>td-bcfe53</span>
          <span className={tui.dim}>Inertial wheel boundary coverage</span>
          <Spacer />
          <span className={tui.subtle}>codex</span>
        </Row>
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.dim}>
            Press `a` on any task to hand it over — it becomes a td issue, and
          </span>
        </Row>
        <Row>
          <span className={tui.dim}>
            comes back to this list when the review lands.
          </span>
        </Row>
      </TuiPane>
    </TuiPanes>
  );
}

export const TASKS_KEYS = [
  ['a', 'Agent'],
  ['n', 'New'],
  ['space', 'Done'],
  ['t', 'Tag'],
  ['d', 'Due'],
  ['b', 'Board'],
  ['/', 'Filter'],
  ['8/9', 'global'],
];

/* -------------------------------------------------------------------- td */

const TD_IN_PROGRESS = [
  {
    glyph: '■',
    id: 'td-f8950c',
    p: 'P1',
    title: 'Files pane: shared file finder + project search',
    ses: 'ses_81db2d',
  },
  {
    glyph: '■',
    id: 'td-bcfe53',
    p: 'P1',
    title: 'Inventory and plan inertial wheel boundary coverage',
    ses: 'ses_a25f68',
  },
  {
    glyph: '✗',
    id: 'td-51d81c',
    p: 'P1',
    title: 'Keep project Workspaces in sync with global worktree creation',
    ses: 'ses_367351',
  },
];

const TD_REVIEWABLE = [
  {
    glyph: '■',
    id: 'td-847b0c',
    p: 'P1',
    title: 'Overview: drop refreshing flash; show agent icons on kanban cards',
  },
  {
    glyph: '✗',
    id: 'td-0818ef',
    p: 'P1',
    title: 'Fix intermittent workspace terminal focus geometry drift',
  },
  {
    glyph: '✗',
    id: 'td-3ca6f1',
    p: 'P1',
    title: 'Show live agents from untyped shell manifests in Overview',
  },
  {
    glyph: '✗',
    id: 'td-8492c5',
    p: 'P1',
    title: 'Fix HistorySnapshot.Output race with appendLoadedHistory',
  },
  {
    glyph: '✗',
    id: 'td-3615a6',
    p: 'P2',
    title: 'Make screenmodel import guard independent of local go.work',
  },
];

export function TdScreen() {
  return (
    <div style={{display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0}}>
      <TuiPanes>
        <TuiPane title=" CURRENT WORK" focused>
          <Row>&nbsp;</Row>
          <Row>
            <span className={tui.subtle}>IN PROGRESS:</span>
          </Row>
          {TD_IN_PROGRESS.map((t) => (
            <Row key={t.id}>
              <span className={t.glyph === '✗' ? tui.red : tui.teal}>
                {t.glyph}
              </span>
              <span className={tui.gold}>{t.id}</span>
              <span className={tui.red}>{t.p}</span>
              <span className={tui.truncate}>{t.title}</span>
              <span className={tui.subtle}>({t.ses})</span>
            </Row>
          ))}
        </TuiPane>
      </TuiPanes>
      <TuiPanes>
        <TuiPane title=" BOARD:" titleDim="All Issues [swimlanes] (1-17 of 98)">
          <Row>
            <span className={tui.gold}>★</span>
            <span className={tui.subtle}>REVIEWABLE (5):</span>
          </Row>
          {TD_REVIEWABLE.map((t) => (
            <Row key={t.id}>
              <span className={tui.purple}>[REV]</span>
              <span className={t.glyph === '✗' ? tui.red : tui.teal}>
                {t.glyph}
              </span>
              <span className={tui.gold}>{t.id}</span>
              <span className={t.p === 'P1' ? tui.red : tui.gold}>{t.p}</span>
              <span className={tui.truncate}>{t.title}</span>
            </Row>
          ))}
        </TuiPane>
      </TuiPanes>
    </div>
  );
}

export const TD_KEYS = [
  ['r', 'Review'],
  ['enter', 'Open'],
  ['s', 'Status'],
  ['/', 'Filter'],
  ['g/G', 'Top/End'],
  ['1-4', 'plugins'],
  ['8/9', 'global'],
];

/* ------------------------------------------------------------------- Git */

const COMMITS = [
  ['7fcd6ab', 'feat: route Notes plugin through td/pkg/notes', true],
  ['ad9893f', 'layout: drop the blank row between header and panes'],
  ['7d308fb', 'theme: unify theme system around Sidecar Modern'],
  ['d664d68', 'fix: open notes db with modernc and take td’s write lock'],
  ['0e29c1e', 'Merge pull request #286 from marcus/pre-launch-polish'],
  ['f0cbd92', 'build: integrate golangci-lint into pre-commit'],
  ['d73cba6', 'fix: resolve staticcheck, ineffassign, and errcheck debt'],
  ['67bccb4', 'fix: refresh worktree status when starting a shell'],
];

export function GitScreen() {
  return (
    <TuiPanes>
      <TuiPane title="Git" titleDim="marketing-site" focused grow={0} basis="32%">
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.subtle}>STAGED (2)</span>
        </Row>
        <Row>
          <span className={tui.green}>A</span>
          <span>src/components/Tui/index.js</span>
        </Row>
        <Row>
          <span className={tui.gold}>M</span>
          <span>src/pages/index.js</span>
        </Row>
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.subtle}>UNSTAGED (1)</span>
        </Row>
        <Row selected>
          <span className={tui.gold}>M</span>
          <span>src/css/custom.css</span>
        </Row>
        <Row>&nbsp;</Row>
        <Rule />
        <Row>
          <span className={tui.dim}>Recent Commits (1933)</span>
          <Spacer />
          <span className={tui.subtle}>no upstream</span>
        </Row>
        {COMMITS.map(([sha, msg]) => (
          <Row key={sha}>
            <span className={tui.gold}>↑</span>
            <span className={tui.teal}>{sha}</span>
            <span className={tui.truncate}>{msg}</span>
          </Row>
        ))}
      </TuiPane>
      <TuiHandle />
      <TuiPane title="Commit" titleDim="7fcd6ab9">
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.dim}>󰀄</span>
          <span>Marcus Vorwaller</span>
          <span className={tui.subtle}>·</span>
          <span className={tui.dim}>9 mins ago</span>
        </Row>
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.bright}>
            feat: route Notes plugin through td/pkg/notes (td-1b4683)
          </span>
        </Row>
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.dim}>
            Sidecar no longer speaks SQL or takes db.lock on issues.db. The notes
          </span>
        </Row>
        <Row>
          <span className={tui.dim}>
            store is a thin adapter over td’s public API.
          </span>
        </Row>
        <Rule />
        <Row>
          <span className={tui.dim}>Files (5)</span>
          <span className={tui.green}>+155</span>
          <span className={tui.red}>-649</span>
        </Row>
        <Row>&nbsp;</Row>
        <div className={tui.add}>
          + func (s *Store) List(ctx context.Context) ([]notes.Note, error) {'{'}
        </div>
        <div className={tui.add}>+ 	return s.client.Notes(ctx, s.project)</div>
        <div className={tui.add}>+ {'}'}</div>
        <div className={tui.del}>- 	db, err := sql.Open("sqlite", path)</div>
        <div className={tui.del}>- 	if err != nil {'{'}</div>
        <div className={tui.del}>- 		return nil, fmt.Errorf("open notes db: %w", err)</div>
        <div className={tui.del}>- 	{'}'}</div>
      </TuiPane>
    </TuiPanes>
  );
}

export const GIT_KEYS = [
  ['s', 'Stage'],
  ['u', 'Unstage'],
  ['c', 'Commit'],
  ['d', 'Diff'],
  ['v', 'Split'],
  ['b', 'Blame'],
  ['1-4', 'plugins'],
  ['8/9', 'global'],
];

/* ----------------------------------------------------------------- Files */

const TREE = [
  ['+', 'cmd', 'dir'],
  ['+', 'docs', 'dir'],
  ['−', 'internal', 'dir'],
  ['', 'overview', 'dir', 1],
  ['', 'paneframe', 'dir', 1],
  ['', 'panelayout', 'dir', 1],
  ['', 'styles', 'dir', 1],
  ['', 'themes.go', 'file', 2, true],
  ['', 'curated_themes.go', 'file', 2],
  ['+', 'website', 'dir'],
  ['', 'AGENTS.md', 'file'],
  ['', 'go.mod', 'file'],
  ['', 'Makefile', 'file'],
];

export function FilesScreen() {
  return (
    <TuiPanes>
      <TuiPane title="Files" titleDim="[name]" grow={0} basis="30%">
        <Row>&nbsp;</Row>
        {TREE.map(([mark, name, kind, depth = 0, selected], i) => (
          <Row key={name + i} selected={selected}>
            <span className={tui.dim} style={{paddingLeft: `${depth * 2}ch`}}>
              {mark || ' '}
            </span>
            <span className={kind === 'dir' ? tui.teal : undefined}>{name}</span>
          </Row>
        ))}
      </TuiPane>
      <TuiHandle />
      <TuiPane title="themes.go" titleDim="internal/styles" focused chips={['×']}>
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.subtle}>134</span>
          <span className={tui.dim}>{'//'} SidecarModernTheme is the launch theme.</span>
        </Row>
        <Row>
          <span className={tui.subtle}>135</span>
          <span className={tui.purple}>var</span>
          <span className={tui.teal}>SidecarModernTheme</span>
          <span>= Theme{'{'}</span>
        </Row>
        <Row>
          <span className={tui.subtle}>136</span>
          <span style={{paddingLeft: '2ch'}} className={tui.teal}>
            Name:
          </span>
          <span className={tui.green}>"sidecar-modern"</span>,
        </Row>
        <Row>
          <span className={tui.subtle}>137</span>
          <span style={{paddingLeft: '2ch'}} className={tui.teal}>
            DisplayName:
          </span>
          <span className={tui.green}>"Sidecar Modern"</span>,
        </Row>
        <Row>
          <span className={tui.subtle}>138</span>
          <span style={{paddingLeft: '2ch'}} className={tui.teal}>
            Colors:
          </span>
          <span>ColorPalette{'{'}</span>
        </Row>
        <Row>
          <span className={tui.subtle}>139</span>
          <span style={{paddingLeft: '4ch'}} className={tui.teal}>
            Primary:
          </span>
          <span className={tui.green}>"#c0982f"</span>,
          <span className={tui.dim}>{'//'} gold — cursor, active tab</span>
        </Row>
        <Row>
          <span className={tui.subtle}>140</span>
          <span style={{paddingLeft: '4ch'}} className={tui.teal}>
            Secondary:
          </span>
          <span className={tui.green}>"#4a8f8f"</span>,
          <span className={tui.dim}>{'//'} teal — identifiers</span>
        </Row>
        <Row>
          <span className={tui.subtle}>141</span>
          <span style={{paddingLeft: '4ch'}} className={tui.teal}>
            Accent:
          </span>
          <span className={tui.green}>"#c0982f"</span>,
        </Row>
        <Row>
          <span className={tui.subtle}>142</span>
        </Row>
        <Row>
          <span className={tui.subtle}>143</span>
          <span style={{paddingLeft: '4ch'}} className={tui.dim}>
            {'//'} Status colors
          </span>
        </Row>
        <Row>
          <span className={tui.subtle}>144</span>
          <span style={{paddingLeft: '4ch'}} className={tui.teal}>
            Success:
          </span>
          <span className={tui.green}>"#5b8f63"</span>,
        </Row>
      </TuiPane>
    </TuiPanes>
  );
}

export const FILES_KEYS = [
  ['enter', 'Open'],
  ['ctrl+p', 'Find'],
  ['f', 'Search'],
  ['e', 'Edit'],
  ['\\', 'Sidebar'],
  ['w', 'Wrap'],
  ['1-4', 'plugins'],
  ['8/9', 'global'],
];

/* ------------------------------------------------------------ Workspaces */

export function WorkspacesScreen() {
  return (
    <TuiPanes>
      <TuiPane title="Workspaces" chips={['⇅ Manual', '+']} grow={0} basis="24%">
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.subtle}>Shells (2)</span>
          <Spacer />
          <span className={tui.subtle}>+</span>
        </Row>
        <Row selected>
          <span className={clsx(tui.green, tui.live)}>◎</span>
          <span className={tui.gold}>❯</span>
          <span>claude · preview tabs</span>
          <Spacer />
          <span className={tui.subtle}>4m</span>
        </Row>
        <Row>
          <span className={tui.dim}>◎</span>
          <span className={tui.dim}>❯</span>
          <span className={tui.dim}>Shell 2</span>
          <Spacer />
          <span className={tui.subtle}>1h</span>
        </Row>
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.subtle}>Worktrees (3)</span>
          <Spacer />
          <span className={tui.subtle}>+</span>
        </Row>
        <Row>
          <span className={tui.dim}>⏸</span>
          <span className={tui.dim}>⑂</span>
          <span>sidecar</span>
          <Spacer />
          <span className={tui.subtle}>2h</span>
        </Row>
        <Row>
          <span className={tui.subtle} style={{paddingLeft: '4ch'}}>
            main
          </span>
        </Row>
        <Row>
          <span className={tui.green}>●</span>
          <span className={tui.dim}>⑂</span>
          <span>preview-tabs</span>
          <Spacer />
          <span className={tui.subtle}>4m</span>
        </Row>
        <Row>
          <span className={tui.subtle} style={{paddingLeft: '4ch'}}>
            feat/preview-tabs
          </span>
        </Row>
        <Row>
          <span className={tui.dim}>⏸</span>
          <span className={tui.dim}>⑂</span>
          <span>marketing-site</span>
          <Spacer />
          <span className={tui.subtle}>9m</span>
        </Row>
      </TuiPane>
      <TuiHandle />
      <TuiPane title="claude" titleDim="preview tabs" focused chips={['Diff']}>
        <Row>
          <span className={tui.teal}>sidecar-preview-tabs</span>
          <span className={tui.gold}>feat/preview-tabs</span>
          <span className={tui.dim}>*❯</span>
        </Row>
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.dim}>›</span>
          <span>Reading internal/overview/preview_tabs.go</span>
        </Row>
        <Row>
          <span className={tui.dim}>›</span>
          <span>Edited internal/overview/preview.go</span>
        </Row>
        <Row>&nbsp;</Row>
        <Row>
          <span>Here’s the tab strip — take a look at the fallback:</span>
        </Row>
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.dim}>❯</span>
          <span className={tui.green}>sidecar open</span>
          <span>internal/overview/preview_tabs.go:88</span>
        </Row>
        <Row>
          <span className={tui.subtle}>opened in a new pane →</span>
        </Row>
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.dim}>❯</span>
          <Cursor />
        </Row>
      </TuiPane>
      <TuiHandle />
      <TuiPane title="preview_tabs.go" chips={['×']} grow={0} basis="30%">
        <Row>&nbsp;</Row>
        <Row>
          <span className={tui.subtle}>86</span>
          <span className={tui.dim}>{'}'}</span>
        </Row>
        <Row>
          <span className={tui.subtle}>87</span>
        </Row>
        <Row selected>
          <span className={tui.subtle}>88</span>
          <span className={tui.purple}>func</span>
          <span className={tui.teal}>(m Model) tabsFor</span>
          <span>(l Leaf) []Tab {'{'}</span>
        </Row>
        <Row>
          <span className={tui.subtle}>89</span>
          <span style={{paddingLeft: '2ch'}} className={tui.purple}>
            if
          </span>
          <span>l.Issue != "" {'{'}</span>
        </Row>
        <Row>
          <span className={tui.subtle}>90</span>
          <span style={{paddingLeft: '4ch'}} className={tui.purple}>
            return
          </span>
          <span>issueTabs</span>
        </Row>
        <Row>
          <span className={tui.subtle}>91</span>
          <span style={{paddingLeft: '2ch'}}>{'}'}</span>
        </Row>
        <Row>
          <span className={tui.subtle}>92</span>
          <span style={{paddingLeft: '2ch'}} className={tui.purple}>
            return
          </span>
          <span>docTabs</span>
        </Row>
        <Row>
          <span className={tui.subtle}>93</span>
          <span>{'}'}</span>
        </Row>
      </TuiPane>
    </TuiPanes>
  );
}

export const WORKSPACES_KEYS = [
  ['n', 'New'],
  ['ctrl+n', 'Shell'],
  ['a', 'Agent'],
  ['m', 'Merge'],
  ['x', 'Tab×'],
  ['+/-', 'Size'],
  ['tab', 'Focus'],
  ['8/9', 'global'],
];

/* ------------------------------------------------------------- Worktrees */

/*
 * Creating a worktree. One key opens this; Sidecar does the branch, the
 * directory, the task link and the agent launch behind it.
 */
export function WorktreesScreen() {
  return (
    <>
      <TuiPanes>
        <TuiPane title="Workspaces" chips={['⇅ Manual', '+']} grow={0} basis="40%">
          <Row>&nbsp;</Row>
          <Row>
            <span className={tui.subtle}>Worktrees (4)</span>
            <Spacer />
            <span className={tui.subtle}>+</span>
          </Row>
          <Row>
            <span className={tui.dim}>⏸</span>
            <span className={tui.dim}>⑂</span>
            <span>sidecar</span>
            <Spacer />
            <span className={tui.subtle}>2h</span>
          </Row>
          <Row>
            <span className={tui.subtle} style={{paddingLeft: '4ch'}}>
              main · main
            </span>
          </Row>
          <Row>
            <span className={clsx(tui.green, tui.live)}>●</span>
            <span className={tui.dim}>⑂</span>
            <span>preview-tabs</span>
            <Spacer />
            <span className={tui.subtle}>4m</span>
          </Row>
          <Row>
            <span className={tui.subtle} style={{paddingLeft: '4ch'}}>
              feat/preview-tabs · PR #291 ✓
            </span>
          </Row>
          <Row>
            <span className={tui.gold}>◆</span>
            <span className={tui.dim}>⑂</span>
            <span>focus-geometry</span>
            <Spacer />
            <span className={tui.subtle}>1h</span>
          </Row>
          <Row>
            <span className={tui.subtle} style={{paddingLeft: '4ch'}}>
              fix/focus-geometry · needs you
            </span>
          </Row>
          <Row>
            <span className={tui.dim}>⏸</span>
            <span className={tui.dim}>⑂</span>
            <span>marketing-site</span>
            <Spacer />
            <span className={tui.subtle}>9m</span>
          </Row>
        </TuiPane>
        <TuiHandle />
        <TuiPane title="preview-tabs" titleDim="feat/preview-tabs" focused>
          <Row>&nbsp;</Row>
          <Row>
            <span className={tui.dim}>PR</span>
            <span className={tui.purple}>#291</span>
            <span>Overview: agent icons on kanban cards</span>
          </Row>
          <Row>
            <span className={tui.dim}>Checks</span>
            <span className={tui.green}>✓ build</span>
            <span className={tui.green}>✓ test</span>
            <span className={tui.green}>✓ lint</span>
          </Row>
          <Row>
            <span className={tui.dim}>Task</span>
            <span className={tui.gold}>td-847b0c</span>
            <span className={tui.subtle}>reviewable</span>
          </Row>
          <Row>
            <span className={tui.dim}>Ahead</span>
            <span>6 commits</span>
            <span className={tui.subtle}>· clean tree</span>
          </Row>
        </TuiPane>
      </TuiPanes>
      <TuiModal
        title=" New Workspace "
        keys={[
          ['enter', 'Create'],
          ['tab', 'Next'],
          ['esc', 'Cancel'],
        ]}>
        <Field label="Name" value="focus-geometry" active />
        <Field label="Branch" value="fix/focus-geometry" />
        <Field label="Base" value="main" />
        <Field label="Agent" value="claude" hint="↑↓" />
        <Field label="Task" value="td-0818ef" hint="from td" />
      </TuiModal>
    </>
  );
}

export const WORKTREES_KEYS = [
  ['n', 'New'],
  ['D', 'Delete'],
  ['a', 'Agent'],
  ['t', 'Task'],
  ['m', 'Merge'],
  ['p', 'Push'],
  ['W', 'Switch'],
];
