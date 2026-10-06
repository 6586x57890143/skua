# Moving to another server

The host needs Docker with the Compose plugin and an ssh user, nothing else. Images
are built for whatever the host runs, which setup records from the host's Docker.

The database moves too: purge's index and schedules, the modules each server turned
off, and armada's message mappings. It holds Discord user ids, so treat the dump like
a secret and delete it when you are done. On each host, run the compose commands as
`deploy` in `~/skua`.

1. On the old host, stop the bot but keep Postgres up, so nothing is written after the
   dump and two bots never run on one token:

   ```sh
   docker compose -f docker-compose.prod.yml stop bot
   docker compose -f docker-compose.prod.yml exec -T postgres \
     sh -c 'pg_dump -U "$POSTGRES_USER" --clean --if-exists skua' > skua.sql
   ```

2. On the new host, create a `deploy` user that can run `docker`, and put the public
   half of the CI deploy key (the private half is the `VPS_SSH_KEY` secret) in its
   `~/.ssh/authorized_keys`.
3. Locally, add an ssh alias for it, then run setup against it:

   ```sh
   go run ./tools/setup -host new-alias
   ```

   On a host with no `~/skua/.env` it creates one with a generated database password
   and a matching `SKUA_DATABASE_URL`, points CI at the new machine (`VPS_HOST`,
   `VPS_USER`, `VPS_DIR` and `VPS_PLATFORM`), deploys and waits for the bot to connect.
   An image built before the move only runs on the old architecture, so rolling back
   past the move means rebuilding that commit.
4. Copy the rest of the old `.env` across, everything but the database lines setup
   wrote: `SKUA_XENO_CANTO_KEY` and every `SKUA_ARMADA_*`. `SKUA_ARMADA_MASTER_SECRET`
   has to be the same, or every Discord member gets a new identity in Armada.
5. Restore the dump into the new host's database, with the bot stopped so it starts
   on the restored data:

   ```sh
   scp old-alias:skua/skua.sql new-alias:skua/
   # then on the new host:
   docker compose -f docker-compose.prod.yml stop bot
   docker compose -f docker-compose.prod.yml exec -T postgres \
     sh -c 'psql -U "$POSTGRES_USER" -d skua -v ON_ERROR_STOP=1' < skua.sql
   docker compose -f docker-compose.prod.yml up -d bot
   rm skua.sql
   ```

   The dump drops and recreates every table, so the empty schema the new bot made at
   boot is replaced, `schema_migrations` included.
6. On the old host, take everything down with `docker compose -f docker-compose.prod.yml
   down`, and delete `skua.sql` there too.
