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

## Run

```sh
cp .env.example .env    # DISCORD_BOT_TOKEN, SKUA_BOOTSTRAP_ADMIN_USER_ID
go run ./cmd/skua       # Postgres optional: set SKUA_DATABASE_URL
# or
docker compose up --build
```

## Go live

After the first merge to `main` has built the image:

```sh
go run ./tools/setup
```

It asks for the bot token (hidden), checks it with Discord, makes the application's
owner the bootstrap admin, writes both to the host, sets the bot's avatar and banner,
reports the privileged intents, deploys through GitHub Actions, waits for the bot to
connect, and prints the invite. Safe to run again at any time, for example to rotate
the token.

Then add skua to a server from **https://skua.melting.lol**. That is `web/`, a Worker
that redirects to Discord's install link. What that link asks for is written by skua
itself at every boot: the bot and its slash commands, plus the permissions the running
modules declare. A module's new permission reaches the link on the deploy that ships it.

## Deploy

Merging to `main` builds an image for the host's platform to GHCR and deploys it to the host with
Docker Compose, once the repository variable `DEPLOY_ENABLED` is `true`.
`scripts/deploy.sh` is the manual path.

## Moving to another server

The host needs Docker with the Compose plugin and an ssh user, nothing else. Images
are built for whatever the host runs, which setup records from the host's Docker.

1. On the new host, create a `deploy` user that can run `docker`, and put the public
   half of the CI deploy key (the private half is the `VPS_SSH_KEY` secret) in its
   `~/.ssh/authorized_keys`.
2. Locally, add an ssh alias for it, then run setup against it:

   ```sh
   go run ./tools/setup -host new-alias
   ```

   On a host with no `~/skua/.env` it creates one with a generated database password
   and a matching `SKUA_DATABASE_URL`, points CI at the new machine (`VPS_HOST`,
   `VPS_USER`, `VPS_DIR` and `VPS_PLATFORM`), deploys and waits for the bot to connect.
   An image built before the move only runs on the old architecture, so rolling back
   past the move means rebuilding that commit.
3. Stop the old host with `docker compose -f docker-compose.prod.yml down` in `~/skua`.
   Two processes with the same token would both answer every command.

There is no data to move yet: no module has a table. The first one that does adds the
dump and restore step here.
