package purge

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/guard"
)

const (
	// workers is how many channels one sweep reads at once. Each channel's
	// reads and deletes are separate buckets on Discord's side, so a
	// channel is two requests in flight; the pacer keeps the total honest.
	workers = 16
	// page is the most messages one read returns.
	page = 100
	// bulkMax is the most IDs one bulk delete takes.
	bulkMax = 100
	// young is how recent a message must be to go in a bulk delete, which
	// refuses anything 14 days old. The hour is margin for a long sweep.
	young = 14*24*time.Hour - time.Hour
)

var errRefused = core.Tell("skua's delete budget for this server is spent or discord is struggling; try again in a few minutes")

// errChannelDone ends one channel's sweep without failing the rest.
var errChannelDone = errors.New("purge: channel done")

// sweep is one pass over a guild: every channel and thread skua can read,
// deleting each message from authors with an ID in (from, cutoff). Reading
// walks forward from from, so a sweep that only needs what is new reads
// only that.
type sweep struct {
	r       rest.Rest
	guard   *guard.Guard
	pace    *pacer
	guild   snowflake.ID
	authors map[snowflake.ID]*atomic.Int64 // each one's deleted count
	from    snowflake.ID
	cutoff  snowflake.ID
	now     func() time.Time

	scanned, deleted, missed atomic.Int64
	channels, done           atomic.Int64

	mu          sync.Mutex
	unreachable []snowflake.ID
}

// run reads every target with up to workers channels at once. It returns
// the first error that has to stop the whole sweep: a cancel, or guard
// refusing more deletes. A channel skua cannot read is noted and skipped.
func (s *sweep) run(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	ids, err := s.targets(ctx)
	if err != nil {
		return err
	}
	s.channels.Store(int64(len(ids)))
	queue := make(chan snowflake.ID)
	var wg sync.WaitGroup
	for range min(workers, len(ids)) {
		wg.Go(func() {
			for id := range queue {
				if err := s.channel(ctx, id); err != nil {
					cancel(err)
				}
				s.done.Add(1)
			}
		})
	}
feed:
	for _, id := range ids {
		select {
		case queue <- id:
		case <-ctx.Done():
			break feed
		}
	}
	close(queue)
	wg.Wait()
	return context.Cause(ctx)
}

// targets is every channel and thread that can hold messages: text, news,
// voice and stage chats, every active thread, and the archived threads of
// text, news, forum and media channels.
func (s *sweep) targets(ctx context.Context) ([]snowflake.ID, error) {
	if err := s.pace.wait(ctx); err != nil {
		return nil, err
	}
	chans, err := s.r.GetGuildChannels(s.guild, rest.WithCtx(ctx))
	if err != nil {
		return nil, err
	}
	var ids []snowflake.ID
	for _, c := range chans {
		switch c.Type() {
		case discord.ChannelTypeGuildText, discord.ChannelTypeGuildNews:
			ids = append(ids, c.ID())
			ids = s.archived(ctx, c.ID(), s.r.GetPublicArchivedThreads, ids)
			ids = s.archived(ctx, c.ID(), s.r.GetPrivateArchivedThreads, ids)
		case discord.ChannelTypeGuildVoice, discord.ChannelTypeGuildStageVoice:
			ids = append(ids, c.ID())
		case discord.ChannelTypeGuildForum, discord.ChannelTypeGuildMedia:
			ids = s.archived(ctx, c.ID(), s.r.GetPublicArchivedThreads, ids)
		}
	}
	if err := s.pace.wait(ctx); err != nil {
		return nil, err
	}
	active, err := s.r.GetActiveGuildThreads(s.guild, rest.WithCtx(ctx))
	if err != nil {
		return nil, err
	}
	for _, t := range active.Threads {
		ids = append(ids, t.ID())
	}
	return ids, ctx.Err()
}

type listThreads func(channel snowflake.ID, before time.Time, limit int, opts ...rest.RequestOpt) (*discord.GetThreads, error)

// archived appends parent's archived threads from list, newest first. A
// listing skua is refused, or that fails, marks parent: some of its
// threads were not read, and the member is told so.
func (s *sweep) archived(ctx context.Context, parent snowflake.ID, list listThreads, ids []snowflake.ID) []snowflake.ID {
	var before time.Time
	for {
		if s.pace.wait(ctx) != nil {
			return ids
		}
		res, err := list(parent, before, page, rest.WithCtx(ctx))
		if err != nil {
			if ctx.Err() == nil {
				s.unreach(parent)
			}
			return ids
		}
		for _, t := range res.Threads {
			ids = append(ids, t.ID())
		}
		if !res.HasMore || len(res.Threads) == 0 {
			return ids
		}
		before = res.Threads[len(res.Threads)-1].ThreadMetadata.ArchiveTimestamp
	}
}

// channel sweeps one channel: a reader walks its history forward while a
// deleter works through what the reader found, so neither waits on the
// other's bucket.
func (s *sweep) channel(ctx context.Context, id snowflake.ID) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	found := make(chan []msg, 4)
	var derr error
	var wg sync.WaitGroup
	wg.Go(func() {
		if derr = s.drain(ctx, id, found); derr != nil {
			cancel()
			for range found { // let the reader finish its send and close
			}
		}
	})
	rerr := s.read(ctx, id, found)
	wg.Wait()
	switch {
	case errors.Is(derr, errChannelDone):
		return nil
	case derr != nil:
		return derr
	case rerr != nil && !errors.Is(rerr, context.Canceled):
		return rerr
	}
	return ctx.Err()
}

// read walks id's history forward from s.from, a page at a time, and hands
// the deleter each page's messages by authors. The author is the one on
// the fetched message, the only thing that decides what is deleted.
func (s *sweep) read(ctx context.Context, id snowflake.ID, found chan<- []msg) error {
	defer close(found)
	after := s.from
	for {
		if err := s.pace.wait(ctx); err != nil {
			return err
		}
		msgs, err := s.r.GetMessages(id, 0, 0, after, page, rest.WithCtx(ctx))
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.guard.Report(s.guild, struggling(err))
			s.unreach(id)
			return nil
		}
		var mine []msg
		var scanned int64
		for _, m := range msgs {
			after = max(after, m.ID)
			if m.ID >= s.cutoff {
				continue
			}
			scanned++
			if s.authors[m.Author.ID] != nil {
				mine = append(mine, msg{m.ID, m.Author.ID})
			}
		}
		s.scanned.Add(scanned)
		if len(mine) > 0 {
			select {
			case found <- mine:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if len(msgs) < page || after >= s.cutoff {
			return nil
		}
	}
}

// drain deletes what read found: recent messages a hundred at a time,
// older ones singly, which is all Discord allows for them.
func (s *sweep) drain(ctx context.Context, ch snowflake.ID, found <-chan []msg) error {
	batch := make([]msg, 0, bulkMax)
	for ms := range found {
		for _, m := range ms {
			if s.now().Sub(m.id.Time()) >= young {
				if err := s.one(ctx, ch, m); err != nil {
					return err
				}
				continue
			}
			if batch = append(batch, m); len(batch) == bulkMax {
				if err := s.bulk(ctx, ch, batch); err != nil {
					return err
				}
				batch = batch[:0]
			}
		}
	}
	return s.bulk(ctx, ch, batch)
}

func (s *sweep) bulk(ctx context.Context, ch snowflake.ID, ms []msg) error {
	switch len(ms) {
	case 0:
		return nil
	case 1:
		return s.one(ctx, ch, ms[0])
	}
	if err := s.spend(ctx); err != nil {
		return err
	}
	ids := make([]snowflake.ID, len(ms))
	for i, m := range ms {
		ids[i] = m.id
	}
	err := s.r.BulkDeleteMessages(ch, ids, rest.WithCtx(ctx))
	s.guard.Report(s.guild, struggling(err))
	status, _ := answer(err)
	switch {
	case err == nil:
		for _, m := range ms {
			s.gone(m)
		}
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case status == 403:
		s.unreach(ch)
		return errChannelDone
	}
	// Too old after all, or anything else: one at a time, so one bad ID
	// costs only itself.
	for _, m := range ms {
		if err := s.one(ctx, ch, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *sweep) one(ctx context.Context, ch snowflake.ID, m msg) error {
	if err := s.spend(ctx); err != nil {
		return err
	}
	err := s.r.DeleteMessage(ch, m.id, rest.WithCtx(ctx))
	s.guard.Report(s.guild, struggling(err))
	status, code := answer(err)
	switch {
	case err == nil:
		s.gone(m)
	case ctx.Err() != nil:
		return ctx.Err()
	case status == 403:
		s.unreach(ch)
		return errChannelDone
	case status == 404, code == rest.JSONErrorCodeCannotExecuteActionOnSystemMessage:
		// Already gone, or a system message nobody can delete.
	default:
		s.missed.Add(1)
	}
	return nil
}

// msg is a message to delete and whose it is.
type msg struct{ id, author snowflake.ID }

// gone counts m deleted, for the sweep and for its author.
func (s *sweep) gone(m msg) {
	s.deleted.Add(1)
	if c := s.authors[m.author]; c != nil {
		c.Add(1)
	}
}

// spend waits for a request slot, then takes one from the guild's purge
// budget. Live deletes have no pacer and go at once.
func (s *sweep) spend(ctx context.Context) error {
	if s.pace != nil {
		if err := s.pace.wait(ctx); err != nil {
			return err
		}
	}
	if err := s.guard.Allow(s.guild, guard.Purge); err != nil {
		return errRefused
	}
	return nil
}

func (s *sweep) unreach(id snowflake.ID) {
	s.mu.Lock()
	s.unreachable = append(s.unreachable, id)
	s.mu.Unlock()
}

// answer is the HTTP status and Discord's error code behind err, if any.
func answer(err error) (int, rest.JSONErrorCode) {
	re, ok := errors.AsType[*rest.Error](err)
	if !ok || re.Response == nil {
		return 0, 0
	}
	return re.Response.StatusCode, re.Code
}

// struggling is true only for answers that say Discord is, not the request.
func struggling(err error) bool {
	s, _ := answer(err)
	return s == 429 || s >= 500
}
