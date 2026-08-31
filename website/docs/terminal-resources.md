---
sidebar_position: 4
title: Terminal Resource Providers
---

# Terminal Resource Providers

Integrate external issue trackers, ticketing systems, and internal developer platforms (Jira, Linear, GitHub Issues) directly into Sidecar terminal panes.

## Overview

Terminal Resource Providers allow Sidecar to recognize external ticket identifiers (such as `PROJ-1234` or `LIN-567`) in terminal output, agent logs, and Markdown files. When recognized, clicking or selecting the identifier opens a rich **Resource Card** in an adjacent pane, displaying title, status, assignee, description, and custom fields without opening a web browser.

```
┌──────────────────────────────────────┬──────────────────────────────────────────┐
│ Terminal Output                      │ Resource Card: JIRA-4812                 │
│                                      │                                          │
│ $ git commit -m "fix(auth): JIRA-4812"│ Implement OAuth2 PKCE token exchange     │
│ [main abc1234] fix(auth): JIRA-4812  │                                          │
│  2 files changed, 45 insertions(+)   │ Status: IN PROGRESS · Priority: High     │
│                                      │ Assignee: Marcus · Reporter: Sarah       │
│                                      │                                          │
│                                      │ Description:                             │
│                                      │ Replace standard authorization code flow │
│                                      │ with PKCE verification for mobile clients│
└──────────────────────────────────────┴──────────────────────────────────────────┘
```

## Configuring Resource Providers

Configure resource providers in `~/.config/sidecar/config.json` under `terminalResources`:

```json
{
  "terminalResources": {
    "enabled": true,
    "providers": {
      "jira-work": {
        "command": "sidecar-jira-provider",
        "pattern": "[A-Z]{2,10}-[0-9]+",
        "claimHosts": [
          "mycompany.atlassian.net"
        ],
        "passEnv": [
          "JIRA_API_TOKEN",
          "JIRA_BASE_URL"
        ],
        "timeout": "5s"
      }
    }
  }
}
```

### Configuration Options

| Field | Type | Description |
|-------|------|-------------|
| `command` | `string` | The executable path or binary on `$PATH` implementing the provider protocol |
| `pattern` | `string` | Regular expression for matching ticket keys in plain text and terminal streams |
| `claimHosts` | `array` | List of hostname patterns. When a Markdown link target matches a claimed host, Sidecar opens the Resource card while preserving the hyperlink |
| `passEnv` | `array` | Whitelist of environment variable names passed to the provider process |
| `timeout` | `string` | Timeout for resolution queries (e.g. `"3s"`, `"5s"`) |

## Provider Protocol

A resource provider is an external executable that communicates with Sidecar over standard input/output using JSON. Providers implement two primary methods:

### 1. `describe`

Sidecar invokes `<command> describe` at startup to inspect provider capabilities:

```bash
sidecar-jira-provider describe
```

**Expected JSON Response:**
```json
{
  "name": "Jira Integration",
  "version": "1.0.0",
  "pattern": "[A-Z]{2,10}-[0-9]+",
  "capabilities": ["card", "url"]
}
```

### 2. `resolve`

Sidecar invokes `<command> resolve <locator>` when a ticket key is activated:

```bash
sidecar-jira-provider resolve PROJ-1234
```

**Expected JSON Response:**
```json
{
  "id": "PROJ-1234",
  "title": "Fix OAuth token expiration bug",
  "status": "In Progress",
  "priority": "High",
  "assignee": "Alex",
  "body": "Tokens are expiring after 5 minutes instead of 60 minutes.",
  "url": "https://mycompany.atlassian.net/browse/PROJ-1234",
  "fields": [
    {"label": "Component", "value": "Auth Service"},
    {"label": "Fix Version", "value": "2.4.0"}
  ]
}
```

## Administration CLI (`sidecar terminal-links`)

Sidecar includes a CLI tool for verifying and debugging configured resource providers.

### 1. `sidecar terminal-links list`

List all configured providers and verify their executable status:

```bash
sidecar terminal-links list
sidecar terminal-links list --describe --json
```

### 2. `sidecar terminal-links check`

Test a provider instance and optionally resolve a sample locator:

```bash
# Check executable resolution and describe response
sidecar terminal-links check jira-work

# Test full resolution against an actual ticket
sidecar terminal-links check jira-work --resolve PROJ-1234 --json
```
