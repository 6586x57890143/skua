package purge

import (
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5"
)

// The index is who wrote which message where, for every channel skua has
// read in a guild: read once, so a purge is a lookup instead of a crawl.
// It holds IDs and authors, never content.
//
// A posting block is one author's message IDs in one channel, ascending.
// The first ID is the row's key and isn't repeated; each next one is the
// gap in milliseconds as a uvarint, then its low 22 bits (worker, process,
// increment) in three bytes. Splitting the snowflake is what makes it
// small: a delta of whole IDs carries the near-random low bits up into the
// varint. A gap of minutes to hours costs 5 to 7 bytes, against 8 raw.

const (
	blockV1 = 1
	lowBits = 22
	lowMask = 1<<lowBits - 1
)

var errBlock = errors.New("purge: corrupt posting block")

// pack packs ascending, distinct ids.
func pack(ids []snowflake.ID) (first snowflake.ID, data []byte) {
	data = make([]byte, 1, 1+6*len(ids))
	data[0] = blockV1
	for i := 1; i < len(ids); i++ {
		data = binary.AppendUvarint(data, uint64(ids[i]>>lowBits-ids[i-1]>>lowBits))
		low := uint32(ids[i] & lowMask)
		data = append(data, byte(low>>16), byte(low>>8), byte(low))
	}
	return ids[0], data
}

func unpack(first snowflake.ID, data []byte) ([]snowflake.ID, error) {
	if len(data) == 0 || data[0] != blockV1 {
		return nil, errBlock
	}
	ids := []snowflake.ID{first}
	ms := uint64(first >> lowBits)
	for p := 1; p < len(data); {
		gap, n := binary.Uvarint(data[p:])
		if n <= 0 || p+n+3 > len(data) {
			return nil, errBlock
		}
		p += n
		ms += gap
		low := uint64(data[p])<<16 | uint64(data[p+1])<<8 | uint64(data[p+2])
		p += 3
		ids = append(ids, snowflake.ID(ms<<lowBits|low))
	}
	return ids, nil
}

// block is one stored posting block.
type block struct {
	first snowflake.ID
	ids   []snowflake.ID
}

// mark is how far a channel has been read, when its archived threads were
// last listed in full, and its parent: a thread's channel, a channel's
// category, 0 for none.
type mark struct {
	through snowflake.ID
	listed  time.Time
	parent  snowflake.ID
}

// index is where postings and read marks live: Postgres, or memory when
// skua runs without one.
type index interface {
	marks(ctx context.Context, guild snowflake.ID) (map[snowflake.ID]mark, error)
	// flush appends found (author -> ascending IDs) for ch and moves its
	// mark to through, together: a read that stops resumes from the mark.
	flush(ctx context.Context, guild, ch, through snowflake.ID, found map[snowflake.ID][]snowflake.ID) error
	// listed records that parent's archived threads were all listed at.
	listed(ctx context.Context, guild, parent snowflake.ID, at time.Time) error
	// parents records each channel's parent (channel -> parent).
	parents(ctx context.Context, guild snowflake.ID, up map[snowflake.ID]snowflake.ID) error
	load(ctx context.Context, guild, author snowflake.ID) (map[snowflake.ID][]block, error)
	// settle replaces the blocks starting at firsts with one of left: what
	// a purge loaded, less what Discord confirmed gone.
	settle(ctx context.Context, guild, author, ch snowflake.ID, firsts []snowflake.ID, left []snowflake.ID) error
	drop(ctx context.Context, guild snowflake.ID) error
}

type pgIndex struct{ db DB }

func (x pgIndex) marks(ctx context.Context, guild snowflake.ID) (map[snowflake.ID]mark, error) {
	rows, err := x.db.Query(ctx, `select channel_id, read_through, threads_listed_at, parent_id from purge_channels where guild_id = $1`, int64(guild))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[snowflake.ID]mark{}
	for rows.Next() {
		var ch, through, parent int64
		var listed *time.Time
		if err := rows.Scan(&ch, &through, &listed, &parent); err != nil {
			return nil, err
		}
		mk := mark{through: snowflake.ID(through), parent: snowflake.ID(parent)}
		if listed != nil {
			mk.listed = *listed
		}
		out[snowflake.ID(ch)] = mk
	}
	return out, rows.Err()
}

func (x pgIndex) flush(ctx context.Context, guild, ch, through snowflake.ID, found map[snowflake.ID][]snowflake.ID) error {
	tx, err := x.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if len(found) > 0 {
		rows := make([][]any, 0, len(found))
		for author, ids := range found {
			first, data := pack(ids)
			rows = append(rows, []any{int64(guild), int64(author), int64(ch), int64(first), data})
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"purge_postings"},
			[]string{"guild_id", "author_id", "channel_id", "first_id", "ids"}, pgx.CopyFromRows(rows)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `insert into purge_channels (guild_id, channel_id, read_through) values ($1, $2, $3)
		on conflict (guild_id, channel_id) do update set read_through = greatest(purge_channels.read_through, excluded.read_through)`,
		int64(guild), int64(ch), int64(through)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (x pgIndex) listed(ctx context.Context, guild, parent snowflake.ID, at time.Time) error {
	_, err := x.db.Exec(ctx, `insert into purge_channels (guild_id, channel_id, threads_listed_at) values ($1, $2, $3)
		on conflict (guild_id, channel_id) do update set threads_listed_at = excluded.threads_listed_at`,
		int64(guild), int64(parent), at)
	return err
}

func (x pgIndex) parents(ctx context.Context, guild snowflake.ID, up map[snowflake.ID]snowflake.ID) error {
	chs, ps := make([]int64, 0, len(up)), make([]int64, 0, len(up))
	for ch, p := range up {
		chs, ps = append(chs, int64(ch)), append(ps, int64(p))
	}
	_, err := x.db.Exec(ctx, `insert into purge_channels (guild_id, channel_id, parent_id)
		select $1, unnest($2::bigint[]), unnest($3::bigint[])
		on conflict (guild_id, channel_id) do update set parent_id = excluded.parent_id`,
		int64(guild), chs, ps)
	return err
}

func (x pgIndex) load(ctx context.Context, guild, author snowflake.ID) (map[snowflake.ID][]block, error) {
	rows, err := x.db.Query(ctx, `select channel_id, first_id, ids from purge_postings where guild_id = $1 and author_id = $2`, int64(guild), int64(author))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[snowflake.ID][]block{}
	for rows.Next() {
		var ch, first int64
		var data []byte
		if err := rows.Scan(&ch, &first, &data); err != nil {
			return nil, err
		}
		ids, err := unpack(snowflake.ID(first), data)
		if err != nil {
			return nil, err
		}
		out[snowflake.ID(ch)] = append(out[snowflake.ID(ch)], block{snowflake.ID(first), ids})
	}
	return out, rows.Err()
}

func (x pgIndex) settle(ctx context.Context, guild, author, ch snowflake.ID, firsts []snowflake.ID, left []snowflake.ID) error {
	tx, err := x.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	keys := make([]int64, len(firsts))
	for i, f := range firsts {
		keys[i] = int64(f)
	}
	if _, err := tx.Exec(ctx, `delete from purge_postings where guild_id = $1 and author_id = $2 and channel_id = $3 and first_id = any($4)`,
		int64(guild), int64(author), int64(ch), keys); err != nil {
		return err
	}
	if len(left) > 0 {
		first, data := pack(left)
		if _, err := tx.Exec(ctx, `insert into purge_postings (guild_id, author_id, channel_id, first_id, ids) values ($1, $2, $3, $4, $5)`,
			int64(guild), int64(author), int64(ch), int64(first), data); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (x pgIndex) drop(ctx context.Context, guild snowflake.ID) error {
	tx, err := x.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, table := range []string{"purge_postings", "purge_channels", "purge_subs"} {
		if _, err := tx.Exec(ctx, "delete from "+table+" where guild_id = $1", int64(guild)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// memIndex is the index without a database: the same blocks, kept for as
// long as the process runs.
type memIndex struct {
	mu    sync.Mutex
	mk    map[snowflake.ID]map[snowflake.ID]mark // guild -> channel ->
	posts map[[3]snowflake.ID][]memBlock         // guild, author, channel ->
}

type memBlock struct {
	first snowflake.ID
	data  []byte
}

func newMemIndex() *memIndex {
	return &memIndex{mk: map[snowflake.ID]map[snowflake.ID]mark{}, posts: map[[3]snowflake.ID][]memBlock{}}
}

func (x *memIndex) marks(_ context.Context, guild snowflake.ID) (map[snowflake.ID]mark, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := map[snowflake.ID]mark{}
	for ch, mk := range x.mk[guild] {
		out[ch] = mk
	}
	return out, nil
}

func (x *memIndex) setMark(guild, ch snowflake.ID, f func(*mark)) {
	if x.mk[guild] == nil {
		x.mk[guild] = map[snowflake.ID]mark{}
	}
	mk := x.mk[guild][ch]
	f(&mk)
	x.mk[guild][ch] = mk
}

func (x *memIndex) flush(_ context.Context, guild, ch, through snowflake.ID, found map[snowflake.ID][]snowflake.ID) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	for author, ids := range found {
		k := [3]snowflake.ID{guild, author, ch}
		first, data := pack(ids)
		x.posts[k] = append(x.posts[k], memBlock{first, data})
	}
	x.setMark(guild, ch, func(mk *mark) { mk.through = max(mk.through, through) })
	return nil
}

func (x *memIndex) listed(_ context.Context, guild, parent snowflake.ID, at time.Time) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.setMark(guild, parent, func(mk *mark) { mk.listed = at })
	return nil
}

func (x *memIndex) parents(_ context.Context, guild snowflake.ID, up map[snowflake.ID]snowflake.ID) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	for ch, p := range up {
		x.setMark(guild, ch, func(mk *mark) { mk.parent = p })
	}
	return nil
}

func (x *memIndex) load(_ context.Context, guild, author snowflake.ID) (map[snowflake.ID][]block, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := map[snowflake.ID][]block{}
	for k, bs := range x.posts {
		if k[0] != guild || k[1] != author {
			continue
		}
		for _, b := range bs {
			ids, err := unpack(b.first, b.data)
			if err != nil {
				return nil, err
			}
			out[k[2]] = append(out[k[2]], block{b.first, ids})
		}
	}
	return out, nil
}

func (x *memIndex) settle(_ context.Context, guild, author, ch snowflake.ID, firsts []snowflake.ID, left []snowflake.ID) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	k := [3]snowflake.ID{guild, author, ch}
	bs := slices.DeleteFunc(x.posts[k], func(b memBlock) bool { return slices.Contains(firsts, b.first) })
	if len(left) > 0 {
		first, data := pack(left)
		bs = append(bs, memBlock{first, data})
	}
	if len(bs) == 0 {
		delete(x.posts, k)
		return nil
	}
	x.posts[k] = bs
	return nil
}

func (x *memIndex) drop(_ context.Context, guild snowflake.ID) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	delete(x.mk, guild)
	for k := range x.posts {
		if k[0] == guild {
			delete(x.posts, k)
		}
	}
	return nil
}
