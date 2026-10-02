# Handoff: Milestone 0 (scaffold)

Written 2026-10-02. Read this with `CLAUDE.md` and `SPEC.md`.

## Where the branches are

| Branch | State |
|---|---|
| `main` | Milestone 0, pushed to `github.com/6586x57890143/skua`. |
| `ci/deploy-foundry` | The CI/CD pipeline and repo hygiene. PR #1. |

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
- **CI builds arm64 only and pushes to GHCR. foundry pulls by commit SHA.** The
  manual `scripts/deploy.sh` builds on foundry itself from `git archive`, under the
  same image name and directory, so the two paths never disagree about what is running.
- **The prune is label-scoped.** GitHub concurrency groups are per repository, so
  nothing serialises skua's deploy against other projects deploying to the same
  daemon. An unscoped prune racing another project's pull fails with `lease does not exist`.
- **The database is optional.** Without `SKUA_DATABASE_URL` skua runs with no DB and
  `/status` says so. That makes the test bed cheap to run anywhere.

## Not done yet, deliberately

- The deploy is held: `DEPLOY_ENABLED` is unset until `/home/deploy/skua/.env` exists
  on foundry with a real bot token. Merges still build and push the image.
- No migration runner or tables: nothing needs one.
- The guard is built and tested, but nothing routes through it yet. `/ping` and
  `/status` only answer interactions, which aren't write-capped. The first module that
  posts or edits must use it.
- Not run against Discord yet: needs a dev bot token.
- No `default.pgo` yet (experiment 2 needs real load first).
- The art is computed, not drawn. It holds up at Discord sizes; a hand-drawn sprite sheet
  would still beat it, and can replace any PNG as long as the file name stays.
