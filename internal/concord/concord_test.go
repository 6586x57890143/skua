// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package concord

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

// testdata/upstream.json is made by armada-discord-bridge's own TypeScript
// (concord-core), so these tests check the port against the real thing:
// keys, hashes, opened wraps and whole folds must come out the same.
type upstream struct {
	Derive struct {
		Secret, ID   string
		Channel      struct{ SK, PK, Conv string }
		Control      string
		Guestbook    string
		Grant        string
		Banlist      string
		Invite       string
		Community    string
		Edition      string
		EditionFirst string `json:"editionFirst"`
	}
	Puppet    struct{ Master, User, SK, PK string }
	Community struct {
		Owner, Salt, Root, ID string
		Members               map[string]string
		A, B                  scenario
	}
	Chat struct {
		Channel, Content, Author string
		Good, Splice, Epoch      struct {
			RumorID string `json:"rumorId"`
			Wrap    *nostr.Event
		}
	}
	Invite struct {
		URL, Stock, Signer string
		Event, Liar        *nostr.Event
	}
}

type scenario struct {
	Wraps []*nostr.Event
	Fold  struct {
		Banned   []string
		Channels map[string]struct {
			Name    string
			Private bool
			Deleted bool
		}
		Roles []struct {
			ID       string
			Position float64
		}
		Grants     []string
		Moderators []bool
	}
}

func load(t *testing.T) upstream {
	t.Helper()
	raw, err := os.ReadFile("testdata/upstream.json")
	if err != nil {
		t.Fatal(err)
	}
	var u upstream
	if err := json.Unmarshal(raw, &u); err != nil {
		t.Fatal(err)
	}
	return u
}

func must32(t *testing.T, s string) [32]byte {
	t.Helper()
	b, err := hex32(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDeriveMatchesUpstream(t *testing.T) {
	u := load(t)
	d := u.Derive
	secret, id := must32(t, d.Secret), must32(t, d.ID)
	ch := ChannelKey(secret, id, 3)
	checks := map[string][2]string{
		"channel sk":    {ch.SK, d.Channel.SK},
		"channel pk":    {ch.PK, d.Channel.PK},
		"channel conv":  {hex.EncodeToString(ch.Conv[:]), d.Channel.Conv},
		"control":       {controlKey(secret, id, 0).PK, d.Control},
		"guestbook":     {guestbookKey(secret, id, 2).PK, d.Guestbook},
		"grant":         {hexOf(grantLocator(id, secret)), d.Grant},
		"banlist":       {hexOf(banlistLocator(id)), d.Banlist},
		"community":     {hexOf(communityID(secret, id)), d.Community},
		"edition":       {hexOf(editionHash(id, 7, &secret, "héllo <&>  ")), d.Edition},
		"edition first": {hexOf(editionHash(id, 1, nil, "")), d.EditionFirst},
	}
	tok := make([]byte, 16)
	for i := range tok {
		tok[i] = 5
	}
	k := inviteBundleKey(tok)
	checks["invite"] = [2]string{hex.EncodeToString(k[:]), d.Invite}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s: got %s, upstream %s", name, c[0], c[1])
		}
	}
}

func TestOpenChatMatchesUpstream(t *testing.T) {
	u := load(t)
	root, cid := must32(t, u.Community.Root), must32(t, u.Chat.Channel)
	ch := ChatChannel{ID: cid, IDHex: u.Chat.Channel, Streams: []Stream{{0, ChannelKey(root, cid, 0)}}}
	o, err := OpenChat(u.Chat.Good.Wrap, ch)
	if err != nil {
		t.Fatal(err)
	}
	if o.RumorID != u.Chat.Good.RumorID || o.Content != u.Chat.Content || o.Author != u.Chat.Author || o.MS != 1_800_000_123_456 {
		t.Fatalf("opened %+v", o)
	}
	if Tag(o.Tags, "q") != strings.Repeat("ab", 32) {
		t.Fatalf("tags %v", o.Tags)
	}
	// Bound to another channel, or to an epoch whose key did not open it.
	for name, w := range map[string]*nostr.Event{"splice": u.Chat.Splice.Wrap, "epoch": u.Chat.Epoch.Wrap} {
		if _, err := OpenChat(w, ch); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	// A wrap at another stream address is not read.
	other := ChatChannel{IDHex: u.Chat.Channel, Streams: []Stream{{0, ChannelKey(root, root, 0)}}}
	if _, err := OpenChat(u.Chat.Good.Wrap, other); err == nil {
		t.Error("opened at a foreign address")
	}
}

func TestRoundTrip(t *testing.T) {
	u := load(t)
	root, cid := must32(t, u.Community.Root), must32(t, u.Chat.Channel)
	ch := ChatChannel{ID: cid, IDHex: u.Chat.Channel, Streams: []Stream{{0, ChannelKey(root, cid, 0)}}}
	ch.Current = ch.Streams[0]
	author, _ := KeyFromSecret([32]byte{4})
	r, err := NewChat(ch, KindMessage, u.Chat.Content, [][]string{{"proxy", "https://discord.com/channels/1/2/3", "web"}}, author.PK, 1_700_000_000_999)
	if err != nil {
		t.Fatal(err)
	}
	w, err := SealChat(r, ch, author.SK)
	if err != nil {
		t.Fatal(err)
	}
	o, err := OpenChat(w, ch)
	if err != nil {
		t.Fatal(err)
	}
	if o.RumorID != r.ID || o.Content != u.Chat.Content || o.MS != 1_700_000_000_999 || !HasTag(o.Tags, "proxy") {
		t.Fatalf("round trip: %+v", o)
	}
	// Someone else's key can't sign as the author: the seal is checked.
	forged := *w
	forged.Content = strings.Replace(w.Content, "A", "B", 1)
	if _, err := OpenChat(&forged, ch); err == nil {
		t.Error("tampered wrap opened")
	}
	if _, err := NewRumor(KindMessage, "x", nil, author.PK, -1); err == nil {
		t.Error("negative send time accepted")
	}
	big, err := NewChat(ch, KindMessage, strings.Repeat("x", maxPlaintext), nil, author.PK, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SealChat(big, ch, author.SK); err == nil {
		t.Error("over the NIP-44 cap and sealed anyway")
	}
}

func TestFoldMatchesUpstream(t *testing.T) {
	u := load(t)
	c := &Community{ID: must32(t, u.Community.ID), Owner: u.Community.Owner, Root: must32(t, u.Community.Root)}
	members := []string{"owner", "admin", "mod", "x", "y"}
	for name, s := range map[string]scenario{"a": u.Community.A, "b": u.Community.B} {
		t.Run(name, func(t *testing.T) {
			f := FoldControl(c, s.Wraps)
			var banned []string
			for pk := range f.Banned {
				banned = append(banned, pk)
			}
			slices.Sort(banned)
			if !slices.Equal(banned, s.Fold.Banned) {
				t.Errorf("banned %v, upstream %v", banned, s.Fold.Banned)
			}
			if len(f.Channels) != len(s.Fold.Channels) {
				t.Errorf("channels %v, upstream %v", f.Channels, s.Fold.Channels)
			}
			for id, want := range s.Fold.Channels {
				got := f.Channels[id]
				if got.Name != want.Name || got.Private != want.Private || got.Deleted != want.Deleted {
					t.Errorf("channel %s: %+v, upstream %+v", id[:8], got, want)
				}
			}
			if len(f.Roster.Roles) != len(s.Fold.Roles) {
				t.Errorf("roles %v, upstream %v", f.Roster.Roles, s.Fold.Roles)
			}
			for _, want := range s.Fold.Roles {
				if r, ok := f.Roster.role(want.ID); !ok || r.Position != want.Position {
					t.Errorf("role %s: %+v", want.ID[:8], r)
				}
			}
			var grants []string
			for _, g := range f.Roster.Grants {
				grants = append(grants, g.Member)
			}
			slices.Sort(grants)
			if !slices.Equal(grants, s.Fold.Grants) {
				t.Errorf("grants %v, upstream %v", grants, s.Fold.Grants)
			}
			for i, m := range members {
				if got := f.IsModerator(u.Community.Members[m]); got != s.Fold.Moderators[i] {
					t.Errorf("%s moderator %v, upstream %v", m, got, s.Fold.Moderators[i])
				}
			}
		})
	}
}

func TestChannelsView(t *testing.T) {
	u := load(t)
	c := &Community{ID: must32(t, u.Community.ID), Owner: u.Community.Owner, Root: must32(t, u.Community.Root), Private: map[string]PrivateKey{}}
	f := FoldControl(c, u.Community.A.Wraps)
	chans := Channels(c, f)
	// The private channel is left out without its key; the deleted one always.
	if len(chans) != 1 || chans[strings.Repeat("21", 32)].Name != "general" {
		t.Fatalf("channels %v", chans)
	}
	c.Private[strings.Repeat("22", 32)] = PrivateKey{Key: [32]byte{1}, Epoch: 4}
	chans = Channels(c, f)
	secret := chans[strings.Repeat("22", 32)]
	if len(chans) != 2 || secret.Current.Epoch != 4 || len(secret.Streams) != 2 || len(secret.Authors()) != 2 {
		t.Fatalf("private channel %+v", secret)
	}
	if Channels(c, nil) == nil || len(Channels(c, nil)) != 0 {
		t.Error("no fold, no channels")
	}
}

func TestInviteMatchesUpstream(t *testing.T) {
	u := load(t)
	inv, err := ParseInvite(u.Invite.URL)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Signer != u.Invite.Signer || !slices.Equal(inv.Bootstrap, []string{"wss://relay.example", "wss://relay.ditto.pub", "https://odd.example/x"}) {
		t.Fatalf("invite %+v", inv)
	}
	stock, err := ParseInvite(u.Invite.Stock)
	if err != nil || !slices.Equal(stock.Bootstrap, stockRelays) {
		t.Fatalf("stock %+v %v", stock, err)
	}
	// The bare form parses the same.
	bare := u.Invite.URL[strings.Index(u.Invite.URL, "naddr1"):]
	if b, err := ParseInvite(bare); err != nil || b.Signer != inv.Signer {
		t.Fatalf("bare %v", err)
	}
	c, err := openBundle(u.Invite.Event, inv.Token, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.IDHex != u.Community.ID || c.Owner != u.Community.Owner || hexOf(c.Root) != u.Community.Root || c.Name != "test" {
		t.Fatalf("community %+v", c)
	}
	if !slices.Equal(c.Relays, []string{"wss://r1.example", "wss://r2.example"}) {
		t.Errorf("relays %v", c.Relays)
	}
	if p := c.Private[strings.Repeat("22", 32)]; p.Epoch != 4 || p.Name != "secret" {
		t.Errorf("private %+v", c.Private)
	}
	// An owner that doesn't reproduce the community id is refused.
	if _, err := openBundle(u.Invite.Liar, inv.Token, 0); err == nil {
		t.Error("lying bundle accepted")
	}
	if _, err := openBundle(u.Invite.Event, make([]byte, 16), 0); err == nil {
		t.Error("opened with the wrong token")
	}
	for _, bad := range []string{"", "https://armada.buzz/x", "https://armada.buzz/invite/naddr1qq#AAAA", bare[:strings.Index(bare, "#")]} {
		if _, err := ParseInvite(bad); err == nil {
			t.Errorf("parsed %q", bad)
		}
	}
}

func TestFragment(t *testing.T) {
	tok := strings.Repeat("\x41", 16)
	enc := func(b ...byte) string { return encodeB64(append(b, tok...)) }
	if _, r, err := decodeFragment(enc(4, 0, 1, 9)); err != nil || len(r) != 0 {
		t.Errorf("unknown dictionary id: %v %v", r, err)
	}
	for name, f := range map[string]string{
		"legacy":   enc(3, 1),
		"newer":    enc(5, 1),
		"too many": enc(4, 0, 4, 1, 1, 1, 1),
		"trailing": enc(4, 1) + "QQ",
		"short":    encodeB64([]byte{4, 1, 1}),
		"not b64":  "!!",
		"literal":  encodeB64([]byte{4, 0, 1, 0, 200}),
	} {
		if _, _, err := decodeFragment(f); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}

func encodeB64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

type fixedQuery []*nostr.Event

func (q fixedQuery) Query(context.Context, nostr.Filter) []*nostr.Event { return q }

func TestIntakeMatchesUpstream(t *testing.T) {
	u := load(t)
	raw, _ := os.ReadFile("testdata/upstream.json")
	var d struct {
		Direct struct {
			Recipient              string
			Owner, Helper, Foreign *nostr.Event
		}
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	inv, _ := ParseInvite(u.Invite.URL)
	c, err := openBundle(u.Invite.Event, inv.Token, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := FoldControl(c, u.Community.A.Wraps)
	tampered := *d.Direct.Owner
	tampered.Content = strings.Replace(tampered.Content, "A", "B", 1)
	q := fixedQuery{d.Direct.Helper, d.Direct.Foreign, &tampered, d.Direct.Owner}
	secret := strings.Repeat("22", 32)
	if got := Intake(context.Background(), q, d.Direct.Recipient, c, f); !slices.Equal(got, []string{secret}) {
		t.Fatalf("learned %v", got)
	}
	if k := c.Private[secret]; k.Epoch != 7 || hexOf(k.Key) != strings.Repeat("d4", 32) {
		t.Fatalf("key %+v", k)
	}
	// The helper holds MANAGE_MESSAGES, not MANAGE_CHANNELS: their grant of
	// general is not taken.
	if _, ok := c.Private[strings.Repeat("21", 32)]; ok {
		t.Fatal("took a key from someone without manage channels")
	}
	// Read again, nothing is new; and nothing is read for someone else.
	if got := Intake(context.Background(), q, d.Direct.Recipient, c, f); len(got) != 0 {
		t.Fatalf("learned again %v", got)
	}
	if got := Intake(context.Background(), q, strings.Repeat("51", 32), c, f); len(got) != 0 {
		t.Fatal("opened another member's invites")
	}
	if Intake(context.Background(), q, d.Direct.Recipient, c, nil) != nil || Intake(context.Background(), q, "zz", c, f) != nil {
		t.Fatal("no fold or no key")
	}
	ev, err := DMRelays([]string{"wss://a.example"}, d.Direct.Recipient)
	if err != nil || ev.Kind != KindDMRelays || Tag(tagsOf(ev), "relay") != "wss://a.example" {
		t.Fatal(ev, err)
	}
}
