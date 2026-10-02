# skua spec

skua is merlin's lean sibling: a Discord bot that exists to try things. It keeps
merlin's spine and drops its weight. When an experiment proves itself here, it can be
ported to merlin, which carries the community's real exposure.

## Principles

1. **One binary, modules by package.** A module implements `core.Module` (`Name`,
   `Want`, `Commands`) and is listed in `cmd/skua/main.go`. Nothing is loaded at runtime.
   Modules never import each other. Whatever one needs it takes as a constructor
   argument, as a narrow interface in its own package.
2. **Ask Discord, don't assume.** Intents come from the portal's application flags
   (`internal/intents`). skua identifies with `wants ∩ granted`, never more, so close
   code 4014 cannot happen. A module missing a required intent is skipped by name. The
   portal is re-read every 10 minutes, and a change restarts the process.
3. **Fail closed, cheaply.** A command with no tier fails boot. Admin means the
   bootstrap user, the Administrator bit, or the guild owner, and nothing else.
   `internal/guard` caps writes per guild and per op with one CAS (~7ns, 0 allocs), and
   a per-guild breaker opens on repeated 429/5xx. Safety that costs nothing measurable
   is the only kind skua takes.
4. **Nothing pings.** Every message skua sends carries `core.NoPings()` (`"parse":[]`). A
   send that genuinely needs to ping gets its own named function with an explicit
   allowlist.
5. **Smallest thing that works.** No table, cache, job or config option until a module
   needs it. Deliberate shortcuts carry a `ponytail:` comment that names their ceiling.

## Stack

| | |
|---|---|
| Discord | `disgoorg/disgo` v0.19, zstd-stream gateway compression (its default), every cache off except guilds |
| Database | Postgres 16 via `pgx/v5` `pgxpool`, optional, no tables yet |
| Build | Go 1.27, `CGO_ENABLED=0 -trimpath -s -w`, distroless nonroot, PGO from `cmd/skua/default.pgo` when present |
| Deploy | `scripts/deploy.sh`: builds natively on `foundry` (arm64) from `git archive`, compose, rollback via `previous-tag.env` |

## Experiments

Each one is a hypothesis with a way to tell whether it worked. Record the result here.

| # | Experiment | State |
|---|---|---|
| 1 | zstd-stream gateway compression | **On**, disgo's default. Compare bytes/s against `zlib-stream` with `gateway.WithCompression` |
| 2 | PGO: capture 30s of CPU from `SKUA_PPROF` under real load into `cmd/skua/default.pgo` | open |
| 3 | GCRA guard in place of merlin's mutex governor | **Done**, 6.9ns/op, 0 allocs under `RunParallel` |
| 4 | Intent probe in place of merlin's `WatchReady` watchdog | **Done**, pending a live portal-toggle test |

## Milestones

| # | Milestone | State |
|---|---|---|
| 0 | Scaffold: router, intents probe, guard, pgx, brand, `/ping` `/status`, deploy | done |
