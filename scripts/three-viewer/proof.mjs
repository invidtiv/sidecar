// Playwright drives the built reference app; no mocked server or SDK methods.
import { createRequire } from 'node:module';
import { spawn, spawnSync } from 'node:child_process';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { createInterface } from 'node:readline';
import assert from 'node:assert/strict';

const [root, repo, ui, session, socket, outer] = process.argv.slice(2);
const { chromium } = createRequire(`${ui}/package.json`)('@playwright/test');
const env = { ...process.env }; delete env.TMUX; delete env.TMUX_PANE;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
function run(binary, args) {
  const result = spawnSync(binary, args, { env, timeout: 8000, encoding: 'utf8' });
  if (result.error || result.status !== 0) throw new Error(`${binary} ${args.join(' ')}: ${result.error || result.stderr}`);
  return result.stdout.replace(/\r?\n$/, '');
}
const tmux = (...args) => run('tmux', ['-S', socket, ...args]);
const host = (...args) => run('tmux', ['-S', outer, ...args]);
const driver = (...args) => run(`${repo}/scripts/tmux-drive.sh`, args);
async function until(name, predicate, timeout = 5000) {
  const started = performance.now(); let last;
  while (performance.now() - started < timeout) {
    last = await predicate();
    if (last) return Math.round(performance.now() - started);
    await sleep(75);
  }
  throw new Error(`${name} timed out (${timeout}ms); last=${JSON.stringify(last)}`);
}
function rpc(binary, args) {
  const child = spawn(binary, args, { env, stdio: ['pipe', 'pipe', 'pipe'] });
  let pending; let error = ''; let failure;
  child.stderr.on('data', (data) => { error += data; });
  createInterface({ input: child.stdout }).on('line', (line) => {
    const next = pending; pending = undefined;
    if (!next) { failure = new Error(`unexpected RPC response ${line}`); return; }
    clearTimeout(next.timer); next.resolve(JSON.parse(line));
  });
  child.on('exit', (code) => {
    failure = new Error(`${binary} exited ${code}: ${error}`);
    if (pending) { clearTimeout(pending.timer); pending.reject(failure); pending = undefined; }
  });
  return {
    child,
    call(value) {
      if (failure) return Promise.reject(failure);
      assert(!pending, 'one ordered RPC at a time');
      return new Promise((resolve, reject) => {
        pending = { resolve, reject, timer: setTimeout(() => { pending = undefined; reject(new Error(`RPC timeout: ${error}`)); }, 10000) };
        child.stdin.write(`${JSON.stringify(value)}\n`);
      });
    },
    async stop() {
      child.stdin.end();
      await Promise.race([new Promise((resolve) => child.once('exit', resolve)), sleep(2000)]);
      if (child.exitCode === null) child.kill('SIGKILL');
    },
  };
}

const results = { sidecar_ui_commit: run('git', ['-C', ui, 'rev-parse', 'HEAD']), handoffs: [], quiet_windows: [], holder_checks: 0 };
let browser; let ios; let tui;
const transcript = [];
try {
  await until('raw byte sink ready', () => tmux('capture-pane', '-t', session, '-p').includes('THREE_VIEWER_READY'));
  tui = rpc('python3', ['-u', `${repo}/scripts/three-viewer/terminal.py`, 'viewer', outer]);
  await until('PTY client attached', () => host('list-clients', '-F', '#{client_tty}').includes('/dev/'));
  await tui.call({ focused: true });
  driver('keys', '1');
  await until('TUI workspaces', () => host('capture-pane', '-t', 'host', '-p').includes('Three viewer proof'));
  driver('keys', 'Enter');
  await tui.call({ focused: true });
  await sleep(500);
  const dimensions = () => tmux('display-message', '-t', session, '-p', '#{pane_width}x#{pane_height}');
  writeFileSync(`${root}/out/initial.txt`, host('capture-pane', '-t', 'host', '-p'));
  await until('TUI fit its terminal', () => dimensions() !== '80x24', 8000);
  const tuiSize = dimensions();
  results.tui_geometry = tuiSize;
  assert.notEqual(tuiSize, '80x24', 'TUI must fit its actual terminal');
  const holder = () => {
    const fields = tmux('display-message', '-t', session, '-p', '#{@sidecar-owner}\t#{@sidecar-holder-owner}\t#{@sidecar-holder-kind}\t#{@sidecar-holder-label}').split('\t');
    const owner = fields[0].split(':').slice(0, -2).join(':');
    if (!fields[0]) return { kind: '', label: '' };
    if (owner === fields[1] && fields[2]) return { kind: fields[2], label: fields[3] };
    return { kind: 'tui', label: `TUI on ${owner.replace(/-\d+$/, '')}` };
  };
  assert.equal(holder().kind, 'tui');
  await tui.call({ focused: false });
  await until('TUI blur release', () => holder().kind === '');
  ios = rpc(`${root}/ios`, [`${root}/sidecar`, `${root}/config/config.json`, session]);
  await until('iOS first frame', async () => (await ios.call({ type: 'state' })).ready);
  await ios.call({ type: 'presence', presence: { focused: false, visible: true, idle_ms: 0, columns: 73, rows: 19 } });
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1180, height: 800 } });
  page.setDefaultTimeout(8000);
  let browserHolder;
  page.on('pageerror', (error) => transcript.push({ pageerror: error.message, stack: error.stack }));
  page.on('websocket', (ws) => {
    if (!ws.url().includes('/terminal')) return;
    ws.on('framesent', (frame) => { const r = JSON.parse(String(frame.payload)); if (r.type !== 'hello' && r.type !== 'resolve') transcript.push({ sent: r }); });
    ws.on('framereceived', (frame) => { const r = JSON.parse(String(frame.payload)); if (r.holder) browserHolder = r.holder; if (r.type !== 'frame') transcript.push({ received: r }); });
  });
  await page.goto(readFileSync(`${root}/pair-url`, 'utf8').trim());
  await page.waitForURL((url) => url.pathname === '/');
  await page.getByRole('button', { name: /Three viewer proof/ }).click();
  const terminal = page.locator('sidecar-terminal');
  const state = async () => ({ control: (await terminal.getAttribute('data-size')) === 'here' ? 'controlling' : 'viewing', holder: browserHolder });
  const focused = async (value) => {
    await page.evaluate((active) => {
      Object.defineProperty(document, 'hasFocus', { configurable: true, value: () => active });
      window.dispatchEvent(new Event(active ? 'focus' : 'blur'));
    }, value);
  };
  await until('browser connected and controlling', async () => (await state())?.control === 'controlling', 10000);
  const browserSize = dimensions(); results.browser_geometry = browserSize;
  assert.notEqual(browserSize, tuiSize); assert.notEqual(browserSize, '73x19');
  const browserLabel = holder().label;
  const matchHolders = async (kind) => {
    const actual = holder(); assert.equal(actual.kind, kind);
    await until(`all protocol holders match ${kind}`, async () => {
      const a = (await ios.call({ type: 'state' })).holder;
      const b = (await state()).holder;
      return a?.kind === actual.kind && a?.label === actual.label && b?.kind === actual.kind && b?.label === actual.label;
    }, 3500);
    if (kind !== 'tui') {
      await until(`TUI names holder ${actual.label}`, () => host('capture-pane', '-t', 'host', '-p').includes(`sized for ${actual.label}`), 3500);
    }
    if (kind !== 'browser' && await page.evaluate(() => document.hasFocus())) {
      await until(`browser hint names ${actual.label}`, async () => (await terminal.locator('[part="hint"]').allTextContents()).includes(`Sized for ${actual.label}`), 3500);
      results.browser_hint_checks = (results.browser_hint_checks ?? 0) + 1;
    }
    results.holder_checks++;
  };
  await matchHolders('browser');

  await focused(false);
  results.browser_blur_ms = await until('browser blur releases', () => holder().kind === '');
  const presence = (active, idle = 0) => ios.call({ type: 'presence', presence: { focused: active, visible: true, idle_ms: idle, columns: 73, rows: 19 } });
  let started = performance.now(); await presence(true);
  await until('iOS fit', () => dimensions() === '73x19');
  results.handoffs.push({ to: 'ios', ms: Math.round(performance.now() - started), geometry: dimensions() });
  await matchHolders('ios');
  started = performance.now(); await presence(false);
  results.ios_blur_ms = await until('iOS blur releases', () => holder().kind === '');
  await tui.call({ focused: true });
  await until('TUI fit after focus', () => dimensions() === tuiSize && holder().kind === 'tui', 6000);
  results.handoffs.push({ to: 'tui', ms: Math.round(performance.now() - started), geometry: dimensions() });
  await matchHolders('tui');

  // Every send lands in one raw sink. Full byte equality proves no loss,
  // duplication or cross-viewer reordering, independently of echoed frames.
  let expected = '';
  for (let round = 0; round < 4; round++) {
    for (const viewer of ['browser', 'ios', 'tui']) {
      const text = `${viewer[0]}${round}.`;
      const start = performance.now();
      if (viewer !== 'tui') await tui.call({ focused: false });
      if (viewer !== 'browser') await focused(false);
      if (viewer !== 'ios') await presence(false);
      if (viewer === 'browser') {
        await focused(true);
        await terminal.evaluate((element) => element.focus());
        await page.keyboard.type(text, { delay: 15 });
      } else if (viewer === 'ios') {
        await presence(true);
        await ios.call({ type: 'input', text });
      } else {
        await tui.call({ focused: true });
        driver('type', text);
      }
      expected += text;
      await until(`${viewer} exact input`, () => existsSync(`${root}/received`) && readFileSync(`${root}/received`, 'utf8') === expected);
      await until(`${viewer} holder and size`, () => holder().kind === viewer && dimensions() === ({ browser: browserSize, ios: '73x19', tui: tuiSize })[viewer]);
      results.handoffs.push({ to: viewer, ms: Math.round(performance.now() - start), bytes: text.length, geometry: dimensions() });
      await matchHolders(viewer);
    }
  }
  results.expected_bytes = Buffer.byteLength(expected);
  results.received_bytes = readFileSync(`${root}/received`).length;
  results.input_exact = readFileSync(`${root}/received`, 'utf8') === expected;

  // Keep a focused iOS peer abandoned and heartbeating past the five-second
  // input margin; activate the browser without delivering terminal bytes.
  await tui.call({ focused: false }); await presence(true);
  await until('idle test iOS holder', () => holder().kind === 'ios');
  // Wait until the idle duration advertised in the owner's refreshed token is
  // beyond the margin. A duration at the last heartbeat is a lower bound on
  // current idle, not a cross-machine timestamp.
  const abandonedAt = performance.now();
  await until('owner advertises at least six idle seconds', () => {
    const token = tmux('display-message', '-t', session, '-p', '#{@sidecar-owner}');
    return Number(token.split(':').at(-1)) >= 6;
  }, 12000);
  const abandonedMS = Math.round(performance.now() - abandonedAt);
  started = performance.now(); await focused(true);
  await until('idle preemption by browser presence', () => holder().kind === 'browser' && dimensions() === browserSize, 6500);
  results.idle_preemption = { abandoned_ms: abandonedMS, active_to_holder_ms: Math.round(performance.now() - started), browser_label: browserLabel };
  await matchHolders('browser');

  // Deliberate input also takes over while both viewers remain focused.
  const takeoverStart = performance.now();
  await ios.call({ type: 'input', text: 'I!' }); expected += 'I!';
  await until('iOS input claims before delivery', () => holder().kind === 'ios' && dimensions() === '73x19' && readFileSync(`${root}/received`, 'utf8') === expected);
  await matchHolders('ios');
  await terminal.evaluate((element) => element.focus());
  await page.keyboard.type('B!', { delay: 15 }); expected += 'B!';
  await until('browser input claims before delivery', () => holder().kind === 'browser' && dimensions() === browserSize && readFileSync(`${root}/received`, 'utf8') === expected);
  await matchHolders('browser');
  results.focused_input_takeovers = { count: 2, ms: Math.round(performance.now() - takeoverStart) };
  results.expected_bytes = Buffer.byteLength(expected);
  results.received_bytes = readFileSync(`${root}/received`).length;
  results.input_exact = readFileSync(`${root}/received`, 'utf8') === expected;

  // The command hook sees resize-window from subprocesses AND control actors.
  tmux('set-hook', '-g', 'after-resize-window', `run-shell "printf 'resize\\n' >> '${root}/resizes'"`);
  writeFileSync(`${root}/resizes`, '');
  const quietStart = performance.now();
  while (performance.now() - quietStart < 6500) {
    assert.equal(holder().kind, 'browser'); assert.equal(dimensions(), browserSize);
    await sleep(200);
  }
  const calls = readFileSync(`${root}/resizes`, 'utf8').trim().split('\n').filter(Boolean).length;
  results.quiet_windows.push({ ms: Math.round(performance.now() - quietStart), resize_window_calls: calls });
  assert.equal(calls, 0, 'settled viewers must not resize or ping-pong');
  // Prove the hook actually observes the command path, including same-size calls.
  tmux('resize-window', '-t', session, '-x', browserSize.split('x')[0], '-y', browserSize.split('x')[1]);
  await until('resize hook positive control', () => readFileSync(`${root}/resizes`, 'utf8').includes('resize'));
  results.resize_hook_positive_control = true;
  results.browser_pageerrors = transcript.filter((item) => item.pageerror).map((item) => item.pageerror);
  await page.screenshot({ path: `${root}/out/browser.png` });
  writeFileSync(`${root}/out/tui.txt`, host('capture-pane', '-t', 'host', '-p'));
  await focused(false); await presence(false);
  await until('all viewers blurred releases', () => holder().kind === '');
  writeFileSync(`${root}/out/results.json`, JSON.stringify(results, null, 2) + '\n');
  console.log(JSON.stringify(results, null, 2));
} catch (error) {
  writeFileSync(`${root}/out/failure.txt`, `${error.stack}\n${host('list-clients', '-F', '#{client_flags}')}\n${tmux('display-message', '-t', session, '-p', '#{pane_width}x#{pane_height}\t#{@sidecar-owner}')}\n${host('capture-pane', '-t', 'host', '-p')}`);
  throw error;
} finally {
  writeFileSync(`${root}/out/browser-protocol.json`, JSON.stringify(transcript, null, 2) + '\n');
  if (browser) await browser.close();
  if (ios) await ios.stop();
  if (tui) await tui.stop();
}
