# skua spec

skua is a lean Discord bot that exists to try things: new Discord surfaces, faster
Go, sharper safety at no measurable cost. Every experiment is a hypothesis with a way
to tell whether it worked, and the log is below.

## Principles

1. **One binary, modules by package.** A module implements `core.Module` (`Name`,
   `Want`, `Commands`) and is listed in `cmd/skua/main.go`. Nothing is loaded at runtime.
   Modules never import each other. Whatever one needs it takes as a constructor
   argument, as a narrow interface in its own package.
2. **Ask Discord, don't assume.** Intents come from the portal's application flags
   (`internal/intents`). skua identifies with `wants ∩ granted`, never more, so close
   code 4014 cannot happen. A module missing a required intent is skipped by name. The
   portal is re-read every 10 minutes, and a change restarts the process. The same goes
   the other way: at boot skua writes the app's install settings from what its running
   modules declare in `Perms`, so the invite asks for exactly what is in use.
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
| Build | Go 1.27, built for the deploy host's platform (`VPS_PLATFORM`), `CGO_ENABLED=0 -trimpath -s -w`, distroless nonroot, PGO from `cmd/skua/default.pgo` when present |
| Deploy | `scripts/deploy.sh`: builds natively on `foundry` (arm64) from `git archive`, compose, rollback via `previous-tag.env` |

## Experiments

Each one is a hypothesis with a way to tell whether it worked. Record the result here.

| # | Experiment | State |
|---|---|---|
| 1 | zstd-stream gateway compression | **On**, disgo's default. Compare bytes/s against `zlib-stream` with `gateway.WithCompression` |
| 2 | PGO: capture 30s of CPU from `SKUA_PPROF` under real load into `cmd/skua/default.pgo` | open |
| 3 | GCRA cells instead of a mutex-guarded sliding window | **Done**, 6.9ns/op, 0 allocs under `RunParallel` |
| 4 | Intent probe instead of a gateway-ready watchdog | **Done**, pending a live portal-toggle test |
| 5 | Input filter: Unicode folding, and a consonant-skeleton key that gates each rule's regex, instead of running every rule's ASCII pattern on every message | **Done**: clean message 3.0us and 0 allocs against 14us for every rule in turn (20us as one alternation); catches lookalikes, fullwidth, zero-width, Zalgo and held letters |

## Milestones

| # | Milestone | State |
|---|---|---|
| 0 | Scaffold: router, intents probe, guard, pgx, brand, `/ping` `/status`, deploy | done |
| 1 | `/echo`: a chat-restricted member posts one line through a per-channel webhook, marked with their username | done |
| 2 | `internal/filter` for every module that posts member text; echo screens through it and members can edit and delete their own echoes | done |
| 3 | `/bird` (experimental, admin): for up to an hour every message one member sends is replaced by one short xeno-canto bird recording, posted through skua's webhook as them with a credit line. Needs `SKUA_XENO_CANTO_KEY` | done, not run against Discord or a live key yet |
