import React, {useCallback, useEffect, useRef, useState} from 'react';
import {THEMES, DEFAULT_THEME, findTheme} from './Tui/theme';

const STORAGE_KEY = 'sidecar-theme';

/**
 * The site's accent follows whichever of the app's 21 themes you pick, the same
 * way pressing `#` recolours the terminal. Backgrounds stay put — the page is
 * not pretending to be the app, it is wearing its palette.
 */
function applyTheme(name) {
  const t = findTheme(name);
  if (!t || !t.colors) return;
  const root = document.documentElement;
  root.setAttribute('data-sidecar-theme', t.name);
  root.style.setProperty('--sc-accent', t.colors.Primary);
  root.style.setProperty('--sc-accent-dim', t.colors.TextSecondary);
  root.style.setProperty('--sc-green', t.colors.Success);
  root.style.setProperty('--sc-teal', t.colors.Info);
  root.style.setProperty('--sc-blue', t.colors.Link || t.colors.Info);
  root.style.setProperty('--sc-pink', t.colors.LaneDone || t.colors.Error);
  root.style.setProperty('--ifm-color-primary', t.colors.Primary);
  root.style.setProperty('--ifm-link-color', t.colors.Primary);
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent('sidecar-theme-change', {detail: t.name}));
  }
}

export default function ThemeSwitcherNavbarItem() {
  const [theme, setTheme] = useState(DEFAULT_THEME);
  const [open, setOpen] = useState(false);
  const ref = useRef(null);

  useEffect(() => {
    const stored = localStorage.getItem(STORAGE_KEY);
    const initial = stored && findTheme(stored).name === stored ? stored : DEFAULT_THEME;
    setTheme(initial);
    applyTheme(initial);

    const onDown = (e) => {
      if (ref.current && !ref.current.contains(e.target)) setOpen(false);
    };
    const onKey = (e) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, []);

  const choose = useCallback((name) => {
    setTheme(name);
    localStorage.setItem(STORAGE_KEY, name);
    applyTheme(name);
    setOpen(false);
  }, []);

  const current = findTheme(theme);

  return (
    <div
      className={`navbar__item dropdown dropdown--right dropdown--nocaret ${open ? 'dropdown--show' : ''}`}
      ref={ref}
      style={{position: 'relative'}}>
      <button
        type="button"
        className="navbar__link"
        aria-haspopup="true"
        aria-expanded={open}
        onClick={(e) => {
          e.stopPropagation();
          setOpen((v) => !v);
        }}
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          fontFamily: 'var(--sc-mono)',
          fontSize: 12.5,
          background: 'none',
          border: 0,
          cursor: 'pointer',
          color: 'var(--ifm-navbar-link-color)',
          padding: '0 12px',
          pointerEvents: 'auto',
          userSelect: 'none',
        }}>
        <Swatch colors={current.colors} />
        <span>{current.displayName}</span>
        <span
          style={{
            fontSize: 10,
            opacity: 0.6,
            transition: 'transform 150ms ease',
            transform: open ? 'rotate(180deg)' : 'none',
            display: 'inline-block',
          }}>
          ▼
        </span>
      </button>

      {open && (
        <div
          className="themeSwatchGrid"
          style={{
            position: 'absolute',
            top: 'calc(100% + 4px)',
            right: 0,
            zIndex: 300,
            width: 340,
            boxShadow: '0 16px 36px rgba(0, 0, 0, 0.8)',
          }}>
          {THEMES.map((t) => {
            const active = t.name === theme;
            return (
              <button
                key={t.name}
                type="button"
                onClick={() => choose(t.name)}
                aria-pressed={active}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 9,
                  padding: '9px 12px',
                  border: 0,
                  cursor: 'pointer',
                  textAlign: 'left',
                  fontFamily: 'var(--sc-mono)',
                  fontSize: 12,
                  background: active ? 'var(--sc-panel)' : 'var(--sc-canvas-2)',
                  color: active ? 'var(--sc-text)' : 'var(--sc-text-3)',
                  boxShadow: active ? `inset 2px 0 0 ${t.colors.Primary}` : 'none',
                  transition: 'background 120ms ease, color 120ms ease',
                }}>
                <Swatch colors={t.colors} />
                <span>{t.displayName}</span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

function Swatch({colors}) {
  return (
    <span
      aria-hidden="true"
      style={{
        display: 'flex',
        flex: 'none',
        width: 24,
        height: 10,
        outline: '1px solid rgba(255,255,255,.14)',
      }}>
      <span style={{flex: 1, background: colors.Primary}} />
      <span style={{flex: 1, background: colors.Info}} />
      <span style={{flex: 1, background: colors.Success}} />
      <span style={{flex: 1, background: colors.Error}} />
    </span>
  );
}
