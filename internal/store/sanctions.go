package store

import (
	"context"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/6586x57890143/skua/internal/sanction"
)

// Sanctions is sanction.History in Postgres.
type Sanctions struct{ Pool *pgxpool.Pool }

func (s Sanctions) Priors(ctx context.Context, guild, user snowflake.ID, since time.Time) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, "select count(*) from sanctions where guild_id = $1 and user_id = $2 and at >= $3",
		int64(guild), int64(user), since).Scan(&n)
	return n, err
}

func (s Sanctions) Record(ctx context.Context, sn sanction.Sentence) error {
	_, err := s.Pool.Exec(ctx, "insert into sanctions (guild_id, user_id, rule, at, lasts) values ($1, $2, $3, $4, $5)",
		int64(sn.Guild), int64(sn.User), sn.Rule, sn.At, sn.For)
	return err
}
