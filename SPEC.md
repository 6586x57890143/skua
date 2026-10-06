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
| Database | Postgres 16 via `pgx/v5` `pgxpool`, optional. SQL migrations in `internal/store/migrations`, compiled in and applied at boot in one transaction |
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
| 7 | Filling a post with reactions: how many calls in flight (1, 2, 4, all), and a raw path paced ahead of disgo by Discord's headers, measured live with `tools/reactbench` | **Done**: disgo waited a whole second on every 429 (`Retry-After` is whole seconds) and Discord's per-user reaction limit draws about 7 a fill, so 15 birds took 10-15s. `internal/ratelimit` waits the 429 body's `retry_after` instead (never `X-RateLimit-Reset-After`, the bucket's reset, which a shared 429 can put far short of its real wait; shared, global and Cloudflare 429s are left to disgo): about 5.5s, every in-flight count alike. The raw path hit 20 429s in a fill and was dropped. preen fills serially. After about 16 fills back to back (around 240 reactions in two minutes) Discord adds a shared-scope limit on the channel and every strategy slows to about 20s; one self-react never gets near it |
| 6 | Always-on tracing from the metal to the API: every module timed in four legs (in, run, wait, http) by lock-free half-octave histograms, the Go runtime's scheduler latency and GC pauses beside them, and a flight recorder of the last 10s of execution trace with a region per module. `/perf` shows it slowest first | **Done**: 33 ns and 0 allocs a sample (28 ns under `RunParallel`); not run under real load yet |
| 5 | Input filter: Unicode folding, and a consonant-skeleton key that gates each rule's regex, instead of running every rule's ASCII pattern on every message | **Done**: clean message 3.0us and 0 allocs against 14us for every rule in turn (20us as one alternation); catches lookalikes, fullwidth, zero-width, Zalgo and held letters |

## Milestones

| # | Milestone | State |
|---|---|---|
| 0 | Scaffold: router, intents probe, guard, pgx, brand, `/ping` `/status`, deploy | done |
| 1 | `/whisper`: a chat-restricted member posts one line through a per-channel webhook, marked with their username | done |
| 2 | `internal/filter` for every module that posts member text; whisper screens through it and members can edit and delete their own whispers | done |
| 3 | `/bird` (experimental, admin): for up to an hour every message one member sends is replaced by one short xeno-canto bird recording, posted through skua's webhook as them with a credit line. Needs `SKUA_XENO_CANTO_KEY` | done, not run against Discord or a live key yet |
| 4 | `/purge`: members delete their own messages in a server on demand, on a schedule or live, at the fastest rate one token allows. The break-glass admin can run it for a member; server admins cannot. Each server is read once into an index of who wrote which message (IDs and authors, never content, about 6 bytes a message), forgotten when skua leaves; a purge reads only what is new, then deletes many channels at once. A readout that outlives its 15 minute token carries on in a DM, or on a progress button for members with DMs closed | done: `now`, `stop`, `live`, `every`, `status`, break-glass with `jobs` (every purge running or coming up, in every server) and the index; run against Discord |
| 6 | `/perf` and the build stamp: admins see where the time goes, `/status` and `/help` say which commit is running, and `/help`'s index is compact, one avatar and each module's name with its line as subtext below | done, not run against Discord yet |
| 5 | `/help`: the field guide. One Components V2 container per page, an index of every running module with a page and a page per module, turned with a select menu; stateless, so any guide ever sent keeps working. Admins can post it, and switch a module off or on for their server from its page (`help` and `status` stay on) | in progress |
