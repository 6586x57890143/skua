package store

import (
	"context"

	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/6586x57890143/skua/internal/core"
)

// ModulesFlipped is every switch flipped from where its module starts, for
// core.NewToggles at boot.
func ModulesFlipped(ctx context.Context, pool *pgxpool.Pool) ([]core.Switch, error) {
	rows, _ := pool.Query(ctx, "select guild_id, module from module_off")
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (core.Switch, error) {
		var s core.Switch
		var guild int64
		err := row.Scan(&guild, &s.Module)
		s.Guild = snowflake.ID(guild)
		return s, err
	})
}

// SaveModule is core.Toggles' save: a row while flipped, none while not.
// The table kept its name from when every module started on, so flipped
// meant off; an opt-in module's row means on, and an older build that
// never heard of it ignores it.
func SaveModule(pool *pgxpool.Pool) func(context.Context, core.Switch, bool) error {
	return func(ctx context.Context, s core.Switch, flipped bool) error {
		q := "insert into module_off (guild_id, module) values ($1, $2) on conflict do nothing"
		if !flipped {
			q = "delete from module_off where guild_id = $1 and module = $2"
		}
		_, err := pool.Exec(ctx, q, int64(s.Guild), s.Module)
		return err
	}
}
