package purge

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	// workers is how many channels one catch-up reads at once.
	workers = 16
	// lanes is how many channels one purge deletes from at once. Discord
	// limits deletes per channel, old ones hard, so the way to delete fast
	// is many channels at a time; the pacer keeps the total honest.
	lanes = 64
	// page is the most messages one read returns.
	page = 100
	// flushPages is how many pages a read holds before writing them to the
	// index and moving its mark: what a stopped first read can lose.
	flushPages = 100
	// bulkMax is the most IDs one bulk delete takes.
	bulkMax = 100
	// young is how recent a message must be to go in a bulk delete, which
	// refuses anything 14 days old. The hour is margin for a long purge.
	young = 14*24*time.Hour - time.Hour
)

var errRefused = core.Tell("skua's delete budget for this server is spent or discord is struggling; try again in a few minutes")

// errChannelDone ends one channel's deletes without failing the rest.
var errChannelDone = errors.New("purge: channel done")

// spot is a channel or thread a catch-up may read. parent is set for a
// thread, and last is the newest message Discord says it has.
type spot struct {
	id, parent snowflake.ID
	last       *snowflake.ID
}

// scan is one catch-up of a guild's index: every channel and thread with
// anything newer than its mark is read from the mark, and every author's
// IDs go in. One runs per guild at a time, for every purge there.
type scan struct {
	r      rest.Rest
	guard  *guard.Guard
	pace   *pacer
	idx    index
	guild  snowflake.ID
	cutoff snowflake.ID // the read stops here
	began  time.Time

	scanned, channels, done atomic.Int64
	listed                  atomic.Bool // channels holds the full count

	mu          sync.Mutex
	unreachable []snowflake.ID
	partial     map[snowflake.ID]bool // parents with a thread that wasn't read

	// up is every listed channel's and thread's parent, 0 for none, for
	// scoped purges to climb.
	up map[snowflake.ID]snowflake.ID
}

// run reads every spot with something new, up to workers at once. It stops
// early only for a cancel or an index that won't take writes; a channel
// skua can't read is noted and skipped, and keeps its mark for next time.
func (s *scan) run(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	marks, err := s.idx.marks(ctx, s.guild)
	if err != nil {
		return err
	}
	spots, listed, err := s.targets(ctx, marks)
	if err != nil {
		return err
	}
	moved := map[snowflake.ID]snowflake.ID{}
	for ch, p := range s.up {
		if marks[ch].parent != p {
			moved[ch] = p
		}
	}
	if len(moved) > 0 {
		if err := s.idx.parents(ctx, s.guild, moved); err != nil {
			return err
		}
	}
	s.channels.Store(int64(len(spots)))
	s.listed.Store(true)
	queue := make(chan spot)
	var wg sync.WaitGroup
	for range min(workers, len(spots)) {
		wg.Go(func() {
			for sp := range queue {
				if err := s.read(ctx, sp, marks[sp.id].through); err != nil {
					cancel(err)
				}
				s.done.Add(1)
			}
		})
	}
feed:
	for _, sp := range spots {
		if sp.last != nil && *sp.last <= marks[sp.id].through {
			s.done.Add(1) // nothing new since the last read
			continue
		}
		select {
		case queue <- sp:
		case <-ctx.Done():
			break feed
		}
	}
	close(queue)
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return err
	}
	for _, parent := range listed {
		if !s.partial[parent] {
			if err := s.idx.listed(ctx, s.guild, parent, s.began); err != nil {
				return err
			}
		}
	}
	return nil
}

// targets is every channel and thread that can hold messages: text, news,
// voice and stage chats, every active thread, and the archived threads of
// text, news, forum and media channels. Only text channels have private
// threads. listed is the parents whose archived threads were all listed.
//
// Archived threads come newest-archived first, and posting in one
// unarchives it, so a parent listed in full before stops at the first page
// archived before that.
func (s *scan) targets(ctx context.Context, marks map[snowflake.ID]mark) (spots []spot, listed []snowflake.ID, err error) {
	if err := s.pace.wait(ctx); err != nil {
		return nil, nil, err
	}
	chans, err := s.r.GetGuildChannels(s.guild, rest.WithCtx(ctx))
	if err != nil {
		return nil, nil, err
	}
	s.up = map[snowflake.ID]snowflake.ID{}
	for _, c := range chans {
		id, since := c.ID(), marks[c.ID()].listed
		s.up[id] = 0
		if p := c.ParentID(); p != nil {
			s.up[id] = *p
		}
		var ok bool
		switch c.Type() {
		case discord.ChannelTypeGuildText:
			spots = append(spots, spotOf(c, 0))
			spots, ok = s.archived(ctx, id, s.r.GetPublicArchivedThreads, since, spots)
			if ok {
				spots, ok = s.archived(ctx, id, s.r.GetPrivateArchivedThreads, since, spots)
			}
		case discord.ChannelTypeGuildNews:
			spots = append(spots, spotOf(c, 0))
			spots, ok = s.archived(ctx, id, s.r.GetPublicArchivedThreads, since, spots)
		case discord.ChannelTypeGuildVoice, discord.ChannelTypeGuildStageVoice:
			spots = append(spots, spotOf(c, 0))
			continue
		case discord.ChannelTypeGuildForum, discord.ChannelTypeGuildMedia:
			spots, ok = s.archived(ctx, id, s.r.GetPublicArchivedThreads, since, spots)
		default:
			continue
		}
		if ok {
			listed = append(listed, id)
		}
	}
	if err := s.pace.wait(ctx); err != nil {
		return nil, nil, err
	}
	active, err := s.r.GetActiveGuildThreads(s.guild, rest.WithCtx(ctx))
	if err != nil {
		return nil, nil, err
	}
	for _, t := range active.Threads {
		var parent snowflake.ID
		if p := t.ParentID(); p != nil {
			parent = *p
		}
		spots = append(spots, spotOf(t, parent))
	}
	// A thread archived between two listings is read once.
	seen := map[snowflake.ID]bool{}
	spots = slices.DeleteFunc(spots, func(sp spot) bool {
		dup := seen[sp.id]
		seen[sp.id] = true
		return dup
	})
	for _, sp := range spots {
		if sp.parent != 0 {
			s.up[sp.id] = sp.parent
		}
	}
	return spots, listed, ctx.Err()
}

func spotOf(c discord.Channel, parent snowflake.ID) spot {
	sp := spot{id: c.ID(), parent: parent}
	if mc, ok := c.(discord.MessageChannel); ok {
		sp.last = mc.LastMessageID()
	}
	return sp
}

type listThreads func(channel snowflake.ID, before time.Time, limit int, opts ...rest.RequestOpt) (*discord.GetThreads, error)

// archived appends parent's archived threads from list, newest first, down
// to since. A listing skua is refused, or that fails, marks parent: some of
// its threads were not read, and the member is told so.
//
// before goes to Discord as whole seconds, so the next page asks from the
// end of the last thread's second and skips what this one already listed:
// threads archived in that same second are not lost between pages. A page
// with nothing new is a whole page archived in one second; the next asks
// from that second's start instead, so the listing still moves on.
//
// ponytail: past the first page of threads archived in one second, the
// rest of that second is skipped. Page by thread ID if Discord offers it.
func (s *scan) archived(ctx context.Context, parent snowflake.ID, list listThreads, since time.Time, spots []spot) ([]spot, bool) {
	var before time.Time
	seen := map[snowflake.ID]bool{}
	for {
		if s.pace.wait(ctx) != nil {
			return spots, false
		}
		res, err := list(parent, before, page, rest.WithCtx(ctx))
		if err != nil {
			if ctx.Err() == nil {
				s.unreach(parent, 0)
			}
			return spots, false
		}
		fresh := 0
		for _, t := range res.Threads {
			if !seen[t.ID()] {
				seen[t.ID()] = true
				spots = append(spots, spotOf(t, parent))
				fresh++
			}
		}
		if !res.HasMore || len(res.Threads) == 0 {
			return spots, true
		}
		oldest := res.Threads[len(res.Threads)-1].ThreadMetadata.ArchiveTimestamp
		if oldest.Before(since) {
			return spots, true
		}
		before = oldest.Truncate(time.Second)
		if fresh > 0 {
			before = before.Add(time.Second)
		}
	}
}

// read walks sp's history forward from its mark, a page at a time, and
// writes every author's IDs to the index every flushPages pages and at the
// end. A channel skua can't read keeps what it did read, and its mark.
func (s *scan) read(ctx context.Context, sp spot, from snowflake.ID) error {
	// An after of 0 is no after at all, which Discord answers with the
	// newest page; 1 starts the walk at the oldest message.
	after := max(from, 1)
	found := map[snowflake.ID][]snowflake.ID{}
	pages := 0
	flush := func() error {
		if after <= from && len(found) == 0 {
			return nil
		}
		for _, ids := range found {
			slices.Sort(ids)
		}
		err := s.idx.flush(ctx, s.guild, sp.id, after, found)
		found, pages = map[snowflake.ID][]snowflake.ID{}, 0
		return err
	}
	for {
		if err := s.pace.wait(ctx); err != nil {
			return err
		}
		msgs, err := s.r.GetMessages(sp.id, 0, 0, after, page, rest.WithCtx(ctx))
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.guard.Report(s.guild, struggling(err))
			s.unreach(sp.id, sp.parent)
			return flush()
		}
		for _, m := range msgs {
			after = max(after, m.ID)
			found[m.Author.ID] = append(found[m.Author.ID], m.ID)
		}
		s.scanned.Add(int64(len(msgs)))
		if len(msgs) < page || after >= s.cutoff {
			return flush()
		}
		if pages++; pages == flushPages {
			if err := flush(); err != nil {
				return err
			}
		}
	}
}

// progress is how many channels are read, with a bar, or that they're
// still being listed.
func (s *scan) progress() string {
	if !s.listed.Load() {
		return listing
	}
	done, total := s.done.Load(), s.channels.Load()
	return fmt.Sprintf("%d of %d %s", done, total, bar(done, total))
}

// unreach notes id as unreadable, and its parent as listed only in part.
func (s *scan) unreach(id, parent snowflake.ID) {
	s.mu.Lock()
	s.unreachable = append(s.unreachable, id)
	if parent != 0 {
		if s.partial == nil {
			s.partial = map[snowflake.ID]bool{}
		}
		s.partial[parent] = true
	}
	s.mu.Unlock()
}

// sweep deletes its authors' indexed messages from a guild, sent before
// cutoff, one lane per channel. Live deletes use its bulk and one alone.
type sweep struct {
	r       rest.Rest
	guard   *guard.Guard
	pace    *pacer
	idx     index
	guild   snowflake.ID
	authors map[snowflake.ID]*atomic.Int64 // each one's deleted count
	in      map[snowflake.ID]scope         // where each author's go; none is everywhere
	cutoff  snowflake.ID
	now     func() time.Time
	// why heads the reason Discord's audit log shows for each delete;
	// the members whose messages they are follow it.
	why string

	deleted, missed atomic.Int64
	// total is how many messages run has to delete, known once the index
	// is loaded; handled is how many of them are settled either way.
	total, handled atomic.Int64
	confirmed      sync.Map // message ID -> struct{}: Discord says it's gone

	mu          sync.Mutex
	unreachable []snowflake.ID
}

// posting is one author's blocks in one channel.
type posting struct {
	author snowflake.ID
	blocks []block
}

// run loads each author's postings and deletes them, up to lanes channels
// at once. It returns the first error that has to stop everything: a
// cancel, guard refusing more deletes, or the index failing.
func (s *sweep) run(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	up, err := s.parents(ctx)
	if err != nil {
		return err
	}
	byCh := map[snowflake.ID][]posting{}
	for author := range s.authors {
		bs, err := s.idx.load(ctx, s.guild, author)
		if err != nil {
			return err
		}
		for ch, b := range bs {
			if !s.in[author].covers(ch, up) {
				continue
			}
			byCh[ch] = append(byCh[ch], posting{author, b})
			for _, blk := range b {
				for _, id := range blk.ids {
					if id < s.cutoff {
						s.total.Add(1)
					}
				}
			}
		}
	}
	slots := make(chan struct{}, lanes)
	var wg sync.WaitGroup
	for ch, ps := range byCh {
		wg.Go(func() {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			if err := s.lane(ctx, ch, ps); err != nil {
				cancel(err)
			}
		})
	}
	wg.Wait()
	return context.Cause(ctx)
}

// parents is how run climbs from a channel to its parent, from the index
// the catch-up just wrote; read only when some author has a scope.
func (s *sweep) parents(ctx context.Context) (func(snowflake.ID) snowflake.ID, error) {
	none := func(snowflake.ID) snowflake.ID { return 0 }
	scoped := false
	for _, sc := range s.in {
		scoped = scoped || len(sc) > 0
	}
	if !scoped {
		return none, nil
	}
	marks, err := s.idx.marks(ctx, s.guild)
	if err != nil {
		return none, err
	}
	return func(ch snowflake.ID) snowflake.ID { return marks[ch].parent }, nil
}

// lane deletes one channel's messages: recent ones a hundred at a time
// first, as they're the ones people see, then old ones singly, which is
// all Discord allows for them. Whatever happens, the index then keeps
// only what Discord didn't confirm gone, for the next purge.
func (s *sweep) lane(ctx context.Context, ch snowflake.ID, ps []posting) error {
	var recent, old []msg
	for _, p := range ps {
		for _, b := range p.blocks {
			for _, id := range b.ids {
				switch {
				case id >= s.cutoff:
				case s.now().Sub(id.Time()) < young:
					recent = append(recent, msg{id, p.author})
				default:
					old = append(old, msg{id, p.author})
				}
			}
		}
	}
	err := func() error {
		for chunk := range slices.Chunk(recent, bulkMax) {
			if err := s.bulk(ctx, ch, chunk); err != nil {
				return err
			}
		}
		for _, m := range old {
			if err := s.one(ctx, ch, m); err != nil {
				return err
			}
		}
		return nil
	}()
	if serr := s.settle(ch, ps); serr != nil && (err == nil || errors.Is(err, errChannelDone)) {
		return serr
	}
	if errors.Is(err, errChannelDone) {
		return nil
	}
	return err
}

// settle writes back each author's blocks in ch less what is confirmed
// gone. It runs even after a cancel, so it has its own deadline.
func (s *sweep) settle(ch snowflake.ID, ps []posting) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, p := range ps {
		var firsts, left []snowflake.ID
		for _, b := range p.blocks {
			firsts = append(firsts, b.first)
			for _, id := range b.ids {
				if _, gone := s.confirmed.Load(id); !gone {
					left = append(left, id)
				}
			}
		}
		slices.Sort(left)
		if err := s.idx.settle(ctx, s.guild, p.author, ch, firsts, slices.Compact(left)); err != nil {
			return err
		}
	}
	return nil
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
	err := s.r.BulkDeleteMessages(ch, ids, rest.WithCtx(ctx), s.reason(ms))
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
	err := s.r.DeleteMessage(ch, m.id, rest.WithCtx(ctx), s.reason([]msg{m}))
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
		s.confirmed.Store(m.id, struct{}{})
		s.handled.Add(1)
	default:
		s.missed.Add(1)
		s.handled.Add(1)
	}
	return nil
}

// msg is a message to delete and whose it is.
type msg struct{ id, author snowflake.ID }

// reasonMax is Discord's 512 characters for an audit log reason, less room
// for the " and more" that ends a list cut short.
const reasonMax = 512 - len(" and more")

// reason is the audit log reason for deleting ms: s.why, then each of
// their authors once. Plain ASCII, so it fits Discord's cap as it stands.
func (s *sweep) reason(ms []msg) rest.RequestOpt {
	r, sep := s.why, " "
	seen := map[snowflake.ID]bool{}
	for _, m := range ms {
		if seen[m.author] {
			continue
		}
		seen[m.author] = true
		id := sep + m.author.String()
		if len(r)+len(id) > reasonMax {
			r += " and more"
			break
		}
		r, sep = r+id, ", "
	}
	return rest.WithReason(r)
}

// gone counts m deleted, for the sweep and for its author.
func (s *sweep) gone(m msg) {
	s.confirmed.Store(m.id, struct{}{})
	s.deleted.Add(1)
	s.handled.Add(1)
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
