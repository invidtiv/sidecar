# Worktree creation and setup

Sidecar validates a new branch with Git and resolves the selected source to an exact
commit before it changes the repository. The confirmation shows the source ref and
OID, source worktree, destination path, branch, task, remote policy, and every setup
artifact Sidecar found. Worktree creation does not push a remote branch.

Environment copying and the setup hook are selected in that confirmation. Defaults
live under `plugins.workspace.worktreeSetup` in `~/.config/sidecar/config.json`:

```json
{
  "plugins": {
    "workspace": {
      "worktreeSetup": {
        "copyEnvFiles": true,
        "envFiles": [".env", ".env.local", ".env.development", ".env.development.local"],
        "runHook": true,
        "hookPath": ".worktree-setup.sh",
        "hookRequired": true
      }
    }
  }
}
```

A project entry may contain its own `worktreeSetup` object to replace the global
policy for that project. Relative env and hook paths are resolved from the canonical
main worktree, even when Sidecar is opened in a linked worktree or repository
subdirectory. Sidecar never logs copied file contents or setup-hook output.

The hook runs in the new worktree with `MAIN_WORKTREE`, `SOURCE_WORKTREE`,
`WORKTREE_PATH`, and `WORKTREE_BRANCH` set. If a required setup action fails, Sidecar
keeps the created worktree and does not start an agent. The recovery dialog can retry
setup, open anyway, or delete the newly created worktree after revalidating its exact
path, branch, cleanliness, and HEAD identity. Task metadata is written before
`td start`; a missing or failing `td` command is reported without removing the link.
