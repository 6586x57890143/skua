package store

import (
	"context"
	"io/fs"
	"os"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/6586x57890143/skua/internal/core"
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
	if _, err := pool.Exec(ctx, "select p.ids, c.read_through, c.threads_listed_at from purge_postings p, purge_channels c limit 0"); err != nil {
		t.Fatalf("the purge index: %v", err)
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

func TestModuleSwitchesRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool, err := Open(ctx, dsn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	save := SaveModule(pool)
	s := core.Switch{Guild: 1 << 60, Module: "preen"}
	for _, on := range []bool{false, false} {
		if err := save(ctx, s, on); err != nil {
			t.Fatal(err)
		}
	}
	off, err := ModulesOff(ctx, pool)
	if err != nil || !slices.Contains(off, s) {
		t.Fatalf("off %v (%v), want %v in it", off, err, s)
	}
	if err := save(ctx, s, true); err != nil {
		t.Fatal(err)
	}
	if off, _ := ModulesOff(ctx, pool); slices.Contains(off, s) {
		t.Fatal("turned on, still off")
	}
}
