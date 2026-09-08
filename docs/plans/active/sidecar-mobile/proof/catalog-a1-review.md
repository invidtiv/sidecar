# M1-A1 independent catalog review

Review task: `td-8e1dc1`. Reviewer: `/root/apple_readiness`, context `mobile-catalog-review`, session `ses_23c2be`. Implementation owner: `/root/terminal_seed_spike`. This review covers the local catalog slice in `codex/mobile-catalog` against base `cf77be7a`; the reviewer did not author its Go implementation or tests. The parent cross-host task `td-ffe174` remains open.

## Verdict

Approved for the bounded M1-A1 local catalog/query/identity contract, with no remaining blocking finding. The independent focused suite and real cold-history CLI rerun pass against the final frozen candidate. This approval does not complete the parent cross-host M1 task or the physical M0 gate.

## Findings and repairs

The initial catalog compared its inventory row ID with the managed-shell resolver's different workspace identity and therefore marked real ready candidates stale. The repair keeps the inventory row ID for catalog selection, compares canonical project/kind/session/pane/durable-creation facts at resolution, and returns the exact terminal identity as `expected_target`. The service revalidates that identity before issuing a target handle. The initial fake resolver test alone did not establish the real selection journey; the final evidence includes an actual ready row resolved by the production service.

The initial query filtered candidate states and computed its content generation before the resolver's final verdict. That hid final unsupported/stale/ambiguous states and could keep a generation unchanged when the server incarnation changed. Authorization now precedes state filtering and generation. Focused regressions exercise the final capability/refusal states, a changed canonical project identity, and a changed tmux server incarnation.

An independent read-only CLI repetition found that an unchanged idle shell acquired a new `changed_at` and catalog generation on each process invocation. The provider discarded the shared activity history and recreated its tracker. Evidence of that original failure is `/tmp/sidecar-mobile-catalog-review-repeat.json`. The provider now loads the existing `activitystore` and retains its collector across service requests. When no persisted tracker exists, the current state is known but its age is not: `changed_at` stays absent until this provider observes an actual transition under the shared tracker's state/evidence rules. This avoids writing a competing history file or manufacturing activity times on cold headless reads.

Count bounds alone did not bound the JSONL response because catalog strings come from provider/manifest data. The shared query path now refuses an oversized serialized catalog with 64 KiB reserved for its JSONL envelope and bounded correlation fields. Both CLI and service inherit that refusal before emitting. The regression uses control characters whose JSON escaping exceeds the advertised line limit, so it checks encoded size rather than only the original string length.

## Independent verification

The reviewer ran the focused catalog, selection-identity, active-stream refusal, cold-history, transition, shared-projection, CLI-contract, and canonical-corpus tests across `internal/mobile`, `internal/mobileproto`, `internal/cli`, and `internal/workspacecatalog`. All four packages passed with `go test ./internal/mobile ./internal/mobileproto ./internal/cli ./internal/workspacecatalog -run 'Test(Catalog|ResolveRefusesCatalog|Sessions|MobileCatalogProvider|ProjectItem|ProductionCatalogCorpus|ParseMobileCatalog|MobileSessionsCommand)' -count=1`. Output is retained in `/tmp/sidecar-mobile-catalog-independent-focused.log`, SHA-256 `8c7c9f820430daa836a70c819c3155645525b14bdb4075902b0a90f108dc0be3`. This was a targeted rerun of the changed contracts; it does not claim a repeated full-repository test run.

The reviewer also ran two actual CLI processes against the private terminal, with a separate temporary state copy and no `agent-activity.json`. Their observation timestamps advanced by 333 ms while the ready row, exact expected target identity, and catalog generation stayed identical. `changed_at` remained absent and no history file was created. Hashes of the original disposable project's metadata remained unchanged. Evidence is `/tmp/sidecar-mobile-catalog-independent-review/result.json`, SHA-256 `e4705a5a85c27d20bf7370b50917c0cd2de7fd1b26fb5122c0c65f7b4d18c174`, against candidate binary SHA-256 `2b4fcf840281a65bf6a9f13c04325eb6628aa6133208e8ab5c0a34a415b69653`. This proof sent no terminal input and created no tmux session.

The final owner manifest `/tmp/sidecar-mobile-catalog-a1-source-manifest.json` pins 18 files against base `cf77be7a5f8ce66b3601f89195667d6d26e2bfc8`; its SHA-256 is `fd9417eb64946af3c680e82a4915c78eeed6d580d867eed8d1f5dbba2b30cb4c`. The reviewer verified every file's byte count and hash. This reviewer-owned note is explicitly excluded from that manifest. The canonical synthetic corpus remains `082ecc241e76ad5b2733fbad591e0508d248e58ad3d8ac38140b1377f6954373`.

The reviewer read the final production `hello → sessions → resolve` transcript and confirmed that its ready row's expected identity exactly matched the returned target, with a successful service exit and empty stderr. Its proof file is `/tmp/sidecar-mobile-catalog-a1-service-proof.json`, SHA-256 `59543d9b8ab895181818a7ba785383e7d13d34336d410fabdc50f922cb152fba`. The companion persisted-history repetition is `/tmp/sidecar-mobile-catalog-a1-repeat.json`, SHA-256 `49192d44536dc409d9a842b6e9896482b474cbf4d0bb56988b89c58251a80627`; both snapshots keep the same recorded change time and identity while observation time advances.

The reviewer inspected the owner's final command/result artifact `/tmp/sidecar-mobile-catalog-a1-owner-gates.json`, SHA-256 `4459bd99157d17576e2603f6821fb9fb8658dfdd7ac2accdb8f17c4f061bb9d0`. It records passing full tests for the changed mobile/protocol/shared-projection packages and desktop Overview, the mobile/shared-projection race suite, the full CLI package suite, `go build ./...`, and lint with zero issues. Those broader checks were executed by the implementation owner; the separate reviewer rerun is identified above. The final working diff also passes `git diff --check`.

## Reviewed boundaries

`workspacecatalog.ProjectItem` is an extraction of the existing desktop Overview projection; both desktop and mobile now call it. `workspacelist` continues to own search, sorting, and grouping. Mobile does not add a status classifier. A ready catalog row carries a selector and an exact expected identity; ambiguous, unsupported, unavailable, or stale rows do not carry attachment authority. Catalog queries are refused while that protocol stream has a live terminal attachment, so collection cannot block its input or heartbeat loop.

The review uses synthetic fixtures and read-only queries against the coordinator-owned private proof fixture. Independent cold-history proof uses a separate temporary Sidecar state copy containing only that disposable project's metadata. Every live query clears inherited `TMUX`/`TMUX_PANE`, uses the explicit private tmux namespace and temporary config, and sets `SIDECAR_ISOLATED_STATE=1`. No default tmux server, managed installation, user configuration, terminal input, or root-owned session is changed.

## Remaining scope

This slice supplies local snapshots and their identity contract. Registered-host composition, outage/stale-client/configuration handling, routed remote terminal operations, and worktree terminal selection remain subsequent M1 work. A1 approval does not complete the parent cross-host task, the native Sessions browser, or physical M0 terminal proof.
