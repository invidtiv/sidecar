import React, {createContext, useCallback, useContext, useMemo, useRef, useState} from 'react';
import clsx from 'clsx';
import styles from './Tui.module.css';
import {DEFAULT_THEME, findTheme, themeVars} from './theme';

/*
 * Primitives for the Sidecar mockups. They are deliberately thin: a mockup is
 * written as the app's own rows and glyphs, not as a bespoke component per
 * screen, so a change to the real chrome is one edit here.
 */

const ThemeCtx = createContext(DEFAULT_THEME);

export function useTuiTheme() {
  return useContext(ThemeCtx);
}

export function TuiWindow({
  theme = DEFAULT_THEME,
  titlebar,
  children,
  className,
  style,
  ...rest
}) {
  const vars = useMemo(() => themeVars(findTheme(theme)), [theme]);
  return (
    <ThemeCtx.Provider value={theme}>
      <div
        className={clsx(styles.window, className)}
        style={{...vars, ...style}}
        {...rest}>
        {titlebar ? <TitleBar {...titlebar} /> : null}
        {children}
      </div>
    </ThemeCtx.Provider>
  );
}

function TitleBar({label, right}) {
  return (
    <div className={styles.titlebar}>
      <span className={styles.dot} style={{background: '#e05c52'}} />
      <span className={styles.dot} style={{background: '#dfae3c'}} />
      <span className={styles.dot} style={{background: '#3fa858'}} />
      <span className={styles.titlebarLabel}>{label}</span>
      {right ? <span className={styles.titlebarRight}>{right}</span> : null}
    </div>
  );
}

/**
 * The app header: brand, the global tab scope, the project tab scope, the
 * project selector chip, and the settings gear.
 */
export function TuiHeader({
  globalTabs = [],
  projectTabs = [],
  active,
  onSelect,
  project,
}) {
  const renderTab = (tab) => {
    const isActive = tab.id === active;
    const interactive = typeof onSelect === 'function';
    return (
      <button
        key={tab.id}
        type="button"
        className={clsx(styles.tab, isActive && styles.tabActive)}
        aria-pressed={isActive}
        aria-label={`${tab.label} tab`}
        tabIndex={interactive ? 0 : -1}
        onClick={interactive ? () => onSelect(tab.id) : undefined}>
        {tab.label}
      </button>
    );
  };

  return (
    <div className={styles.header} role="tablist">
      <span className={styles.brand}>◱ Sidecar</span>
      <span className={styles.headerRule}>│</span>
      <div className={styles.tabGroup}>{globalTabs.map(renderTab)}</div>
      <span className={styles.headerSpacer} />
      <div className={styles.tabGroup}>{projectTabs.map(renderTab)}</div>
      {project ? <span className={styles.projectChip}>{project} ▾</span> : null}
      <span className={styles.gear}>⚙</span>
    </div>
  );
}

export function usePaneResize(initialPercent = 30, min = 15, max = 75) {
  const [percent, setPercent] = useState(initialPercent);
  const containerRef = useRef(null);

  const onHandleResize = useCallback(
    (_deltaX, currentX) => {
      if (!containerRef.current) return;
      const rect = containerRef.current.getBoundingClientRect();
      if (!rect.width) return;
      const pct = ((currentX - rect.left) / rect.width) * 100;
      setPercent(Math.max(min, Math.min(max, pct)));
    },
    [min, max],
  );

  return {percent, containerRef, onHandleResize, setPercent};
}

export function TuiPanes({children, className, style, innerRef, ...rest}) {
  return (
    <div
      ref={innerRef}
      className={clsx(styles.panes, className)}
      style={style}
      {...rest}>
      {children}
    </div>
  );
}

export function TuiHandle({onResize, className, style, ...rest}) {
  const [isDragging, setIsDragging] = useState(false);

  const handlePointerDown = useCallback(
    (e) => {
      if (!onResize) return;
      e.preventDefault();
      setIsDragging(true);
      const startX = e.clientX;
      const handleEl = e.currentTarget;
      try {
        handleEl.setPointerCapture?.(e.pointerId);
      } catch (_) {}

      const onPointerMove = (moveEv) => {
        const deltaX = moveEv.clientX - startX;
        onResize(deltaX, moveEv.clientX);
      };

      const onPointerUp = (upEv) => {
        setIsDragging(false);
        try {
          handleEl.releasePointerCapture?.(upEv.pointerId);
        } catch (_) {}
        window.removeEventListener('pointermove', onPointerMove);
        window.removeEventListener('pointerup', onPointerUp);
      };

      window.addEventListener('pointermove', onPointerMove);
      window.addEventListener('pointerup', onPointerUp);
    },
    [onResize],
  );

  return (
    <div
      className={clsx(
        styles.handle,
        isDragging && styles.handleActive,
        onResize && styles.handleInteractive,
        className,
      )}
      onPointerDown={handlePointerDown}
      style={style}
      aria-hidden="true"
      {...rest}
    />
  );
}

export function TuiPane({
  title,
  titleDim,
  chips,
  focused,
  grow = 1,
  basis,
  children,
}) {
  return (
    <div
      className={clsx(styles.pane, focused && styles.paneFocused)}
      style={{flexGrow: grow, flexBasis: basis}}>
      <div className={styles.paneInner}>
        {title !== undefined ? (
          <div className={styles.paneHeader}>
            <span className={styles.paneTitle}>{title}</span>
            {titleDim ? (
              <span className={styles.paneTitleDim}>{titleDim}</span>
            ) : null}
            {chips ? (
              <span className={styles.paneChips}>
                {chips.map((c) => (
                  <span key={c} className={styles.chip}>
                    {c}
                  </span>
                ))}
              </span>
            ) : null}
          </div>
        ) : null}
        <div className={styles.paneBody}>{children}</div>
      </div>
    </div>
  );
}

export function TuiFooter({keys = [], clock = '21:02'}) {
  return (
    <div className={styles.footer}>
      {keys.map(([cap, label]) => (
        <span key={cap + label} className={styles.key}>
          <span className={styles.keyCap}>{cap}</span>
          {label}
        </span>
      ))}
      <span className={styles.clock}>↻ {clock}</span>
    </div>
  );
}

/* ------------------------------------------------------------ row helpers */

export function Row({selected, onClick, className, children, ...rest}) {
  return (
    <div
      className={clsx(
        styles.row,
        selected && styles.rowSelected,
        onClick && styles.rowClickable,
        className,
      )}
      onClick={onClick}
      {...rest}>
      {children}
    </div>
  );
}

export function Spacer() {
  return <span className={styles.spacerCol} />;
}

export function Rule() {
  return (
    <div className={styles.row}>
      <span className={styles.rule}>{'─'.repeat(240)}</span>
    </div>
  );
}

/** A Sidecar modal over the pane area: dimmed backdrop, one raised box. */
export function TuiModal({title, children, keys = []}) {
  return (
    <div className={styles.modalHost}>
      <div className={styles.modal}>
        <div className={styles.modalInner}>
          <div className={styles.modalTitle}>{title}</div>
          {children}
          {keys.length ? (
            <div className={styles.modalKeys}>
              {keys.map(([cap, label]) => (
                <span key={cap} className={styles.key}>
                  <span className={styles.keyCap}>{cap}</span>
                  {label}
                </span>
              ))}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

export function Field({label, value, active, hint}) {
  return (
    <div className={styles.field}>
      <span className={styles.fieldLabel}>{label}</span>
      <span
        className={clsx(styles.fieldBox, active && styles.fieldBoxActive)}>
        {value}
        {active ? <Cursor /> : null}
      </span>
      {hint ? <span className={styles.subtle}>{hint}</span> : null}
    </div>
  );
}

export function Cursor() {
  return <span className={styles.cursor}>&nbsp;</span>;
}

export const tui = styles;
