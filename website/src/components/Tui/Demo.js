import React, {useCallback, useEffect, useRef, useState} from 'react';
import {TuiFooter, TuiHeader, TuiWindow} from './index';
import demo from './Demo.module.css';
import {
  ACTIVITY_KEYS,
  ActivityScreen,
  FILES_KEYS,
  FilesScreen,
  GIT_KEYS,
  GitScreen,
  SESSIONS_KEYS,
  SessionsScreen,
  TASKS_KEYS,
  TD_KEYS,
  TasksScreen,
  TdScreen,
  WORKSPACES_KEYS,
  WORKTREES_KEYS,
  WorkspacesScreen,
  WorktreesScreen,
} from './Screens';

/*
 * The hero demo. Its tabs are the app's own two scopes — the global one on the
 * left, the project one on the right — and clicking either switches the pane
 * content exactly as `8`, `9` or `1`-`4` would in the terminal.
 */

const GLOBAL_TABS = [
  {id: 'sessions', label: 'Sessions'},
  {id: 'activity', label: 'Activity'},
  {id: 'tasks', label: 'Tasks'},
];

const PROJECT_TABS = [
  {id: 'td', label: 'td'},
  {id: 'git', label: 'Git'},
  {id: 'files', label: 'Files'},
  {id: 'workspaces', label: 'Workspaces'},
];

const SCREENS = {
  sessions: {render: SessionsScreen, keys: SESSIONS_KEYS, scope: 'global'},
  activity: {render: ActivityScreen, keys: ACTIVITY_KEYS, scope: 'global'},
  tasks: {render: TasksScreen, keys: TASKS_KEYS, scope: 'global'},
  td: {render: TdScreen, keys: TD_KEYS, scope: 'project'},
  git: {render: GitScreen, keys: GIT_KEYS, scope: 'project'},
  files: {render: FilesScreen, keys: FILES_KEYS, scope: 'project'},
  workspaces: {render: WorkspacesScreen, keys: WORKSPACES_KEYS, scope: 'project'},
  // Not a tab of its own — the Workspaces tab with the create modal open.
  worktrees: {
    render: WorktreesScreen,
    keys: WORKTREES_KEYS,
    scope: 'project',
    tab: 'workspaces',
  },
};

const ORDER = [...GLOBAL_TABS, ...PROJECT_TABS].map((t) => t.id);

const CAPTIONS = {
  sessions:
    'Every agent you have running, in every repo, on one screen. This is where most days start.',
  activity:
    'The same fleet as a board. Blocked means an agent is waiting on you — that lane is the whole point.',
  tasks:
    'Tasks is your own list, in the same window. Hand one to an agent and it comes back as a review.',
  td: 'td is the agents’ tracker: what they picked up, what they logged, what is waiting on a review.',
  git: 'Staging, history and diffs without reaching for an IDE.',
  files: 'The tree and a syntax-highlighted preview, editable in place.',
  workspaces:
    'A live agent shell with the file it just asked you to look at, opened beside it.',
};

export default function SidecarDemo({theme = 'sidecar-modern', clock = '21:02'}) {
  const [active, setActive] = useState('sessions');
  const [touched, setTouched] = useState(false);
  const ref = useRef(null);
  const screen = SCREENS[active];
  const Screen = screen.render;

  const select = useCallback((id) => {
    setActive(id);
    setTouched(true);
  }, []);

  // Arrow keys walk the tab strip once the demo has focus, the same left-to-
  // right order the app's tab/shift-tab cycle uses.
  const onKeyDown = useCallback(
    (e) => {
      if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
      e.preventDefault();
      const i = ORDER.indexOf(active);
      const next = e.key === 'ArrowRight' ? i + 1 : i - 1 + ORDER.length;
      select(ORDER[next % ORDER.length]);
    },
    [active, select],
  );

  // Before anyone touches it the demo shows that it is live by walking one
  // step from Sessions to Activity, then stops and waits.
  useEffect(() => {
    if (touched) return undefined;
    const el = ref.current;
    if (!el || typeof IntersectionObserver === 'undefined') return undefined;
    let timer;
    const io = new IntersectionObserver(
      ([entry]) => {
        if (!entry.isIntersecting || timer) return;
        timer = setTimeout(() => {
          setActive((cur) => (cur === 'sessions' ? 'activity' : cur));
        }, 2600);
      },
      {threshold: 0.5},
    );
    io.observe(el);
    return () => {
      io.disconnect();
      clearTimeout(timer);
    };
  }, [touched]);

  return (
    <figure className={demo.figure} ref={ref}>
      <div className={demo.pan}>
        <TuiWindow
          theme={theme}
          titlebar={{
            label: 'sidecar — ~/code/sidecar',
            right: (
              <span>{screen.scope === 'global' ? 'global' : 'sidecar'}</span>
            ),
          }}
          style={{height: 'clamp(400px, 42vw, 520px)'}}
          onKeyDown={onKeyDown}>
          <TuiHeader
            globalTabs={GLOBAL_TABS}
            projectTabs={PROJECT_TABS}
            active={active}
            onSelect={select}
            project="sidecar [main]"
          />
          <Screen key={active} />
          <TuiFooter keys={screen.keys} clock={clock} />
        </TuiWindow>
      </div>
      <figcaption className={demo.caption}>
        <span className={demo.hint} aria-hidden="true">
          click a tab
        </span>
        <span key={active} className={demo.text}>
          {CAPTIONS[active]}
        </span>
      </figcaption>
    </figure>
  );
}

/**
 * A non-interactive mockup for the feature sections: same chrome, one screen,
 * no tab affordance so it does not compete with the hero demo.
 */
export function SidecarStill({
  screen,
  theme = 'sidecar-modern',
  project = 'sidecar [main]',
  height = 'clamp(300px, 32vw, 380px)',
}) {
  const entry = SCREENS[screen];
  const Screen = entry.render;
  return (
    <TuiWindow theme={theme} style={{height}}>
      <TuiHeader
        globalTabs={GLOBAL_TABS}
        projectTabs={PROJECT_TABS}
        active={entry.tab || screen}
        project={project}
      />
      <Screen />
      <TuiFooter keys={entry.keys.slice(0, 6)} clock="21:02" />
    </TuiWindow>
  );
}

export {SCREENS, GLOBAL_TABS, PROJECT_TABS};
