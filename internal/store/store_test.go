package store

import (
	"context"
	"io/fs"
	"os"
	"testing"
	"testing/fstest"
)

// The test needs a real Postgres: CI runs one, and locally `docker compose up
// postgres` with SKUA_TEST_DATABASE_URL pointing at it. Unset, it skips.
func dsn(t *testing.T) string {
	t.Helper()
	d := os.Getenv("SKUA_TEST_DATABASE_URL")
	if d == "" {
		t.Skip("SKUA_TEST_DATABASE_URL is not set")
	}
	return d
}

func TestMigrateTwiceIsANoop(t *testing.T) {
	ctx := context.Background()
	pool, err := Open(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for range 2 {
		if err := Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, "select count(*) from schema_migrations where version = 'migrations/001_purge.sql'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("001 recorded %d times, want 1", n)
	}
	if _, err := pool.Exec(ctx, "select guild_id, swept_through from purge_subs limit 0"); err != nil {
		t.Fatalf("purge_subs: %v", err)
	}
}

func TestOpenRefusesABadDSN(t *testing.T) {
	if _, err := Open(context.Background(), "postgres://nobody@127.0.0.1:1/x?connect_timeout=1"); err == nil {
		t.Fatal("Open succeeded against a closed port")
	}
	if _, err := Open(context.Background(), "::not a dsn"); err == nil {
		t.Fatal("Open accepted a malformed DSN")
	}
}

// A migration that fails takes the ones before it in the same run down with
// it: the schema is either all the files or none of the new ones.
func TestMigrateRollsBackAFailure(t *testing.T) {
	ctx := context.Background()
	pool, err := Open(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	defer func(m fs.FS) { migrations = m }(migrations)
	migrations = fstest.MapFS{
		"migrations/900_good.sql": {Data: []byte("create table rollback_probe (id int)")},
		"migrations/901_bad.sql":  {Data: []byte("this is not sql")},
	}
	if err := Migrate(ctx, pool); err == nil {
		t.Fatal("a broken migration applied")
	}
	var exists bool
	if err := pool.QueryRow(ctx, "select to_regclass('rollback_probe') is not null").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("900_good.sql survived 901_bad.sql failing")
	}
}
