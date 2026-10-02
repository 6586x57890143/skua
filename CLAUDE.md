# CLAUDE.md

## What this is

`skua` is a minimal, fast Discord bot in Go (disgo, pgx v5) and a test bed for
experiments before they reach `active/merlin`. Read [`SPEC.md`](./SPEC.md) first. It is
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
- `tools/sprites`: the pixel art, as text grids. Edit a grid and rerun it, don't edit
  the PNGs.

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
scripts/deploy.sh                                   # commit first; deploys HEAD to ssh foundry
```

CI (`.github/workflows/ci.yml`) runs vet, lint, race tests, govulncheck, gitleaks, the
prose check (no em dashes, ellipsis characters or curly quotes anywhere), and an arm64
Docker build. Run the prose grep from that file before committing.

There is deliberately no remote yet. Don't add one or push without being asked.
