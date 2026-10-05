# CLAUDE.md

## What this is

`skua` is a minimal, fast Discord bot in Go (disgo, pgx v5) and a test bed for
Discord experiments. Read [`SPEC.md`](./SPEC.md) first. It is
one page and lists the principles and the experiments. Anything a person sees (replies,
embeds, command descriptions, the setup CLI, the art) follows [`UX.md`](./UX.md): all
lowercase, errors as `core.Tell`, facts on a grid, art on an exact pixel grid.

- `cmd/skua/main.go`: wiring only.
- `internal/core`: `Module` interface and the command `Router`. A tier is mandatory, and
  an unset one fails boot.
- `internal/intents`: portal flags → granted intents → what to identify with.
- `internal/guard`: per-guild per-op GCRA caps plus a breaker. Route new Discord writes
  through `Allow`/`Report`.
- `internal/brand`: palette and mood icons. The mood comes from the embed colour.
  Always send the `*discord.File` that `brand.Embed` returns with its embed. `voice.go`
  is her status line, rotated on the re-probe tick; `voice_test` holds every line to the
  shared bird voice (lowercase, no oxford commas or generated-text punctuation).
- `internal/modules/*`: features. `status` is the reference module.
- `tools/sprites`: all the art. The avatar is drawn: `art/source/skua_avatar_source.png`
  is the original, and the generator lifts the bird out, recolours it into the palette
  (feathers to the umber ramp, neutral greys to slate) and resamples it by majority
  onto a 128-cell grid. The profile picture is that bird over a flat field and a halo
  disc centred on the grid (8px cells, 1024); every mood icon is the bird centred at
  2px cells (256) with its badge. The banner is computed from shapes, banded light and
  a fixed hash. Style: high resolution pixel art, flat fields, cool grey frame, the bird
  the only warmth. Every PNG is written indexed at best compression, and
  `internal/brand` fails if an embedded icon reaches 10 KB or leaves its 2px grid. Edit
  the source or the generator and rerun it; don't edit the output PNGs.

Adding a module: implement `core.Module`, declare its intents honestly in `Want`
(Required vs Optional) and its channel permissions in `Perms` (only what skua does as
itself; interaction replies need none), give it a `/help` page with `Help()` (field-notes
voice: lowercase, third person "she", dry), and append it to `all` in `main.go`. skua writes
the union of the running modules' `Perms` into the app's install settings at boot, which
is what https://skua.melting.lol asks a server for. It ships with handler
tests: `internal/core/coretest` builds the interaction and records replies, and a test
that needs REST sets `e.Client().Rest` to a fake embedding `rest.Rest`. CI fails any
`internal/` package under 85% coverage. If it needs a table, add
the migration runner in `internal/store` along with the first migration.

## Commands

```sh
go build ./... && go vet ./...
golangci-lint run
scripts/coverage.sh                    # race tests + 85% floor per internal/ package
scripts/prose.sh                       # CI's punctuation check
govulncheck ./...
go run ./tools/sprites                 # regenerate internal/brand/assets
go test ./internal/guard -bench . -run x
go test ./internal/obs -bench . -run x       # what a /perf sample costs
curl -o skua.trace localhost:6060/debug/skua/flight && go tool trace skua.trace   # with SKUA_PPROF=localhost:6060

cp .env.example .env && docker compose up --build   # local bot + Postgres
go run ./cmd/skua                                   # native, DB optional
go run ./tools/setup                                # go live / rotate the token: .env, profile, deploy
(cd web && wrangler deploy)                         # skua.melting.lol invite redirect; only when web/ changes
scripts/deploy.sh                                   # manual path: commit first; deploys HEAD via foundry-deploy
```

## GitHub and deploy

Public repo `6586x57890143/skua`. `main` takes changes only through PRs, squash merged,
and the PR title becomes the commit, so it should read like one (`feat(guard): ...`).
One change per PR. Branches are deleted on merge.

CI (`.github/workflows/ci.yml`) runs on every PR and every push to `main`: vet, lint,
race tests under a per-package coverage floor, govulncheck, gitleaks, the prose check
(`scripts/prose.sh`: no em dashes, ellipsis characters or curly quotes anywhere), and
a Docker build for the deploy host's platform (`VPS_PLATFORM`, written by
`tools/setup`; arm64 by default). Those are the required checks, and they are strict:
a PR must be up to date with `main` to merge. On `main` it then pushes
`ghcr.io/6586x57890143/skua:<sha>` and deploys to the host as `VPS_USER` into `VPS_DIR`
(repository variables, defaulting to foundry's `deploy` and `/home/deploy/skua`; `tools/setup`
writes them), using the `VPS_HOST`/`VPS_SSH_KEY` repository secrets. The deploy only runs while the repository variable `DEPLOY_ENABLED` is
`true`; otherwise it skips green with a notice.

Rollback, and the manual path, are in `docker-compose.prod.yml` and `scripts/deploy.sh`.

## Working in parallel

Several agents work on skua at once. The rules that keep them out of each other's way:

- **Never work in the main checkout** (`C:\files\sdb\active\skua`). It stays on a clean
  `main` for reading and reviewing. `.claude/settings.json` denies file edits there.
  Each branch gets its own worktree in `C:\files\sdb\active\skua.worktrees\<name>`:
  `git worktree add -b <branch> ../skua.worktrees/<name> origin/main`. Not a session
  scratchpad, which other sessions can't see and which gets wiped. Not `EnterWorktree`,
  which nests worktrees inside the main checkout. After the merge, run
  `git worktree remove` and delete the local branch.
- **Claim work with a draft PR** as soon as the branch has a commit. `gh pr list` is the
  record of who is doing what. Check it before you start, and don't take on a change
  another open PR already touches.
- **`main` moves under you.** Rebase on `origin/main` before asking for review, and again
  whenever the PR falls behind (`gh pr update-branch`, or rebase and force-push your
  own branch). Before changing a shared contract (`core`, `guard`, `brand`), grep its
  callers on current `main`, not on your branch's base.
- **Shared files:** in `all` (`main.go`), `SPEC.md` and `HANDOFF.md`, edit only your own
  line or row. On a conflict in generated PNGs, don't pick a side: rerun
  `go run ./tools/sprites` on the rebased branch.
- **One bot per token.** Two processes on the same `DISCORD_BOT_TOKEN` both receive every
  interaction and race to answer it. Tests need no token. Run a live bot only when you
  have checked that no other session is running one. Give each local Postgres its own
  port with `SKUA_PG_PORT` in that worktree's `.env`.
- Stage paths explicitly, never `git add -A`. Auto-merge only after review.
