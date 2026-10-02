# Handoff: Milestone 0 (scaffold)

Written 2026-10-02. Read this with `CLAUDE.md` and `SPEC.md`.

## Where the branches are

| Branch | State |
|---|---|
| `main` | Milestone 0. Local only, no remote. |

## What M0 did

Built the spine and one module. skua probes the portal, identifies with only what is
granted, registers `/ping` (public) and `/status` (admin) per guild on GuildReady/GuildJoin,
and answers them with branded ephemeral embeds. `/status` reports granted vs identified
intents, skipped modules, database round trip, gateway latency and uptime.

Decisions worth keeping:

- **Our own command map, not disgo's `handler.Mux`.** Dispatch is one map lookup plus
  the tier check, under `recover()`. Mux's path routing buys nothing for top-level
  commands, and owning the map is what lets `Add` refuse an unset tier at boot.
- **The re-probe exits instead of reconnecting.** It's a single `return`, and compose's
  `restart: unless-stopped` does the rest. A clean exit is logged at info, not error.
- **Images build on foundry.** Local Docker was not running and foundry is arm64.
  `git archive | ssh foundry docker build -` builds natively, needs no registry, and
  refuses a dirty tree, so what runs is always a commit.
- **The database is optional.** Without `SKUA_DATABASE_URL` skua runs with no DB and
  `/status` says so. That makes the test bed cheap to run anywhere.

## Not done yet, deliberately

- No GitHub remote, so CI has never run and there is no GHCR/CI deploy. The script is
  the deploy path.
- No migration runner or tables: nothing needs one.
- The guard is built and tested, but nothing routes through it yet. `/ping` and
  `/status` only answer interactions, which aren't write-capped. The first module that
  posts or edits must use it.
- Not run against Discord yet: needs a dev bot token in `.env`. Not deployed to foundry
  yet: needs `~/skua/.env` there.
- No `default.pgo` yet (experiment 2 needs real load first).
- The sprites are hand-drawn 32x32 grids. A proper drawn sheet, cut the way merlin's
  was, can replace them without code changes as long as the file names stay the same.
