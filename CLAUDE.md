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
- `internal/brand`: palette, mood icons and skua's application emoji. The mood comes from
  the embed colour. `brand.Sync` uploads any missing emoji in the background at boot;
  once it has, icons are served from Discord's CDN and nothing is attached. Always send
  the files `brand.Embed` returns with its embed: empty once synced, the icon until then.
  `voice.go`
  is her status line, rotated on the re-probe tick; `voice_test` holds every line to the
  shared bird voice (lowercase, no oxford commas or generated-text punctuation).
- `internal/modules/*`: features. `status` is the reference module.
- `internal/concord`: the slice of Armada's Concord protocol the `armada` bridge needs,
  ported from armada-discord-bridge's concord-core. `testdata/upstream.json` is made by
  upstream's own TypeScript; regenerate it rather than edit it. Changing what the fold
  admits makes skua disagree with Armada about who is banned, so don't.
- `tools/sprites`: all the art. The avatar is drawn: `art/source/skua_avatar_source.png`
  is the original, and the generator lifts the bird out, recolours it into the palette
  (feathers to the umber ramp, neutral greys to slate) and resamples it by majority
  onto a 128-cell grid. The profile picture is that bird over a flat field and a halo
  disc centred on the grid (8px cells, 1024); every mood icon is the bird centred at
  2px cells (256) with its badge. It also writes 128px application emoji into
  `internal/brand/emoji`: the moods (the bird at 1px cells with its badge), the avatar
  and a tile per module whose glyph comes from pixelarticons (MIT,
  `art/LICENSE-pixelarticons`), kept as text grids in `tools/sprites/icons.go`. An
  emoji's name carries its PNG's hash, so only a redrawn one is uploaded again. The
  banner is computed from shapes, banded light and a fixed hash. Style: high resolution pixel art, flat fields, cool grey frame, the bird
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
go run ./tools/sprites                 # regenerate internal/brand/assets and emoji
go test ./internal/guard -bench . -run x
go test ./internal/obs -bench . -run x       # what a /perf sample costs
go run ./tools/reactbench                    # live: see "Live benchmarks"
curl -o skua.trace localhost:6060/debug/skua/flight && go tool trace skua.trace   # with SKUA_PPROF=localhost:6060

cp .env.example .env && docker compose up --build   # local bot + Postgres
go run ./cmd/skua                                   # native, DB optional
go run ./tools/setup                                # go live / rotate the token: .env, profile, deploy
(cd web && wrangler deploy)                         # skua.melting.lol invite redirect; only when web/ changes
scripts/deploy.sh                                   # manual path: commit first; deploys HEAD via foundry-deploy
```

## Live benchmarks

A performance claim about Discord is measured live, never assumed. There is a test bot
for it, `flightless.skua`, separate from skua so a run never shares skua's rate limit
buckets, in a test channel. Its token and the channel are in
`C:\files\sdb\active\skua.worktrees\bench.env`, outside every checkout so no commit or
worktree removal takes them. Never print the token, and never put it in `.env.example`.

```sh
set -a; . ../bench.env; set +a       # from inside a worktree
go run ./tools/reactbench -trials 5  # every preen.Strategies entry, interleaved
go run ./tools/reactbench -compare policy -trials 6   # serial under each 429 wait policy
```

The 429 wait policy is `userReset` in `cmd/skua/main.go` (see `internal/ratelimit`): a
user-scope 429 waits its bucket's reset rather than the body's `retry_after`. It was
measured faster with no more 429s; re-measure with `-compare policy` before changing it.
Let the channel rest a minute or two between runs: a sustained run brings in a shared
limit that slows every method alike and hides the difference.

The loop: add an idea as an entry in `preen.Strategies` (or a new tool beside
`tools/reactbench` for another route), run it, and only move it first, the one preen
uses, if it beats the current first by more than run-to-run noise over at least five
trials. Record the numbers in SPEC.md's experiments. `/perf` shows the same split in
production (wait is the rate limit, http the round trip).

Stay inside Discord's invalid request limit: 10,000 401, 403 and 429 answers per 10
minutes per IP, past which Cloudflare bans the IP and every bot on it. Honour every
wait Discord gives, never retry a 401 or 403, stop on any global or shared 429, and cap
429s per run (`reactbench` stops at 300). A path that sends ahead of Discord's headers
was tried and drew 20 429s in one fill, so it is not worth trying again.

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
