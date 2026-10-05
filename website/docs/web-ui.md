---
title: Use the web UI
description: Install a built Sidecar UI and run its API service at login.
---

# Use the web UI

Sidecar can serve a built web UI and its API from one always-on per-user service. On macOS it uses launchd; on Linux it uses a systemd user service and socket. The service starts at login and leaves tmux sessions alone.

Install [Sidecar](intro.md#quick-install), and make sure your version supports `sidecar api service install --ui`. The reference UI and its SDK are private for now, so the reference path below requires access to the `sidecar-ui` repository. Without that access, you can [build your own UI](build-your-own-ui.md) against the public API.

## Install the reference UI

With Node 22 or newer and pnpm 10 installed, run these commands from your `sidecar-ui` checkout:

```sh
pnpm install --frozen-lockfile
pnpm run install-local --service
sidecar api open
```

The installer builds the app, copies its static output into `~/.local/share/sidecar/ui/`, atomically points `current` at the new build, and retains the three newest builds. `--service` configures Sidecar to serve that `current` path and installs or restarts the API service. Omit `--service` to install only the files and print the service command for later. Set `SIDECAR_UI_HOME` to choose another installation directory. Repeat the installer after updating the UI.

`sidecar api open` opens a one-use pairing URL in your browser. The normal service address is `http://127.0.0.1:7861`. Keep using the same exact browser origin so your pairing registration can renew after server restarts.

## Use another built UI

Any static UI directory containing `index.html` can be served:

```sh
sidecar api service install --ui /absolute/path/to/ui
sidecar api open
sidecar api service status
sidecar api status --json
```

`--ui` validates the directory, saves its absolute path as `api.uiDir`, and preserves other configuration settings. A symlink such as `current` stays a symlink path in config, so it can point at a new build without another config edit. Omitting `--ui` keeps the saved setting. Service status shows the configured UI directory; API status shows the directory used by the running server. Config edits take effect on the next server start.

To return to an API-only service, run `sidecar api service install --ui ""`. Its root page explains how to add a UI. To remove the service, run `sidecar api service uninstall`; this preserves Sidecar state and pairing registrations.

Run service commands as your login user without sudo. Stop any foreground `sidecar api serve` before installing the service. On macOS, choose either `sidecar api service` or `brew services` to manage this job. If Homebrew currently manages it, stop that job before switching. On Linux, use `sidecar api service`; it needs a running systemd user manager and does not enable lingering.

See the [UI API service reference](https://github.com/marcus/sidecar/blob/main/docs/reference/ui-api.md#per-user-background-service) for service paths, logs, restart behavior, and JSON status fields.
