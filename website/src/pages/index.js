import { useState, useCallback, useEffect } from 'react';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';

const TABS = ['overview', 'td', 'git', 'files', 'notes*', 'conversations', 'workspaces'];

const MINI_FEATURES = [
  { icon: 'kanban', title: 'Agent Overview', description: 'Cross-project dashboard tracking agents, tasks, and running tmux workspaces across repositories.' },
  { icon: 'sticky-note', title: 'Developer Notes*', description: 'In-TUI scratchpads with task links and vim editing. *Beta: requires notes_plugin feature flag.' },
  { icon: 'command', title: 'Command Palette', description: 'Quick access to all commands with fuzzy search. Press Ctrl+P to open.' },
  { icon: 'folder-kanban', title: 'Project Switcher', description: 'Switch back and forth between projects instantly. State persists per-project—cursor, plugin, and preferences restore automatically.' },
  { icon: 'columns-2', title: 'Drag-to-Resize Panes', description: 'View two plugins side by side with interactive mouse drag pane resizing.' },
  { icon: 'activity', title: 'Diagnostics Overlay', description: 'Real-time metrics on memory, goroutines, and render performance. Toggle with F12.' },
  { icon: 'search', title: 'Fuzzy File Finder', description: 'Find any file by typing part of its name. Respects .gitignore patterns.' },
  { icon: 'file-search', title: 'Ripgrep Search', description: 'Search file contents across your entire codebase. Results update as you type.' },
  { icon: 'git-branch', title: 'Git Graph', description: 'Visual branch history showing merges, rebases, and commit relationships.' },
  { icon: 'trello', title: 'Kanban Workspaces', description: 'Organize workspaces into columns by status: active, paused, ready to merge.' },
  { icon: 'link', title: 'Task Linking', description: 'Connect td tasks to git branches and PRs. Track work across tools automatically.' },
  { icon: 'external-link', title: 'External Editor', description: 'Open any file in your $EDITOR with a single keypress. Returns you to Sidecar when done.' },
  { icon: 'clipboard', title: 'System Clipboard', description: 'Copy file paths, diffs, commit hashes, and more directly to your clipboard.' },
  { icon: 'move', title: 'Vim Navigation', description: 'h/j/k/l, gg/G, Ctrl+d/u, and more. Navigate like you would in vim.' },
  { icon: 'git-merge', title: 'Merge Workflow', description: 'Merge PRs, delete branches, and clean up workspaces with guided prompts.' },
  { icon: 'git-fork', title: 'Worktree Switcher', description: 'Switch back and forth between git worktrees instantly. Per-worktree state restores automatically.' },
  { icon: 'refresh-cw', title: 'Global Refresh', description: 'Press R to refresh all plugins at once. Git status, files, and tasks update together.' },
  { icon: 'sun', title: 'High-Contrast Themes', description: 'Built-in WCAG contrast engine and community theme gallery with live previews.' },
  { icon: 'terminal', title: 'Live PTY Streaming', description: 'Passive terminal follow grid and interactive PTY backend for tmux sessions.' },
  { icon: 'square-pen', title: 'Inline Preview Editor', description: 'Edit files with vim or nvim directly in the preview pane. File tree stays visible for context.' },
];

// For a cleaner install command, consider setting up a custom domain:
// - Buy sidecar.dev (or similar) and set up a redirect
// - Example: curl -fsSL sidecar.dev/install.sh | sh
// - Or: curl -fsSL get.sidecar.dev | sh
// Popular projects use: sh.rustup.rs, get.docker.com, brew.sh, etc.
const INSTALL_COMMAND = 'curl -fsSL https://raw.githubusercontent.com/marcus/sidecar/main/scripts/setup.sh | bash';
const BREW_COMMAND = 'brew install marcus/tap/sidecar';

function CopyButton({ text }) {
  const [copied, setCopied] = useState(false);
  const [sparkles, setSparkles] = useState([]);
  const btnRef = useCallback(node => { if (node) node.__btnRef = node; }, []);

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);

      // Generate sparkle particles
      const colors = [
        'var(--ifm-color-primary)',
        'var(--sc-blue)',
        'var(--sc-pink)',
        'var(--sc-purple)',
        'var(--sc-yellow)',
        'var(--sc-orange)',
      ];
      const newSparkles = Array.from({ length: 24 }, (_, i) => ({
        id: Date.now() + i,
        color: colors[Math.floor(Math.random() * colors.length)],
        angle: (i * 15) + (Math.random() * 10 - 5),
        distance: 40 + Math.random() * 60,
        size: 3 + Math.random() * 5,
        delay: Math.random() * 80,
        duration: 500 + Math.random() * 300,
      }));
      setSparkles(newSparkles);

      setTimeout(() => setCopied(false), 2000);
      setTimeout(() => setSparkles([]), 900);
    } catch (err) {
      console.error('Failed to copy:', err);
    }
  }, [text]);

  return (
    <div className="sc-copyBtnWrap">
      {sparkles.map(s => (
        <span
          key={s.id}
          className="sc-sparkle"
          style={{
            '--sparkle-angle': `${s.angle}deg`,
            '--sparkle-distance': `${s.distance}px`,
            '--sparkle-size': `${s.size}px`,
            '--sparkle-color': s.color,
            '--sparkle-delay': `${s.delay}ms`,
            '--sparkle-duration': `${s.duration}ms`,
          }}
        />
      ))}
      <button
        ref={btnRef}
        type="button"
        className="sc-copyBtn"
        onClick={handleCopy}
        aria-label={copied ? 'Copied' : 'Copy to clipboard'}
      >
        <i className={copied ? 'icon-check' : 'icon-copy'} />
      </button>
    </div>
  );
}

function OverviewPane() {
  return (
    <>
      <div className="sc-paneSidebar">
        <p className="sc-sectionTitle">Projects</p>
        <div className="sc-list">
          <div className="sc-item sc-itemActive">
            <span className="sc-bullet sc-bulletGreen" />
            <span>sidecar <span className="sc-lineYellow" style={{ fontSize: 9 }}>2 agents</span></span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletBlue" />
            <span>betamax <span className="sc-linePink" style={{ fontSize: 9 }}>1 agent</span></span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletPink" />
            <span>td <span className="sc-lineGreen" style={{ fontSize: 9 }}>active</span></span>
          </div>
        </div>
      </div>
      <div className="sc-paneMain">
        <div className="sc-codeBlock" role="img" aria-label="Cross-project Agent Overview Board">
          <div className="sc-lineDim">Agent Overview Board | <span className="sc-lineGreen">3 Projects · 4 Agents</span></div>
          <div style={{ height: 4 }} />
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 6, fontSize: 10 }}>
            <div style={{ background: 'rgba(0,0,0,0.3)', padding: 6, borderRadius: 4, border: '1px solid var(--sc-border)' }}>
              <div className="sc-lineGreen" style={{ fontSize: 9, fontWeight: 700, marginBottom: 4 }}>● WORKING</div>
              <div className="sc-lineYellow">sidecar / feature/auth</div>
              <div className="sc-lineDim" style={{ fontSize: 8 }}>Claude Code | td-a1b2c3</div>
            </div>
            <div style={{ background: 'rgba(0,0,0,0.3)', padding: 6, borderRadius: 4, border: '1px solid var(--sc-border)' }}>
              <div className="sc-linePink" style={{ fontSize: 9, fontWeight: 700, marginBottom: 4 }}>● NEEDS ATTENTION</div>
              <div className="sc-lineYellow">sidecar / fix/leak</div>
              <div className="sc-lineDim" style={{ fontSize: 8 }}>Cursor | blocked</div>
            </div>
            <div style={{ background: 'rgba(0,0,0,0.3)', padding: 6, borderRadius: 4, border: '1px solid var(--sc-border)' }}>
              <div className="sc-lineBlue" style={{ fontSize: 9, fontWeight: 700, marginBottom: 4 }}>● DONE</div>
              <div className="sc-lineYellow">td / refactor/cli</div>
              <div className="sc-lineDim" style={{ fontSize: 8 }}>Grok | PR #42 ready</div>
            </div>
          </div>
          <div style={{ height: 4 }} />
          <div className="sc-lineDim">Press @ to switch between projects instantly</div>
        </div>
      </div>
    </>
  );
}

function NotesPane() {
  return (
    <>
      <div className="sc-paneSidebar">
        <p className="sc-sectionTitle">Notes <span className="sc-chipBeta">notes_plugin</span></p>
        <div className="sc-list">
          <div className="sc-item sc-itemActive">
            <span className="sc-bullet sc-bulletYellow" />
            <span>auth-architecture.md</span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletBlue" />
            <span>migration-steps.md</span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletDim" />
            <span>prompt-ideas.md</span>
          </div>
        </div>
      </div>
      <div className="sc-paneMain">
        <div className="sc-codeBlock">
          <div className="sc-lineDim">auth-architecture.md | <span className="sc-lineYellow">Linked to td-a1b2c3</span></div>
          <div style={{ height: 4 }} />
          <div><span className="sc-linePink"># Auth Refactor Notes</span></div>
          <div><span className="sc-lineDim">Token validation middleware specs:</span></div>
          <div style={{ height: 2 }} />
          <div><span className="sc-lineBlue">- [x]</span> JWT validation function</div>
          <div><span className="sc-lineBlue">- [ ]</span> Refresh token rotation logic</div>
          <div style={{ height: 4 }} />
          <div className="sc-lineDim" style={{ fontSize: 9 }}>* Beta feature — enable with SIDECAR_FEATURE_NOTES_PLUGIN=1</div>
        </div>
      </div>
    </>
  );
}

function TdPane() {
  return (
    <>
      <div className="sc-tdSection">
        <p className="sc-sectionTitle">Current Work</p>
        <div className="sc-item sc-itemActive">
          <span className="sc-bullet sc-bulletGreen" />
          <span>td-a1b2c3 Implement auth flow</span>
        </div>
      </div>
      <div className="sc-tdSection">
        <p className="sc-sectionTitle">Board</p>
        <div className="sc-list">
          <div className="sc-item">
            <span className="sc-bullet sc-bulletBlue" />
            <span>td-d4e5f6 Add rate limiting</span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletPink" />
            <span>td-g7h8i9 Fix memory leak</span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletBlue" />
            <span>td-j0k1l2 Update API docs</span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletBlue" />
            <span>td-m3n4o5 Refactor css</span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletPink" />
            <span>td-p6q7r8 Update dependencies</span>
          </div>
        </div>
      </div>
      <div className="sc-tdSection">
        <p className="sc-sectionTitle">Activity</p>
        <div style={{ fontSize: 10 }}>
          <div><span className="sc-lineYellow">00:39</span> <span className="sc-lineDim">td-a1b2c3</span> Started</div>
          <div><span className="sc-lineYellow">00:15</span> <span className="sc-lineDim">td-d4e5f6</span> Created</div>
          <div><span className="sc-lineYellow">23:42</span> <span className="sc-lineDim">td-g7h8i9</span> Blocked</div>
        </div>
      </div>
    </>
  );
}

function GitPane() {
  return (
    <>
      <div className="sc-paneSidebar">
        <p className="sc-sectionTitle">Modified</p>
        <div className="sc-list">
          <div className="sc-item sc-itemActive">
            <span className="sc-lineGreen">M</span>
            <span>middleware.go</span>
          </div>
          <div className="sc-item">
            <span className="sc-lineGreen">A</span>
            <span>jwt.go</span>
          </div>
          <div className="sc-item">
            <span className="sc-lineGreen">M</span>
            <span>api_test.go</span>
          </div>
          <div className="sc-item">
            <span className="sc-linePink">D</span>
            <span>old_auth.go</span>
          </div>
          <div className="sc-item">
            <span className="sc-lineGreen">M</span>
            <span>README.md</span>
          </div>
        </div>
        <p className="sc-sectionTitle" style={{ marginTop: 10 }}>Commits</p>
        <div className="sc-list">
          <div className="sc-item">
            <span className="sc-lineDim">736a844</span>
            <span>Minor fixes</span>
          </div>
          <div className="sc-item">
            <span className="sc-lineDim">8b2c9d1</span>
            <span>Update deps</span>
          </div>
        </div>
      </div>
      <div className="sc-paneMain">
        <div className="sc-codeBlock">
          <div className="sc-lineDim">internal/auth/middleware.go</div>
          <div style={{ height: 4 }} />
          <div><span className="sc-lineBlue">@@ -42,6 +42,14 @@</span></div>
          <div><span className="sc-lineGreen">+func AuthMiddleware(next) {'{'}</span></div>
          <div><span className="sc-lineGreen">+  return http.HandlerFunc(w, r) {'{'}</span></div>
          <div><span className="sc-lineGreen">+    token := r.Header.Get("Auth")</span></div>
          <div><span className="sc-lineGreen">+    if !ValidateJWT(token) {'{'}</span></div>
          <div><span className="sc-lineGreen">+      http.Error(w, "Unauth", 401)</span></div>
          <div><span className="sc-lineGreen">+    {'}'}</span></div>
          <div><span className="sc-lineGreen">+  {'}'})</span></div>
          <div><span className="sc-lineGreen">+{'}'}</span></div>
        </div>
      </div>
    </>
  );
}

function FilesPane() {
  return (
    <>
      <div className="sc-paneSidebar">
        <p className="sc-sectionTitle">Files</p>
        <div className="sc-list">
          <div className="sc-item">
            <span className="sc-lineDim">[v]</span>
            <span>internal/</span>
          </div>
          <div className="sc-item" style={{ paddingLeft: 12 }}>
            <span className="sc-lineDim">[v]</span>
            <span>auth/</span>
          </div>
          <div className="sc-item sc-itemActive" style={{ paddingLeft: 20 }}>
            <span className="sc-bullet sc-bulletBlue" />
            <span>middleware.go</span>
          </div>
          <div className="sc-item" style={{ paddingLeft: 20 }}>
            <span className="sc-bullet sc-bulletBlue" />
            <span>jwt.go</span>
          </div>
          <div className="sc-item" style={{ paddingLeft: 20 }}>
            <span className="sc-bullet sc-bulletDim" />
            <span>utils.go</span>
          </div>
          <div className="sc-item" style={{ paddingLeft: 20 }}>
            <span className="sc-bullet sc-bulletDim" />
            <span>config.go</span>
          </div>
          <div className="sc-item" style={{ paddingLeft: 12 }}>
            <span className="sc-lineDim">[&gt;]</span>
            <span>plugins/</span>
          </div>
          <div className="sc-item" style={{ paddingLeft: 12 }}>
            <span className="sc-lineDim">[&gt;]</span>
            <span>app/</span>
          </div>
        </div>
      </div>
      <div className="sc-paneMain">
        <div className="sc-codeBlock">
          <div className="sc-lineDim">middleware.go | 156 lines</div>
          <div style={{ height: 4 }} />
          <div><span className="sc-lineBlue">package</span> auth</div>
          <div style={{ height: 2 }} />
          <div><span className="sc-lineBlue">import</span> (</div>
          <div>  <span className="sc-lineYellow">"net/http"</span></div>
          <div>  <span className="sc-lineYellow">"strings"</span></div>
          <div>)</div>
          <div style={{ height: 2 }} />
          <div><span className="sc-linePink">// AuthMiddleware validates requests</span></div>
          <div><span className="sc-lineBlue">func</span> AuthMiddleware(next)...</div>
        </div>
      </div>
    </>
  );
}

function ConversationsPane() {
  return (
    <>
      <div className="sc-paneSidebar">
        <p className="sc-sectionTitle">Sessions</p>
        <div className="sc-list">
          <div className="sc-item sc-itemActive">
            <span className="sc-bullet sc-bulletGreen" />
            <span>auth-flow <span className="sc-lineYellow" style={{ fontSize: 9 }}>Claude</span></span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletBlue" />
            <span>rate-limit <span className="sc-linePink" style={{ fontSize: 9 }}>Cursor</span></span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletPink" />
            <span>refactor <span className="sc-lineBlue" style={{ fontSize: 9 }}>Gemini</span></span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletBlue" />
            <span>api-docs <span className="sc-lineGreen" style={{ fontSize: 9 }}>Amp</span></span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletGreen" />
            <span>docs-refactor <span className="sc-lineBlue" style={{ fontSize: 9 }}>Kiro</span></span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletPink" />
            <span>deploy-fix <span className="sc-lineGreen" style={{ fontSize: 9 }}>Pi</span></span>
          </div>
        </div>
      </div>
      <div className="sc-paneMain">
        <div className="sc-codeBlock">
          <div className="sc-lineDim">auth-flow | <span className="sc-lineYellow">Claude Code</span></div>
          <div style={{ height: 4 }} />
          <div><span className="sc-lineBlue">User:</span> Add JWT auth to the API</div>
          <div style={{ height: 2 }} />
          <div><span className="sc-lineGreen">Claude:</span> I'll implement JWT auth.</div>
          <div className="sc-lineDim">Let me check the existing auth...</div>
          <div style={{ height: 2 }} />
          <div><span className="sc-lineYellow">-&gt;</span> Read middleware.go</div>
          <div><span className="sc-lineYellow">-&gt;</span> Edit jwt.go</div>
          <div><span className="sc-lineYellow">-&gt;</span> Read api_test.go</div>
          <div style={{ height: 2 }} />
          <div className="sc-lineDim">12.4k tokens | 24m</div>
        </div>
      </div>
    </>
  );
}

function WorkspacesPane() {
  return (
    <>
      <div className="sc-paneSidebar">
        <p className="sc-sectionTitle">Workspaces</p>
        <div className="sc-list">
          <div className="sc-item">
            <span className="sc-bullet sc-bulletGreen" />
            <span>main</span>
          </div>
          <div className="sc-item sc-itemActive">
            <span className="sc-bullet sc-bulletBlue" />
            <span>feature/auth <span className="sc-lineDim" style={{ fontSize: 9 }}>#47</span></span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletPink" />
            <span>fix/memory <span className="sc-lineDim" style={{ fontSize: 9 }}>#52</span></span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletBlue" />
            <span>docs/update</span>
          </div>
          <div className="sc-item">
            <span className="sc-bullet sc-bulletDim" />
            <span>chore/deps</span>
          </div>
        </div>
      </div>
      <div className="sc-paneMain">
        <div className="sc-codeBlock" role="img" aria-label="Sidecar output pane preview">
          <div className="sc-lineDim">feature/auth | <span className="sc-lineGreen">Ready</span></div>
          <div style={{ height: 3 }} />
          <div><span className="sc-lineBlue">Task:</span> td-a1b2c3 <span className="sc-lineDim">from td</span></div>
          <div><span className="sc-lineYellow">Prompts:</span> 3 configured</div>
          <div style={{ height: 3 }} />
          <div className="sc-lineDim">Actions:</div>
          <div>  <span className="sc-lineGreen">[n]</span> New workspace + agent</div>
          <div>  <span className="sc-lineGreen">[s]</span> Send task from td</div>
          <div>  <span className="sc-lineGreen">[p]</span> Run prompt sequence</div>
          <div style={{ height: 3 }} />
          <div className="sc-lineDim">3 commits ahead | checks pass</div>
        </div>
      </div>
    </>
  );
}

function Frame({ activeTab, onTabChange }) {
  const [time, setTime] = useState(() => {
    const now = new Date();
    return now.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
  });

  useEffect(() => {
    const updateTime = () => {
      const now = new Date();
      setTime(now.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false }));
    };
    const interval = setInterval(updateTime, 1000);
    return () => clearInterval(interval);
  }, []);

  const renderPane = () => {
    switch (activeTab) {
      case 'overview': return <OverviewPane />;
      case 'td': return <TdPane />;
      case 'git': return <GitPane />;
      case 'files': return <FilesPane />;
      case 'notes*': return <NotesPane />;
      case 'conversations': return <ConversationsPane />;
      case 'workspaces': return <WorkspacesPane />;
      default: return <OverviewPane />;
    }
  };

  return (
    <div className="sc-frame">
      <div className="sc-frameTop">
        <div className="sc-dots" aria-hidden="true">
          <span className="sc-dot" />
          <span className="sc-dot" />
          <span className="sc-dot" />
        </div>
        <div className="sc-topRight">
          <span className="sc-codeInline">sidecar</span>
          <span className="sc-lineDim">{time}</span>
        </div>
      </div>

      <div className="sc-tabs">
        {TABS.map((tab) => (
          <button
            key={tab}
            className={`sc-tab ${activeTab === tab ? 'sc-tabActive' : ''}`}
            onClick={() => onTabChange(tab)}
            type="button"
          >
            {tab}
          </button>
        ))}
      </div>

      <div className={activeTab === 'td' ? 'sc-frameBodyStacked' : 'sc-frameBody'} key={activeTab}>
        {renderPane()}
      </div>

      <div className="sc-frameFooter">
        <span className="sc-lineYellow">tab</span>
        <span className="sc-lineDim"> switch | </span>
        <span className="sc-lineYellow">enter</span>
        <span className="sc-lineDim"> select | </span>
        <span className="sc-lineYellow">?</span>
        <span className="sc-lineDim"> help | </span>
        <span className="sc-lineYellow">q</span>
        <span className="sc-lineDim"> quit</span>
      </div>
    </div>
  );
}

function FeatureCard({ id, title, chip, children, isHighlighted, isHero, onClick }) {
  return (
    <div
      className={`sc-card ${isHero ? 'sc-cardHero' : ''} ${isHighlighted ? 'sc-cardHighlighted' : ''}`}
      onClick={onClick}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => e.key === 'Enter' && onClick?.()}
    >
      <div className="sc-cardHeader">
        <h3 className="sc-cardTitle">{title}</h3>
        <span className="sc-chip">{chip}</span>
      </div>
      <p className="sc-cardBody">{children}</p>
    </div>
  );
}

// Mockup screens for component deep dive
function TdMockup() {
  return (
    <div className="sc-mockup sc-mockupTd">
      <div className="sc-mockupHeader">
        <span className="sc-mockupTitle">Task Management</span>
        <span className="sc-lineDim">3 tasks | 1 in progress</span>
      </div>
      <div className="sc-mockupBodyStacked">
        <div className="sc-mockupSection">
          <div className="sc-lineDim" style={{ fontSize: 9, marginBottom: 4, textTransform: 'uppercase' }}>Current Work</div>
          <div className="sc-mockupItem sc-mockupItemActive">
            <span className="sc-bullet sc-bulletGreen" />
            <span>td-a1b2c3</span>
            <span className="sc-lineYellow">in_progress</span>
            <span className="sc-lineDim">Implement auth flow</span>
          </div>
        </div>
        <div className="sc-mockupSection" style={{ flex: 2 }}>
          <div className="sc-lineDim" style={{ fontSize: 9, marginBottom: 4, textTransform: 'uppercase' }}>Board</div>
          <div style={{ display: 'grid', gap: 3 }}>
            <div className="sc-mockupItem">
              <span className="sc-linePink" style={{ fontSize: 9 }}>REV</span>
              <span>td-d4e5f6</span>
              <span className="sc-lineDim">Add rate limiting</span>
            </div>
            <div className="sc-mockupItem">
              <span className="sc-lineBlue" style={{ fontSize: 9 }}>RDY</span>
              <span>td-g7h8i9</span>
              <span className="sc-lineDim">Fix memory leak</span>
            </div>
            <div className="sc-mockupItem">
              <span className="sc-lineBlue" style={{ fontSize: 9 }}>RDY</span>
              <span>td-j0k1l2</span>
              <span className="sc-lineDim">Update API docs</span>
            </div>
          </div>
        </div>
        <div className="sc-mockupSection">
          <div className="sc-lineDim" style={{ fontSize: 9, marginBottom: 4, textTransform: 'uppercase' }}>Activity</div>
          <div style={{ fontSize: 9, display: 'grid', gap: 2 }}>
            <div><span className="sc-lineYellow">00:39</span> <span className="sc-lineDim">td-a1b2c3</span> Started</div>
            <div><span className="sc-lineYellow">00:15</span> <span className="sc-lineDim">td-d4e5f6</span> Submitted for review</div>
          </div>
        </div>
      </div>
      <div className="sc-mockupFooter">
        <span className="sc-lineYellow">n</span><span className="sc-lineDim"> new | </span>
        <span className="sc-lineYellow">e</span><span className="sc-lineDim"> edit | </span>
        <span className="sc-lineYellow">s</span><span className="sc-lineDim"> status | </span>
        <span className="sc-lineYellow">/</span><span className="sc-lineDim"> search</span>
      </div>
    </div>
  );
}

function GitMockup() {
  return (
    <div className="sc-mockup sc-mockupGit">
      <div className="sc-mockupHeader">
        <span className="sc-mockupTitle">Git Status</span>
        <span className="sc-lineDim">feature/auth | 3 changed</span>
      </div>
      <div className="sc-mockupBody">
        <div className="sc-mockupSidebar">
          <div className="sc-lineDim" style={{ fontSize: 9, marginBottom: 4, textTransform: 'uppercase' }}>Staged</div>
          <div className="sc-mockupItem sc-mockupItemActive">
            <span className="sc-lineGreen">M</span>
            <span>middleware.go</span>
          </div>
          <div className="sc-mockupItem">
            <span className="sc-lineGreen">A</span>
            <span>jwt.go</span>
          </div>
          <div className="sc-lineDim" style={{ fontSize: 9, marginBottom: 4, marginTop: 8, textTransform: 'uppercase' }}>Unstaged</div>
          <div className="sc-mockupItem">
            <span className="sc-linePink">D</span>
            <span>old_auth.go</span>
          </div>
        </div>
        <div className="sc-mockupMain">
          <div className="sc-mockupDiff">
            <div className="sc-lineDim" style={{ marginBottom: 6 }}>internal/auth/middleware.go</div>
            <div style={{ fontSize: 10, lineHeight: 1.4 }}>
              <div><span className="sc-lineBlue">@@ -42,6 +42,14 @@</span></div>
              <div><span className="sc-lineGreen">+func AuthMiddleware(next) {'{'}</span></div>
              <div><span className="sc-lineGreen">+  return http.HandlerFunc(w, r) {'{'}</span></div>
              <div><span className="sc-lineGreen">+    token := r.Header.Get("Auth")</span></div>
              <div><span className="sc-lineGreen">+    if !ValidateJWT(token) {'{'}</span></div>
              <div><span className="sc-lineGreen">+      http.Error(w, "Unauth", 401)</span></div>
              <div><span className="sc-lineGreen">+    {'}'}</span></div>
              <div><span className="sc-lineGreen">+  {'}'})</span></div>
              <div><span className="sc-lineGreen">+{'}'}</span></div>
            </div>
          </div>
        </div>
      </div>
      <div className="sc-mockupFooter">
        <span className="sc-lineYellow">a</span><span className="sc-lineDim"> stage | </span>
        <span className="sc-lineYellow">u</span><span className="sc-lineDim"> unstage | </span>
        <span className="sc-lineYellow">c</span><span className="sc-lineDim"> commit | </span>
        <span className="sc-lineYellow">d</span><span className="sc-lineDim"> diff</span>
      </div>
    </div>
  );
}

function FilesMockup() {
  return (
    <div className="sc-mockup sc-mockupFiles">
      <div className="sc-mockupHeader">
        <span className="sc-mockupTitle">File Browser</span>
        <span className="sc-lineDim">sidecar/internal</span>
      </div>
      <div className="sc-mockupBody">
        <div className="sc-mockupSidebar">
          <div className="sc-mockupItem">
            <span className="sc-lineDim">[v]</span>
            <span>internal/</span>
          </div>
          <div className="sc-mockupItem" style={{ paddingLeft: 10 }}>
            <span className="sc-lineDim">[v]</span>
            <span>auth/</span>
          </div>
          <div className="sc-mockupItem sc-mockupItemActive" style={{ paddingLeft: 18 }}>
            <span className="sc-bullet sc-bulletBlue" />
            <span>middleware.go</span>
          </div>
          <div className="sc-mockupItem" style={{ paddingLeft: 18 }}>
            <span className="sc-bullet sc-bulletBlue" />
            <span>jwt.go</span>
          </div>
          <div className="sc-mockupItem" style={{ paddingLeft: 10 }}>
            <span className="sc-lineDim">[&gt;]</span>
            <span>plugins/</span>
          </div>
          <div className="sc-mockupItem" style={{ paddingLeft: 10 }}>
            <span className="sc-lineDim">[&gt;]</span>
            <span>app/</span>
          </div>
        </div>
        <div className="sc-mockupMain">
          <div className="sc-mockupPreview">
            <div className="sc-lineDim" style={{ marginBottom: 6 }}>middleware.go | 156 lines | Go</div>
            <div style={{ fontSize: 10, lineHeight: 1.4 }}>
              <div><span className="sc-lineBlue">package</span> auth</div>
              <div style={{ height: 3 }} />
              <div><span className="sc-lineBlue">import</span> (</div>
              <div>  <span className="sc-lineYellow">"net/http"</span></div>
              <div>  <span className="sc-lineYellow">"strings"</span></div>
              <div>)</div>
              <div style={{ height: 3 }} />
              <div><span className="sc-linePink">// AuthMiddleware validates requests</span></div>
              <div><span className="sc-lineBlue">func</span> AuthMiddleware(next)...</div>
            </div>
          </div>
        </div>
      </div>
      <div className="sc-mockupFooter">
        <span className="sc-lineYellow">enter</span><span className="sc-lineDim"> open | </span>
        <span className="sc-lineYellow">/</span><span className="sc-lineDim"> search | </span>
        <span className="sc-lineYellow">e</span><span className="sc-lineDim"> editor | </span>
        <span className="sc-lineYellow">g</span><span className="sc-lineDim"> goto</span>
      </div>
    </div>
  );
}

function ConversationsMockup() {
  return (
    <div className="sc-mockup sc-mockupConvos">
      <div className="sc-mockupHeader">
        <span className="sc-mockupTitle">Conversations</span>
        <span className="sc-lineDim">18 sessions | all agents</span>
      </div>
      <div className="sc-mockupBody">
        <div className="sc-mockupSidebar">
          <div className="sc-mockupItem sc-mockupItemActive">
            <span className="sc-bullet sc-bulletGreen" />
            <div>
              <div>auth-flow <span className="sc-lineYellow" style={{ fontSize: 8 }}>Claude</span></div>
              <div className="sc-lineDim" style={{ fontSize: 9 }}>24m | 12.4k</div>
            </div>
          </div>
          <div className="sc-mockupItem">
            <span className="sc-bullet sc-bulletBlue" />
            <div>
              <div>rate-limit <span className="sc-linePink" style={{ fontSize: 8 }}>Cursor</span></div>
              <div className="sc-lineDim" style={{ fontSize: 9 }}>2h | 8.2k</div>
            </div>
          </div>
          <div className="sc-mockupItem">
            <span className="sc-bullet sc-bulletPink" />
            <div>
              <div>refactor <span className="sc-lineBlue" style={{ fontSize: 8 }}>Gemini</span></div>
              <div className="sc-lineDim" style={{ fontSize: 9 }}>1d | 24.1k</div>
            </div>
          </div>
        </div>
        <div className="sc-mockupMain">
          <div className="sc-mockupConvo">
            <div style={{ marginBottom: 8 }}>
              <div className="sc-lineBlue" style={{ marginBottom: 2 }}>User</div>
              <div style={{ fontSize: 10 }}>Add JWT auth to the API endpoints</div>
            </div>
            <div style={{ marginBottom: 8 }}>
              <div className="sc-lineGreen" style={{ marginBottom: 2 }}>Claude</div>
              <div style={{ fontSize: 10 }}>I'll implement JWT auth for your API.</div>
              <div className="sc-lineDim" style={{ fontSize: 9, marginTop: 2 }}>Let me check the existing auth setup...</div>
            </div>
            <div style={{ fontSize: 9, display: 'grid', gap: 2 }}>
              <div><span className="sc-lineYellow">-&gt;</span> Read middleware.go</div>
              <div><span className="sc-lineYellow">-&gt;</span> Edit jwt.go</div>
              <div><span className="sc-lineYellow">-&gt;</span> Write auth_test.go</div>
            </div>
          </div>
        </div>
      </div>
      <div className="sc-mockupFooter">
        <span className="sc-lineYellow">enter</span><span className="sc-lineDim"> expand | </span>
        <span className="sc-lineYellow">/</span><span className="sc-lineDim"> search | </span>
        <span className="sc-lineYellow">y</span><span className="sc-lineDim"> copy | </span>
        <span className="sc-lineYellow">j/k</span><span className="sc-lineDim"> nav</span>
      </div>
    </div>
  );
}

function WorkspacesMockup() {
  return (
    <div className="sc-mockup sc-mockupWorkspaces">
      <div className="sc-mockupHeader">
        <span className="sc-mockupTitle">Workspaces</span>
        <span className="sc-lineDim">zero commands | auto</span>
      </div>
      <div className="sc-mockupBody">
        <div className="sc-mockupSidebar">
          <div className="sc-mockupItem">
            <span className="sc-bullet sc-bulletGreen" />
            <div>
              <div>main</div>
              <div className="sc-lineDim" style={{ fontSize: 9 }}>default</div>
            </div>
          </div>
          <div className="sc-mockupItem sc-mockupItemActive">
            <span className="sc-bullet sc-bulletBlue" />
            <div>
              <div>feature/auth</div>
              <div className="sc-lineDim" style={{ fontSize: 9 }}>PR #47 | ready</div>
            </div>
          </div>
          <div className="sc-mockupItem">
            <span className="sc-bullet sc-bulletPink" />
            <div>
              <div>fix/memory</div>
              <div className="sc-lineDim" style={{ fontSize: 9 }}>td-g7h8i9</div>
            </div>
          </div>
        </div>
        <div className="sc-mockupMain">
          <div className="sc-mockupWorkspace">
            <div className="sc-lineBlue" style={{ fontSize: 12, marginBottom: 6 }}>feature/auth</div>
            <div style={{ display: 'grid', gap: 3, fontSize: 10 }}>
              <div><span className="sc-lineDim">PR:</span> <span className="sc-lineGreen">#47 Add JWT auth</span></div>
              <div><span className="sc-lineDim">Task:</span> <span className="sc-lineYellow">td-a1b2c3</span> <span className="sc-lineDim">from td</span></div>
              <div><span className="sc-lineDim">Status:</span> <span className="sc-lineGreen">Ready to merge</span></div>
            </div>
            <div style={{ marginTop: 8, fontSize: 10 }}>
              <div className="sc-lineDim" style={{ marginBottom: 3 }}>Quick actions</div>
              <div><span className="sc-lineGreen">[n]</span> New workspace + agent</div>
              <div><span className="sc-lineGreen">[s]</span> Send task from td</div>
              <div><span className="sc-lineGreen">[p]</span> Run prompt sequence</div>
              <div><span className="sc-lineGreen">[m]</span> Merge & cleanup</div>
            </div>
          </div>
        </div>
      </div>
      <div className="sc-mockupFooter">
        <span className="sc-lineYellow">n</span><span className="sc-lineDim"> new | </span>
        <span className="sc-lineYellow">s</span><span className="sc-lineDim"> send task | </span>
        <span className="sc-lineYellow">p</span><span className="sc-lineDim"> prompts | </span>
        <span className="sc-lineYellow">m</span><span className="sc-lineDim"> merge</span>
      </div>
    </div>
  );
}

function OverviewMockup() {
  return (
    <div className="sc-mockup sc-mockupOverview">
      <div className="sc-mockupHeader">
        <span className="sc-mockupTitle">Agent Overview Board</span>
        <span className="sc-lineDim">3 projects · 4 agents active</span>
      </div>
      <div className="sc-mockupKanbanBody">
        {/* Column 1: WORKING */}
        <div className="sc-kanbanColumn">
          <div className="sc-kanbanLaneHeader sc-laneGreen">
            <span className="sc-laneDot sc-bulletGreen" />
            <span>WORKING</span>
            <span className="sc-laneCount">2</span>
          </div>
          <div className="sc-kanbanCard sc-cardActive">
            <div className="sc-cardTop">
              <span className="sc-lineYellow">sidecar</span>
              <span className="sc-lineGreen">Claude</span>
            </div>
            <div className="sc-cardBranch">feature/auth</div>
            <div className="sc-cardMeta">
              <span className="sc-lineDim">td-a1b2c3</span>
              <span className="sc-lineGreen">24m</span>
            </div>
          </div>
          <div className="sc-kanbanCard">
            <div className="sc-cardTop">
              <span className="sc-lineYellow">betamax</span>
              <span className="sc-linePink">AGY</span>
            </div>
            <div className="sc-cardBranch">fix/recorder</div>
            <div className="sc-cardMeta">
              <span className="sc-lineDim">td-99ff22</span>
              <span className="sc-linePink">12m</span>
            </div>
          </div>
        </div>

        {/* Column 2: NEEDS ATTENTION */}
        <div className="sc-kanbanColumn">
          <div className="sc-kanbanLaneHeader sc-lanePink">
            <span className="sc-laneDot sc-bulletPink" />
            <span>ATTENTION</span>
            <span className="sc-laneCount">1</span>
          </div>
          <div className="sc-kanbanCard">
            <div className="sc-cardTop">
              <span className="sc-lineYellow">sidecar</span>
              <span className="sc-lineBlue">Cursor</span>
            </div>
            <div className="sc-cardBranch">fix/memory-leak</div>
            <div className="sc-cardMeta">
              <span className="sc-linePink">blocked</span>
              <span className="sc-lineDim">td-g7h8i9</span>
            </div>
          </div>
        </div>

        {/* Column 3: DONE */}
        <div className="sc-kanbanColumn">
          <div className="sc-kanbanLaneHeader sc-laneBlue">
            <span className="sc-laneDot sc-bulletBlue" />
            <span>DONE</span>
            <span className="sc-laneCount">1</span>
          </div>
          <div className="sc-kanbanCard">
            <div className="sc-cardTop">
              <span className="sc-lineYellow">td</span>
              <span className="sc-lineBlue">Grok</span>
            </div>
            <div className="sc-cardBranch">refactor/cli</div>
            <div className="sc-cardMeta">
              <span className="sc-lineGreen">PR #42</span>
              <span className="sc-lineDim">td-33aa11</span>
            </div>
          </div>
        </div>

        {/* Column 4: IDLE */}
        <div className="sc-kanbanColumn">
          <div className="sc-kanbanLaneHeader sc-laneDim">
            <span className="sc-laneDot sc-bulletDim" />
            <span>IDLE</span>
            <span className="sc-laneCount">1</span>
          </div>
          <div className="sc-kanbanCard sc-cardIdle">
            <div className="sc-cardTop">
              <span className="sc-lineYellow">sidecar</span>
              <span className="sc-lineDim">main</span>
            </div>
            <div className="sc-cardBranch">main branch</div>
            <div className="sc-cardMeta">
              <span className="sc-lineDim">idle</span>
            </div>
          </div>
        </div>
      </div>
      <div className="sc-mockupFooter">
        <span className="sc-lineYellow">@</span><span className="sc-lineDim"> switch project | </span>
        <span className="sc-lineYellow">h/l</span><span className="sc-lineDim"> lanes | </span>
        <span className="sc-lineYellow">j/k</span><span className="sc-lineDim"> cards | </span>
        <span className="sc-lineYellow">enter</span><span className="sc-lineDim"> open workspace</span>
      </div>
    </div>
  );
}

function NotesMockup() {
  return (
    <div className="sc-mockup sc-mockupNotes">
      <div className="sc-mockupHeader">
        <span className="sc-mockupTitle">In-TUI Developer Notes</span>
        <span className="sc-chipBeta">notes_plugin</span>
      </div>
      <div className="sc-mockupBody">
        <div className="sc-mockupSidebar">
          <div className="sc-lineDim" style={{ fontSize: 9, marginBottom: 4, textTransform: 'uppercase' }}>Notes</div>
          <div className="sc-mockupItem sc-mockupItemActive">
            <span className="sc-bullet sc-bulletYellow" />
            <span>auth-notes.md</span>
          </div>
          <div className="sc-mockupItem">
            <span className="sc-bullet sc-bulletBlue" />
            <span>prompt-ideas.md</span>
          </div>
          <div className="sc-mockupItem">
            <span className="sc-bullet sc-bulletDim" />
            <span>todo.md</span>
          </div>
        </div>
        <div className="sc-mockupMain">
          <div className="sc-mockupPreview">
            <div className="sc-lineDim" style={{ marginBottom: 6 }}>auth-notes.md | Linked to <span className="sc-lineYellow">td-a1b2c3</span></div>
            <div style={{ fontSize: 10, lineHeight: 1.4 }}>
              <div><span className="sc-linePink"># Auth Migration Strategy</span></div>
              <div style={{ height: 2 }} />
              <div><span className="sc-lineDim">Key items to double check before PR review:</span></div>
              <div><span className="sc-lineBlue">- [x]</span> Validate JWT header injection</div>
              <div><span className="sc-lineBlue">- [ ]</span> Unit tests for expired tokens</div>
            </div>
          </div>
        </div>
      </div>
      <div className="sc-mockupFooter">
        <span className="sc-lineYellow">e</span><span className="sc-lineDim"> edit | </span>
        <span className="sc-lineYellow">/</span><span className="sc-lineDim"> search | </span>
        <span className="sc-lineYellow">t</span><span className="sc-lineDim"> link task | </span>
        <span className="sc-lineDim">* Beta (behind notes_plugin flag)</span>
      </div>
    </div>
  );
}

function ComponentSection({ id, title, features, gradient, MockupComponent }) {
  return (
    <div className={`sc-componentSection ${gradient}`} id={id}>
      <div className="sc-componentContent">
        <div className="sc-componentInfo">
          <h3 className="sc-componentTitle">{title}</h3>
          <div className="sc-componentFeatures">
            {features.map((feature, idx) => (
              <div key={idx} className="sc-componentFeature">
                <i className="icon-check sc-featureIcon" />
                <span>{feature}</span>
              </div>
            ))}
          </div>
        </div>
        <div className="sc-componentMockup">
          <MockupComponent />
        </div>
      </div>
    </div>
  );
}

function FeatureListItem({ icon, title, description, color }) {
  return (
    <div className="sc-featureListItem">
      <div className="sc-featureListHeader">
        <h4 className={`sc-featureListTitle ${color ? `sc-featureColor-${color}` : ''}`}>{title}</h4>
        <div className="sc-featureListIcon">
          <i className={`icon-${icon}`} />
        </div>
      </div>
      <p className="sc-featureListDesc">{description}</p>
    </div>
  );
}

function MiniFeatureItem({ icon, title, description }) {
  const [isOpen, setIsOpen] = useState(false);

  const handleToggle = useCallback(() => {
    setIsOpen(prev => !prev);
  }, []);

  const handleMouseEnter = useCallback(() => {
    setIsOpen(true);
  }, []);

  const handleMouseLeave = useCallback(() => {
    setIsOpen(false);
  }, []);

  return (
    <div
      className={`sc-miniFeature ${isOpen ? 'sc-miniFeatureOpen' : ''}`}
      onMouseEnter={handleMouseEnter}
      onMouseLeave={handleMouseLeave}
      onClick={handleToggle}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => e.key === 'Enter' && handleToggle()}
    >
      <span className="sc-miniFeatureIcon"><i className={`icon-${icon}`} /></span>
      <span>{title}</span>
      {isOpen && (
        <div className="sc-miniFeatureTooltip">
          <div className="sc-miniFeatureTooltipContent">
            {description}
          </div>
        </div>
      )}
    </div>
  );
}

function WorkflowSection() {
  return (
    <section className="sc-workflow">
      <div className="container">
        <h2 className="sc-showcaseTitle" style={{ textAlign: 'center', marginBottom: '48px' }}>The Sidecar Workflow</h2>
        
        <div className="sc-workflowGrid">
          
          <div className="sc-workflowStep sc-step-green">
            <div className="sc-workflowIconBox">
              <i className="icon-list sc-workflowIcon" />
            </div>
            <div>
              <h3 className="sc-workflowTitle">1. Plan</h3>
              <p className="sc-workflowDesc">
                Create tasks in <a href="https://td.haplab.com/" className="sc-inlineLink">td</a> to give agents clear objectives and context.
              </p>
            </div>
          </div>

          <div className="sc-workflowStep sc-step-blue">
            <div className="sc-workflowIconBox">
              <i className="icon-terminal sc-workflowIcon" />
            </div>
            <div>
              <h3 className="sc-workflowTitle">2. Act</h3>
              <p className="sc-workflowDesc">
                Your agent (Claude, Cursor, etc.) reads the task and writes code.
              </p>
            </div>
          </div>

          <div className="sc-workflowStep sc-step-purple">
            <div className="sc-workflowIconBox">
              <i className="icon-eye sc-workflowIcon" />
            </div>
            <div>
              <h3 className="sc-workflowTitle">3. Monitor</h3>
              <p className="sc-workflowDesc">
                Watch the agent's progress, diffs, and logs in Sidecar's TUI.
              </p>
            </div>
          </div>

          <div className="sc-workflowStep sc-step-pink">
            <div className="sc-workflowIconBox">
              <i className="icon-check sc-workflowIcon" />
            </div>
            <div>
              <h3 className="sc-workflowTitle">4. Review</h3>
              <p className="sc-workflowDesc">
                Verify the changes, commit, and mark the task as done.
              </p>
            </div>
          </div>

        </div>
      </div>
    </section>
  );
}

import useBaseUrl from '@docusaurus/useBaseUrl';

// ... (existing imports)

export default function Home() {
  const { siteConfig } = useDocusaurusContext();
  const [activeTab, setActiveTab] = useState('td');
  const [showInstallMethods, setShowInstallMethods] = useState(false);
  const [showVideoModal, setShowVideoModal] = useState(false);

  useEffect(() => {
    if (!showVideoModal) return;
    const onKey = (e) => { if (e.key === 'Escape') setShowVideoModal(false); };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [showVideoModal]);

  const handleTabChange = (tab) => {
    setActiveTab(tab);
  };

  const handleCardClick = (tab) => {
    setActiveTab(tab);
  };

  return (
    <Layout
      title="You might never open your editor again"
      description="Use sidecar for planning features, managing workspaces, reviewing diffs, and staging commits. It's the most fun you can have in a terminal."
    >
      <header className="sc-hero">
        <div className="container">
          <div className="sc-heroHeader">
            <h1 className="sc-title">
              <img 
                src={useBaseUrl('/img/sidecar-logo.png')} 
                alt="Sidecar" 
                className="sc-logo"
              />
              <span className="sc-titleTagline">You might never open your editor again.</span>
            </h1>

            <p className="sc-subtitle">
              Sidecar puts your entire development workflow in one shell:
              plan tasks with <a href="https://td.haplab.com/" className="sc-inlineLink">td</a>, chat with AI agents, review diffs, stage commits, switch between projects instantly, and manage git workspaces—all without leaving Sidecar.
            </p>

            <div className="sc-heroCta">
              <div className="sc-installWrapper">
                <div className="sc-codeBlock sc-installBlock sc-installHero" aria-label="Quick install snippet">
                  <div className="sc-installCommand">
                    <span className="sc-lineBlue">$ </span>
                    <span>{INSTALL_COMMAND}</span>
                  </div>
                </div>
                <CopyButton text={INSTALL_COMMAND} />
              </div>

              <div className="sc-heroSecondary">
                <Link className="sc-btnSecondary" to="/docs/intro">
                  <i className="icon-book-open" /> Docs
                </Link>
                <a className="sc-btnSecondary" href={siteConfig.customFields?.githubUrl || 'https://github.com/marcus/sidecar'}>
                  <i className="icon-github" /> GitHub
                </a>
              </div>

              <div className="sc-heroNote">
                <span className="sc-heroNoteHighlight">Free & Open Source</span> (MIT)
              </div>
              <button
                className="sc-installMethodsLink"
                onClick={() => setShowInstallMethods(true)}
              >
                Other install methods →
              </button>
            </div>
          </div>
        </div>
      </header>

      <div className="sc-videoCta">
        <button className="sc-videoLink" onClick={() => setShowVideoModal(true)}>
          <span className="sc-videoPlayIcon">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><polygon points="6,3 20,12 6,21" /></svg>
          </span>
          <span className="sc-videoLinkText">Watch the demo</span>
          <span className="sc-videoDuration">10 min</span>
        </button>
      </div>

      <section className="sc-demoSection">
        <div className="container">
          <Frame activeTab={activeTab} onTabChange={handleTabChange} />
        </div>
      </section>

      <main className="sc-main">
        {/* Feature Cards */}
        <section className="sc-grid">
          <div className="container">
            <div className="sc-gridInner sc-gridFeatures">
              {/* Overview Hero Card - double wide */}
              <FeatureCard
                id="overview"
                title="Cross-project Agent Overview"
                chip="overview"
                isHero={true}
                isHighlighted={activeTab === 'overview'}
                onClick={() => handleCardClick('overview')}
              >
                Monitor active agents, tasks, and tmux workspaces across all your repositories in a single real-time grid.
                Color-encoded matrix shows agent status, task progress, and running processes across your entire dev environment.
              </FeatureCard>

              <FeatureCard
                id="td"
                title={<>Plan with <a href="https://td.haplab.com/" className="sc-inlineLink">td</a></>}
                chip="td"
                isHighlighted={activeTab === 'td'}
                onClick={() => handleCardClick('td')}
              >
                Give agents structured work so they can operate autonomously for longer. Tasks persist across
                context resets with built-in review workflows.
              </FeatureCard>

              <FeatureCard
                id="notes"
                title={<>Developer Notes <span className="sc-chipBeta">notes_plugin</span></>}
                chip="notes*"
                isHighlighted={activeTab === 'notes*'}
                onClick={() => handleCardClick('notes*')}
              >
                Jot down specs, agent prompts, and debug scratchpads directly in Sidecar with vim inline editing and task linking. (*Beta)
              </FeatureCard>

              <FeatureCard
                id="git"
                title="See what the agent changed"
                chip="git"
                isHighlighted={activeTab === 'git'}
                onClick={() => handleCardClick('git')}
              >
                Split-pane diffs, commit context, fast staging—all without bouncing to an IDE.
              </FeatureCard>

              <FeatureCard
                id="files"
                title="Browse and preview files"
                chip="files"
                isHighlighted={activeTab === 'files'}
                onClick={() => handleCardClick('files')}
              >
                Navigate your codebase with a tree view, preview file contents, and jump to any file instantly.
              </FeatureCard>

              <FeatureCard
                id="conversations"
                title="One timeline, all agents"
                chip="conversations"
                isHighlighted={activeTab === 'conversations'}
                onClick={() => handleCardClick('conversations')}
              >
                All your agents in one timeline—Claude, Cursor, Gemini, Antigravity, Grok, Copilot, Amp, Kiro, Pi, and more.
              </FeatureCard>

              <FeatureCard
                id="workspaces"
                title="Zero-command workspace workflow"
                chip="workspaces"
                isHighlighted={activeTab === 'workspaces'}
                onClick={() => handleCardClick('workspaces')}
              >
                Create workspaces, pass tasks from td, kick off with configured prompts—no git commands needed. Everything is automatic.
              </FeatureCard>
            </div>
          </div>
        </section>

        <WorkflowSection />

        {/* Component Showcase Sections */}
        <section className="sc-showcase">
          <div className="container">
            <h2 className="sc-showcaseTitle">Sidecar's Features</h2>
          </div>

          <div className="sc-showcaseFullWidth">
            <ComponentSection
              id="showcase-overview"
              title="Cross-Project Agent Overview"
              gradient="sc-gradientOrange"
              MockupComponent={OverviewMockup}
              features={[
                'Unified multi-project matrix across all repositories',
                'Color-encoded status cards for active AI agents',
                'Real-time workspace inventory and running process follow',
                'Switch active project context instantly with @ key',
                'Aggregated task progress and PR state across repositories',
                'Automatic polling and status synchronization',
              ]}
            />

            <ComponentSection
              id="showcase-notes"
              title={<>In-TUI Developer Notes <span className="sc-chipBeta">notes_plugin</span></>}
              gradient="sc-gradientTeal"
              MockupComponent={NotesMockup}
              features={[
                'In-TUI markdown scratchpad for specs, prompts, and notes',
                'Inline vim/nvim editor directly within the note preview pane',
                'Direct task linking between notes and td tasks',
                'Fuzzy title and content search across all notes',
                'Per-project note persistence in workspace state',
                'Beta feature: enable with SIDECAR_FEATURE_NOTES_PLUGIN=1',
              ]}
            />

            <ComponentSection
              id="showcase-td"
              title={<>Plan with <a href="https://td.haplab.com/" className="sc-inlineLink">td</a></>}
              gradient="sc-gradientGreen"
              MockupComponent={TdMockup}
              features={[
                'Structured work keeps agents focused and autonomous',
                'Tasks persist across context window resets',
                'Built-in review workflow: verify before moving on',
                'Hierarchical subtasks break down complex work',
                'Status tracking: pending, in_progress, blocked, done',
                'Epics group related tasks for larger features',
                'Integrate with git commits and PRs',
              ]}
            />

            <ComponentSection
              id="showcase-git"
              title="Git Status & Diff"
              gradient="sc-gradientBlue"
              MockupComponent={GitMockup}
              features={[
                'Real-time status of staged and unstaged changes',
                'Inline diff viewer with syntax highlighting',
                'Stage/unstage files with single keypress',
                'Commit directly from the interface',
                'View commit history and messages',
                'Branch switching and creation',
                'Stash management',
              ]}
            />

            <ComponentSection
              id="showcase-files"
              title="File Browser"
              gradient="sc-gradientPurple"
              MockupComponent={FilesMockup}
              features={[
                'Tree view with expand/collapse',
                'File preview with syntax highlighting',
                'Quick jump with fuzzy search',
                'Show git status indicators on files',
                'Open files in external editor',
                'Navigate to file from other plugins',
                'Respect .gitignore patterns',
              ]}
            />

            <ComponentSection
              id="showcase-conversations"
              title="Unified Conversation Timeline"
              gradient="sc-gradientPink"
              MockupComponent={ConversationsMockup}
              features={[
                'Chronological view across all coding agents',
                'Claude, Cursor, Gemini, Codex, Amp, Kiro, Pi, Opencode, and Warp in one list',
                'Search across all adapters at once',
                'Filter by agent, date, or content',
                'Expand messages and view tool calls',
                'Token usage and session duration stats',
                'Copy and export conversation content',
              ]}
            />

            <ComponentSection
              id="showcase-workspaces"
              title="Zero-Command Workspace Workflow"
              gradient="sc-gradientYellow"
              MockupComponent={WorkspacesMockup}
              features={[
                'No git commands needed--everything is automatic',
                'Pass tasks directly from td to new workspaces',
                'Configure prompt sequences to kick off agents',
                'Create, switch, merge, delete with single keys',
                'PR status and CI checks at a glance',
                'Auto-cleanup after merge',
                'Linked task tracking across workspaces',
              ]}
            />
          </div>
        </section>

        {/* Comprehensive Features Section */}
        <section className="sc-features">
          <div className="container">
            <h2 className="sc-featuresTitle">Additional Features</h2>

            <div className="sc-featuresGrid">
              <FeatureListItem
                icon="feather"
                title="Zero Config Setup"
                color="green"
                description="Just add a couple lines to your AGENTS.md file. No hooks, no agent modifications. Easy to add, easy to remove."
              />
              <FeatureListItem
                icon="zap"
                title="Instant Startup"
                color="yellow"
                description="Launches in milliseconds. No waiting for heavy runtimes or dependency resolution."
              />
              <FeatureListItem
                icon="columns-2"
                title="Tab-Based Navigation"
                color="blue"
                description="Switch between plugins instantly with tab/shift-tab. Each plugin is a focused view of your project."
              />
              <FeatureListItem
                icon="mouse-pointer"
                title="Full Mouse Support"
                color="purple"
                description="Click, scroll, and navigate with your mouse. Almost every element in the TUI responds to mouse interaction."
              />
              <FeatureListItem
                icon="refresh-cw"
                title="Auto-Update"
                color="pink"
                description="Sidecar checks for updates automatically and can update itself in place. Always stay on the latest version."
              />
              <FeatureListItem
                icon="layers"
                title="Multi-Agent Support"
                color="orange"
                description="Works with Claude Code, Antigravity, Grok, Copilot, Codex, Gemini CLI, Opencode, Cursor, Amp Code, Kiro, Pi, and Warp."
              />
              <FeatureListItem
                icon="git-branch"
                title="Git Integration"
                color="blue"
                description="Deep integration with git: status, diff, staging, commits, branches, and workspaces."
              />
              <FeatureListItem
                icon="palette"
                title="Custom Themes"
                color="green"
                description="Built-in themes plus a community theme browser with live previews and contrast enforcement."
              />
              <FeatureListItem
                icon="folder-kanban"
                title="Instant Project Switching"
                color="yellow"
                description="Switch back and forth between projects with @. Cursor position, active plugin, and view preferences restore per-project."
              />
              <FeatureListItem
                icon="monitor"
                title="Live PTY & tmux"
                color="green"
                description="Embedded PTY backend and tmux follow grid for seamless background agent execution."
              />
              <FeatureListItem
                icon="keyboard"
                title="Vim-Style Keybindings"
                color="purple"
                description="j/k navigation, /search, and familiar modal interactions. Your muscle memory works here."
              />
              <FeatureListItem
                icon="code"
                title="Open Source"
                color="pink"
                description="MIT licensed. Inspect the code, contribute features, or fork for your needs."
              />
              <FeatureListItem
                icon="package"
                title="Single Binary"
                color="orange"
                description="No dependencies to install. Download one binary and you're ready to go."
              />
            </div>
          </div>
        </section>

        {/* Even more features - smaller blocks */}
        <section className="sc-moreFeatures">
          <div className="container">
            <h2 className="sc-featuresTitle" style={{ fontSize: '20px', marginBottom: '24px' }}>Even more features</h2>
            <div className="sc-miniFeaturesGrid">
              {MINI_FEATURES.map((feature, idx) => (
                <MiniFeatureItem
                  key={idx}
                  icon={feature.icon}
                  title={feature.title}
                  description={feature.description}
                />
              ))}
            </div>
          </div>
        </section>

        {/* Supported Agents */}
        <section className="sc-agents">
          <div className="container">
            <h2 className="sc-agentsTitle">Supported Agents</h2>
            <p className="sc-agentsSubtitle">
              Sidecar reads session data from multiple AI coding tools, giving you a unified view of agent activity
            </p>

            <div className="sc-agentsGrid">
              <div className="sc-agentCard">
                <div className="sc-agentLogo">
                  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect width="32" height="32" rx="6" fill="#D97706" />
                    <path d="M16 6L8 10v12l8 4 8-4V10l-8-4zm0 2.2l5.6 2.8L16 13.8l-5.6-2.8L16 8.2zM10 11.8l5 2.5v7.4l-5-2.5v-7.4zm12 0v7.4l-5 2.5v-7.4l5-2.5z" fill="white" />
                  </svg>
                </div>
                <div className="sc-agentInfo">
                  <h3 className="sc-agentName">Claude Code</h3>
                  <p className="sc-agentDesc">Anthropic's official CLI for Claude</p>
                </div>
              </div>

              <div className="sc-agentCard">
                <div className="sc-agentLogo">
                  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect width="32" height="32" rx="6" fill="#1A73E8" />
                    <path d="M16 7L24 21H8L16 7z" fill="#4285F4" />
                    <path d="M16 11L21 20H11L16 11z" fill="#34A853" />
                    <circle cx="16" cy="17" r="2.5" fill="#FBBC04" />
                  </svg>
                </div>
                <div className="sc-agentInfo">
                  <h3 className="sc-agentName">Antigravity</h3>
                  <p className="sc-agentDesc">Google DeepMind's agentic AI coding assistant</p>
                </div>
              </div>

              <div className="sc-agentCard">
                <div className="sc-agentLogo">
                  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect width="32" height="32" rx="6" fill="#000000" />
                    <path d="M9 9l14 14M23 9L9 23" stroke="white" strokeWidth="3" strokeLinecap="round" />
                  </svg>
                </div>
                <div className="sc-agentInfo">
                  <h3 className="sc-agentName">xAI Grok</h3>
                  <p className="sc-agentDesc">xAI's developer CLI & coding agent</p>
                </div>
              </div>

              <div className="sc-agentCard">
                <div className="sc-agentLogo">
                  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect width="32" height="32" rx="6" fill="#24292F" />
                    <path d="M16 8a8 8 0 00-8 8c0 3.5 2.3 6.5 5.5 7.5.4.1.5-.2.5-.4v-1.4c-2.2.5-2.7-1.1-2.7-1.1-.4-.9-.9-1.2-.9-1.2-.7-.5.1-.5.1-.5.8.1 1.2.8 1.2.8.7 1.2 1.9.9 2.4.7.1-.5.3-.9.5-1.1-1.8-.2-3.6-.9-3.6-4 0-.9.3-1.6.8-2.1-.1-.2-.4-1 .1-2.1 0 0 .7-.2 2.2.8.7-.2 1.4-.3 2.1-.3.7 0 1.4.1 2.1.3 1.5-1 2.2-.8 2.2-.8.5 1.1.2 1.9.1 2.1.5.5.8 1.2.8 2.1 0 3.1-1.9 3.8-3.7 4 .3.3.6.8.6 1.6v2.4c0 .2.1.5.6.4A8 8 0 0024 16a8 8 0 00-8-8z" fill="white" />
                  </svg>
                </div>
                <div className="sc-agentInfo">
                  <h3 className="sc-agentName">GitHub Copilot</h3>
                  <p className="sc-agentDesc">GitHub's AI pair programmer CLI</p>
                </div>
              </div>

              <div className="sc-agentCard">
                <div className="sc-agentLogo">
                  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect width="32" height="32" rx="6" fill="#10A37F" />
                    <circle cx="16" cy="16" r="8" stroke="white" strokeWidth="2" fill="none" />
                    <circle cx="16" cy="16" r="3" fill="white" />
                  </svg>
                </div>
                <div className="sc-agentInfo">
                  <h3 className="sc-agentName">Codex</h3>
                  <p className="sc-agentDesc">OpenAI's code generation model</p>
                </div>
              </div>

              <div className="sc-agentCard">
                <div className="sc-agentLogo">
                  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect width="32" height="32" rx="6" fill="#4285F4" />
                    <path d="M16 8l-6.93 12h13.86L16 8z" fill="#EA4335" />
                    <path d="M9.07 20L16 8v12H9.07z" fill="#FBBC05" />
                    <path d="M22.93 20L16 8v12h6.93z" fill="#34A853" />
                  </svg>
                </div>
                <div className="sc-agentInfo">
                  <h3 className="sc-agentName">Gemini CLI</h3>
                  <p className="sc-agentDesc">Google's multimodal AI assistant</p>
                </div>
              </div>

              <div className="sc-agentCard">
                <div className="sc-agentLogo">
                  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect width="32" height="32" rx="6" fill="#6366F1" />
                    <path d="M10 10h12v12H10V10z" stroke="white" strokeWidth="2" fill="none" />
                    <path d="M14 14h4v4h-4v-4z" fill="white" />
                  </svg>
                </div>
                <div className="sc-agentInfo">
                  <h3 className="sc-agentName">Opencode</h3>
                  <p className="sc-agentDesc">Terminal-first AI coding assistant</p>
                </div>
              </div>

              <div className="sc-agentCard">
                <div className="sc-agentLogo">
                  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect width="32" height="32" rx="6" fill="#171717" />
                    <path d="M8 16a8 8 0 1 1 16 0" stroke="#F7B500" strokeWidth="2.5" strokeLinecap="round" />
                    <circle cx="16" cy="16" r="3" fill="#F7B500" />
                    <path d="M16 19v5" stroke="#F7B500" strokeWidth="2" strokeLinecap="round" />
                  </svg>
                </div>
                <div className="sc-agentInfo">
                  <h3 className="sc-agentName">Cursor</h3>
                  <p className="sc-agentDesc">AI-first code editor (cursor-agent)</p>
                </div>
              </div>

              <div className="sc-agentCard">
                <div className="sc-agentLogo">
                  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect width="32" height="32" rx="6" fill="#1A1A2E" />
                    <path d="M10 22V10l6 6 6-6v12" stroke="#E94560" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" />
                  </svg>
                </div>
                <div className="sc-agentInfo">
                  <h3 className="sc-agentName">Amp Code</h3>
                  <p className="sc-agentDesc">Amp's AI coding assistant</p>
                </div>
              </div>

              <div className="sc-agentCard">
                <div className="sc-agentLogo">
                  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect width="32" height="32" rx="6" fill="#0F172A" />
                    <text x="16" y="21" textAnchor="middle" fill="#38BDF8" fontSize="16" fontFamily="serif" fontStyle="italic">k</text>
                  </svg>
                </div>
                <div className="sc-agentInfo">
                  <h3 className="sc-agentName">Kiro</h3>
                  <p className="sc-agentDesc">Amazon's AI coding assistant</p>
                </div>
              </div>

              <div className="sc-agentCard">
                <div className="sc-agentLogo">
                  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect width="32" height="32" rx="6" fill="#2D1B69" />
                    <circle cx="12" cy="14" r="2.5" fill="#A78BFA" />
                    <circle cx="20" cy="14" r="2.5" fill="#A78BFA" />
                    <path d="M10 20c0 0 2 3 6 3s6-3 6-3" stroke="#A78BFA" strokeWidth="2" strokeLinecap="round" />
                    <circle cx="12" cy="14" r="1" fill="white" />
                    <circle cx="20" cy="14" r="1" fill="white" />
                  </svg>
                </div>
                <div className="sc-agentInfo">
                  <h3 className="sc-agentName">Pi</h3>
                  <p className="sc-agentDesc">Pi AI agent (OpenClaw)</p>
                </div>
              </div>

              <div className="sc-agentCard">
                <div className="sc-agentLogo">
                  <svg viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
                    <rect width="32" height="32" rx="6" fill="#01A4FF" />
                    <path d="M8 16h5l3-6 3 12 3-6h2" stroke="white" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
                  </svg>
                </div>
                <div className="sc-agentInfo">
                  <h3 className="sc-agentName">Warp</h3>
                  <p className="sc-agentDesc">Warp terminal AI assistant</p>
                </div>
              </div>
            </div>

            <p className="sc-agentsNote">
              Each agent stores session data in its own format. Sidecar normalizes and displays them in a unified interface.
            </p>
          </div>
        </section>

        {/* Bottom CTA */}
        <section className="sc-bottomCta">
          <div className="container">
            <h2 className="sc-bottomCtaTitle">Get started in seconds</h2>
            <div className="sc-installWrapper">
              <div className="sc-codeBlock sc-installBlock sc-installHero" aria-label="Quick install snippet">
                <div className="sc-installCommand">
                  <span className="sc-lineBlue">$ </span>
                  <span>{INSTALL_COMMAND}</span>
                </div>
              </div>
              <CopyButton text={INSTALL_COMMAND} />
            </div>
            <div className="sc-bottomCtaLinks">
              <Link className="sc-btnSecondary" to="/docs/intro">
                <i className="icon-book-open" /> Docs
              </Link>
              <a className="sc-btnSecondary" href="https://github.com/marcus/sidecar">
                <i className="icon-github" /> GitHub
              </a>
            </div>
            <p className="sc-bottomCtaAlt">
              Also available via <button className="sc-installMethodsInline" onClick={() => setShowInstallMethods(true)}>Homebrew, binary download, or source</button>
            </p>
          </div>
        </section>

        {/* Sister Projects */}
        <section className="sc-sisterProjects">
          <div className="container">
            <h2 className="sc-sisterTitle">Sister Projects</h2>
            <a href="https://haplab.com" className="sc-sisterHaplab">
              <img src={useBaseUrl('/img/haplab-logo.png')} alt="Haplab" />
            </a>
            <div className="sc-sisterGrid">
              <a href="https://sidecar.haplab.com/" className="sc-sisterCard sc-sisterCardGreen sc-sisterCardCurrent">
                <div className="sc-sisterLogoWrapper">
                  <img src={useBaseUrl('/img/sidecar-logo.png')} alt="Sidecar" className="sc-sisterLogo" />
                </div>
                <p>You might never open your editor again.</p>
              </a>
              <a href="https://betamax.haplab.com/" className="sc-sisterCard sc-sisterCardBlue">
                <div className="sc-sisterLogoWrapper">
                  <img src={useBaseUrl('/img/betamax-logo-fuzzy.png')} alt="Betamax" className="sc-sisterLogo" />
                </div>
                <p>Record anything you see in your terminal.</p>
              </a>
              <a href="https://td.haplab.com/" className="sc-sisterCard sc-sisterCardPurple">
                <div className="sc-sisterLogoWrapper">
                  <img src={useBaseUrl('/img/td-logo.png')} alt="td" className="sc-sisterLogo" />
                </div>
                <p>Task management for AI-assisted development.</p>
              </a>
              <a href="https://nightshift.haplab.com/" className="sc-sisterCard sc-sisterCardAmber">
                <div className="sc-sisterLogoWrapper">
                  <img src={useBaseUrl('/img/nightshift-logo.png')} alt="Nightshift" className="sc-sisterLogo" />
                </div>
                <p>It finds what you forgot to look for.</p>
              </a>
            </div>
          </div>
        </section>
      </main>
        {showInstallMethods && (
          <div className="sc-modal-overlay" onClick={() => setShowInstallMethods(false)}>
            <div className="sc-modal" onClick={(e) => e.stopPropagation()}>
              <div className="sc-modal-header">
                <h3>Install Sidecar</h3>
                <button className="sc-modal-close" onClick={() => setShowInstallMethods(false)}>×</button>
              </div>
              <div className="sc-modal-body">
                <div className="sc-install-method">
                  <h4>Setup Script</h4>
                  <div className="sc-codeBlock sc-installBlock">
                    <div className="sc-installCommand">
                      <span className="sc-lineBlue">$ </span>
                      <span>{INSTALL_COMMAND}</span>
                    </div>
                  </div>
                </div>
                <div className="sc-install-method">
                  <h4>Homebrew</h4>
                  <div className="sc-codeBlock sc-installBlock">
                    <div className="sc-installCommand">
                      <span className="sc-lineBlue">$ </span>
                      <span>{BREW_COMMAND}</span>
                    </div>
                  </div>
                </div>
                <div className="sc-install-method">
                  <h4>Binary Download</h4>
                  <p className="sc-install-desc">Download pre-built binaries from <a href="https://github.com/marcus/sidecar/releases">GitHub Releases</a></p>
                </div>
                <div className="sc-install-method">
                  <h4>From Source</h4>
                  <div className="sc-codeBlock sc-installBlock">
                    <div className="sc-installCommand">
                      <span className="sc-lineBlue">$ </span>
                      <span>go install github.com/marcus/sidecar/cmd/sidecar@latest</span>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        )}
        {showVideoModal && (
          <div className="sc-modal-overlay sc-videoModal-overlay" onClick={() => setShowVideoModal(false)}>
            <div className="sc-videoModal" onClick={(e) => e.stopPropagation()}>
              <button className="sc-videoModal-close" onClick={() => setShowVideoModal(false)} aria-label="Close video">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
              </button>
              <div className="sc-videoModal-frame">
                <iframe
                  src="https://www.youtube.com/embed/5QZxWmDl_tc?autoplay=1&rel=0&modestbranding=1"
                  title="Sidecar Demo"
                  allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture"
                  allowFullScreen
                />
              </div>
            </div>
          </div>
        )}
    </Layout>
  );
}
