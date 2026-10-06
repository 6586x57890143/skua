# Moving to another server

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
