<img src="internal/brand/assets/skua_avatar.png" width="128" align="right" alt="skua">

# skua

A minimal, fast, modular Discord bot in Go, and a test bed for what Discord bots can
do. Built on [disgo](https://github.com/disgoorg/disgo) and
[pgx v5](https://github.com/jackc/pgx).

- Identifies only with the gateway intents the Developer Portal has actually granted,
  read from the application's flags, and restarts itself when a toggle changes.
- Every command declares who may run it, or the bot refuses to start.
- Per-guild write caps in a single compare-and-swap (about 7ns, no allocation).
- Nothing it sends can ping anyone.

[`SPEC.md`](SPEC.md) is the one-page design and the experiment log.

## Docs

- [Run](docs/run.md) it locally
- [Go live](docs/go-live.md): the token, the profile and the invite
- [Deploy](docs/deploy.md): what a merge to `main` does
- [Moving to another server](docs/moving.md)
