# CLAUDE.md

## What this is

`skua` is a minimal, fast Discord bot in Go (disgo, pgx v5) and a test bed for
Discord experiments. Read [`SPEC.md`](./SPEC.md) first. It is
one page and lists the principles and the experiments.

- `cmd/skua/main.go`: wiring only.
- `internal/core`: `Module` interface and the command `Router`. A tier is mandatory, and
  an unset one fails boot.
- `internal/intents`: portal flags → granted intents → what to identify with.
- `internal/guard`: per-guild per-op GCRA caps plus a breaker. Route new Discord writes
  through `Allow`/`Report`.
- `internal/brand`: palette and mood icons. The mood comes from the embed colour.
  Always send the `*discord.File` that `brand.Embed` returns with its embed.
- `internal/modules/*`: features. `status` is the reference module.
- `tools/sprites`: all the pixel art, computed from shapes, banded light and a fixed
  hash, so every run is identical. One bust drawing is the profile picture (on its
  halo, `art/`) and every mood icon (with a badge, embedded); the banner is in `art/`
  too. Style: high resolution pixel art, flat fields, 2px clusters, cool grey frame,
  the bird the only warmth. Edit and rerun it; don't edit the PNGs.

Adding a module: implement `core.Module`, declare its intents honestly in `Want`
(Required vs Optional), and append it to `all` in `main.go`. If it needs a table, add
the migration runner in `internal/store` along with the first migration.

## Commands

```sh
go build ./... && go vet ./...
golangci-lint run
go test ./... -race -cover
govulncheck ./...
go run ./tools/sprites                 # regenerate internal/brand/assets
go test ./internal/guard -bench . -run x

cp .env.example .env && docker compose up --build   # local bot + Postgres
go run ./cmd/skua                                   # native, DB optional
go run ./tools/setup                                # go live / rotate the token: .env, profile, deploy, invite
scripts/deploy.sh                                   # manual path: commit first; deploys HEAD via foundry-deploy
```

## GitHub and deploy

Public repo `6586x57890143/skua`. `main` takes changes only through PRs, squash merged,
and the PR title becomes the commit, so it should read like one (`feat(guard): ...`).
One change per PR. Branches are deleted on merge.

CI (`.github/workflows/ci.yml`) runs on every PR and every push to `main`: vet, lint,
race tests, govulncheck, gitleaks, the prose check (no em dashes, ellipsis characters
or curly quotes anywhere; run its grep before committing), and an arm64 Docker build.
Those are the required checks. On `main` it then pushes
`ghcr.io/6586x57890143/skua:<sha>` and deploys to foundry as `deploy` into
`/home/deploy/skua`, using the `VPS_HOST`/`VPS_SSH_KEY` repository secrets. The deploy only runs while the repository variable `DEPLOY_ENABLED` is
`true`; otherwise it skips green with a notice.

Rollback, and the manual path, are in `docker-compose.prod.yml` and `scripts/deploy.sh`.
