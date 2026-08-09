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
Configured artifacts must be regular files reached without symlink traversal;
Sidecar walks their parents from the canonical main-worktree directory using
directory file descriptors. Destination worktrees are staged safely and moved
with descriptor-relative operations into the pinned destination-parent identity,
so a concurrent pathname-to-symlink swap cannot redirect Git into another tree.

The hook runs in the new worktree with `MAIN_WORKTREE`, `SOURCE_WORKTREE`,
`WORKTREE_PATH`, and `WORKTREE_BRANCH` set. If a required setup action fails, Sidecar
keeps the created worktree and does not start an agent. The recovery dialog can retry
setup, open anyway, or delete the newly created worktree after revalidating its exact
path, branch, cleanliness, and HEAD identity. Task metadata is written before
`td start`; a missing or failing `td` command is reported without removing the link.

Immediately after Git adds the worktree, Sidecar writes an inspectable
`pending-creation-*.json` journal in that worktree's collision-safe Sidecar state
directory. If the app changes projects, restarts, or loses the original operation
result before setup finishes, returning to the repository restores the recovery
dialog from this journal without applying the old UI state to another project.
Finishing or opening the worktree removes that journal and syncs its containing
directory. If either step fails, Sidecar keeps the recovery dialog visible rather
than allowing a later restart to misrepresent the operation as newly interrupted.
