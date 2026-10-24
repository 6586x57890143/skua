package pp

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// deskRest is a server's roles, an interaction's edits and the heralds'
// channel.
type deskRest struct {
	yardRest
	posts []string
}

func (f *deskRest) CreateMessage(_ snowflake.ID, c discord.MessageCreate, _ ...rest.RequestOpt) (*discord.Message, error) {
	f.posts = append(f.posts, js(c))
	return &discord.Message{ID: snowflake.ID(1000 + len(f.posts))}, nil
}

// desk is a server on a real database where member 5 sits on rung r with
// purse pp and a petition drafted, and the heralds sit in channel 77.
func desk(t *testing.T, r, purse int64) (*Module, *deskRest) {
	t.Helper()
	db := testDB(t)
	for _, table := range []string{"pp_petition", "pp_ledger", "pp_member", "pp_guild"} {
		if _, err := db.Exec(context.Background(), `delete from `+table+` where guild_id = 3`); err != nil {
			t.Fatal(err)
		}
	}
	m, roles := liveryServer()
	m.db = db
	d := &deskRest{yardRest: yardRest{roleRest: *roles}}
	m.rest = d
	s := m.st.server(3)
	s.heralds, s.yardOpen = 77, true
	mem := m.st.member(s, 3, 5, DayOf(m.now()))
	mem.rung, mem.purse = r, purse*unit
	// The purse opens the member's ledger, so the audit agrees with it.
	s.append(5, DayOf(m.now()), reasonOpen, purse*unit, nil)
	s.drafts = map[snowflake.ID]*draft{5: {name: "the lamplighter", style: ownStyles[1], colors: []uint32{0xc66e34, 0x6286c3}}}
	return m, d
}

// press is a member pressing id, its replies going to d; perms are the
// member's permissions and who their ID.
func (d *deskRest) press(t *testing.T, m *Module, id, who string, perms discord.Permissions) error {
	e, _ := coretest.Button(t, id, func(p map[string]any) {
		mem := p["member"].(map[string]any)
		mem["permissions"] = strconv.FormatInt(int64(perms), 10)
		mem["user"].(map[string]any)["id"] = who
	})
	e.Client().Rest = d
	return m.press(context.Background(), e)
}

// A petition sent holds the fee and a week, waits with the heralds, and a
// moderator's grant makes the role, dresses the member and spends the
// hold. Nobody grants their own, a member without Manage Roles grants
// nothing, and a petition is granted once.
func TestPetitionGranted(t *testing.T) {
	m, d := desk(t, 4, 10000)
	// Member 5 plays as an admin; the heralds are 6 and 7.
	m.Admin = func(i discord.Interaction) bool { return i.User().ID == 5 }
	s := m.st.server(3)
	mem := s.members[5]
	before, burned := mem.purse, s.seasonBurn
	if err := d.press(t, m, "pp:ownsend", "5", 0); err != nil || len(d.followups) != 0 {
		t.Fatalf("sending: %v %v", err, d.followups)
	}
	fee, week := m.st.pot()*heraldsFee/100, m.st.pot()*ownWeek(ownStyles[1], false)/100
	if mem.purse != before-fee-week || s.asked[5] == 0 || len(d.posts) != 1 {
		t.Fatalf("after sending: purse %d of %d, asked %d, posts %d", mem.purse, before, s.asked[5], len(d.posts))
	}
	if !strings.Contains(d.posts[0], "the lamplighter") || !strings.Contains(d.posts[0], "#c66e34") || !strings.Contains(d.posts[0], "pp:herald:yes:") {
		t.Errorf("the heralds' post: %s", d.posts[0])
	}
	if err := d.press(t, m, "pp:ownsend", "5", 0); err != nil || d.followups[0] != errOwnAsked.Error() {
		t.Errorf("a second petition: %v %v", err, d.followups)
	}
	id := s.asked[5]
	yes := "pp:herald:yes:" + itoa(id)

	if err := d.press(t, m, yes, "6", 0); !errors.Is(err, errHeraldWho) {
		t.Errorf("a member without manage roles: %v", err)
	}
	d.followups = nil
	if err := d.press(t, m, yes, "5", discord.PermissionManageRoles); err != nil || len(d.followups) != 1 || d.followups[0] != errHeraldSelf.Error() {
		t.Errorf("their own petition: %v %v", err, d.followups)
	}
	if err := d.press(t, m, yes, "6", discord.PermissionManageRoles); err != nil {
		t.Fatal(err)
	}
	if mem.livery != ownKey || mem.liveryUntil != DayOf(m.now())+leaseDays || s.own[5].role != 99 || s.asked[5] != 0 || s.drafts[5] != nil {
		t.Fatalf("after the grant: %q until %d, own %+v, asked %d", mem.livery, mem.liveryUntil, s.own[5], s.asked[5])
	}
	if s.seasonBurn-burned != fee+week || mem.purse != before-fee-week {
		t.Errorf("the hold wasn't spent: burned %d, purse %d", s.seasonBurn-burned, mem.purse)
	}
	if c := d.created; c.Name != "the lamplighter" || c.Colors.PrimaryColor != 0xc66e34 || c.Colors.SecondaryColor == nil || *c.Permissions != 0 {
		t.Errorf("the role made: %+v", c)
	}
	if last := d.edits[len(d.edits)-1]; !strings.Contains(last, "granted by") || strings.Contains(last, "pp:herald:yes") {
		t.Errorf("the post after the grant: %s", last)
	}
	d.followups = nil
	if err := d.press(t, m, yes, "7", discord.PermissionManageRoles); err != nil || d.followups[0] != errHeraldGone.Error() {
		t.Errorf("a second grant: %v %v", err, d.followups)
	}

	// The yard's corner and the petition screen show the livery worn, and
	// another week runs on from the last.
	got, _ := m.ownPage(context.Background(), 3, 5)
	if !strings.Contains(js(got), "pp:ownweek") || !strings.Contains(js(got), "change the petition") {
		t.Errorf("the petition screen when worn: %s", js(got))
	}
	if err := d.press(t, m, "pp:ownweek", "5", 0); err != nil || mem.liveryUntil != DayOf(m.now())+2*leaseDays {
		t.Errorf("another week: %v, until %d", err, mem.liveryUntil)
	}

	// A boot finds the livery worn and nothing with the heralds.
	back := newState(&Default, 42)
	if err := back.loadPetitions(context.Background(), m.db); err != nil {
		t.Fatal(err)
	}
	if b := back.server(3); b.own[5].role != 99 || b.own[5].week != ownWeek(ownStyles[1], false) || len(b.asked) != 0 {
		t.Errorf("loaded: %+v asked %v", b.own[5], b.asked)
	}
}

// A change to a livery worn holds only the fee and, granted, changes the
// role rather than making another.
func TestPetitionChange(t *testing.T) {
	m, d := desk(t, 4, 10000)
	s := m.st.server(3)
	mem := s.members[5]
	if err := d.press(t, m, "pp:ownsend", "5", 0); err != nil {
		t.Fatal(err)
	}
	if err := d.press(t, m, "pp:herald:yes:"+itoa(s.asked[5]), "6", discord.PermissionManageRoles); err != nil {
		t.Fatal(err)
	}
	until, before := mem.liveryUntil, mem.purse
	s.drafts[5] = &draft{name: "the lamplighter's lamp", style: ownStyles[0], colors: []uint32{0xa2803e}}
	if err := d.press(t, m, "pp:ownsend", "5", 0); err != nil {
		t.Fatal(err)
	}
	if mem.purse != before-m.st.pot()*heraldsFee/100 {
		t.Errorf("a change held %d", before-mem.purse)
	}
	d.writes = nil
	if err := d.press(t, m, "pp:herald:yes:"+itoa(s.asked[5]), "6", discord.PermissionManageRoles); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.Join(d.writes, ","), "edit 99") || mem.liveryUntil != until || s.own[5].week != ownWeek(ownStyles[0], false) {
		t.Errorf("the change: writes %v, until %d of %d, week %d", d.writes, mem.liveryUntil, until, s.own[5].week)
	}
	var granted int64
	if err := queryOne(context.Background(), m.db, `select count(*) from pp_petition where guild_id = 3 and state = 'granted'`, nil, &granted); err != nil || granted != 1 {
		t.Errorf("%d petitions granted at once: %v", granted, err)
	}
}

// A refusal gives the hold back with the heralds' reason, which the member
// reads on their petition.
func TestPetitionRefused(t *testing.T) {
	m, d := desk(t, 4, 10000)
	s := m.st.server(3)
	mem := s.members[5]
	before := mem.purse
	if err := d.press(t, m, "pp:ownsend", "5", 0); err != nil {
		t.Fatal(err)
	}
	id := itoa(s.asked[5])
	e, _ := coretest.Button(t, "pp:herald:no:"+id, func(p map[string]any) {
		p["member"].(map[string]any)["permissions"] = strconv.FormatInt(int64(discord.PermissionManageRoles), 10)
		p["member"].(map[string]any)["user"].(map[string]any)["id"] = "6"
	})
	modal := false
	e.Respond = func(kind discord.InteractionResponseType, _ discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		modal = kind == discord.InteractionResponseTypeModal
		return nil
	}
	if err := m.press(context.Background(), e); err != nil || !modal {
		t.Fatalf("the refusal form: %v", err)
	}
	f, _ := coretest.Modal(t, heraldForm+":"+id, map[string]string{"reason": "too close to a family's name"}, func(p map[string]any) {
		p["member"].(map[string]any)["permissions"] = strconv.FormatInt(int64(discord.PermissionManageRoles), 10)
		p["member"].(map[string]any)["user"].(map[string]any)["id"] = "6"
	})
	var updated []string
	f.Respond = func(_ discord.InteractionResponseType, data discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		updated = append(updated, js(data))
		return nil
	}
	if err := m.refusePetition(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if mem.purse != before || s.asked[5] != 0 || len(updated) != 1 || !strings.Contains(updated[0], "refused by") {
		t.Fatalf("after the refusal: purse %d of %d, asked %d, %v", mem.purse, before, s.asked[5], updated)
	}
	if got := m.letter(context.Background(), 3, 5); got != "refused: too close to a family's name" {
		t.Errorf("the letter: %q", got)
	}
	if err := m.refusePetition(context.Background(), f); !errors.Is(err, errHeraldGone) {
		t.Errorf("a second refusal: %v", err)
	}
}

// A name another member's petition holds is taken; with no heralds' desk
// nothing is sent; and a livery of one's own that fades takes its role
// with it.
func TestPetitionEdges(t *testing.T) {
	m, d := desk(t, 4, 10000)
	s := m.st.server(3)
	if _, err := m.db.Exec(context.Background(), `insert into pp_petition (guild_id, user_id, name, style, state, held, asked)
		values (3, 8, 'The Lamplighter', 'plain', 'asked', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := d.press(t, m, "pp:ownsend", "5", 0); err != nil || d.followups[0] != errOwnTaken.Error() {
		t.Errorf("a name another holds: %v %v", err, d.followups)
	}
	s.heralds = 0
	s.drafts[5].name = "another"
	if err := d.press(t, m, "pp:ownsend", "5", 0); err != nil || d.followups[1] != errNoHeralds.Error() {
		t.Errorf("no desk: %v %v", err, d.followups)
	}
	if s.members[5].purse != 10000*unit {
		t.Error("a petition not sent held something")
	}

	// Fading.
	s.heralds = 77
	if err := d.press(t, m, "pp:ownsend", "5", 0); err != nil {
		t.Fatal(err)
	}
	if err := d.press(t, m, "pp:herald:yes:"+itoa(s.asked[5]), "6", discord.PermissionManageRoles); err != nil {
		t.Fatal(err)
	}
	d.writes = nil
	m.fade(context.Background(), s.members[5].liveryUntil+fadeDays)
	if s.members[5].livery != "" || s.own[5].role != 0 || strings.Join(d.writes, ",") != "remove 99,delete 99" {
		t.Errorf("after fading: %q %+v writes %v", s.members[5].livery, s.own[5], d.writes)
	}
	var lapsed int64
	if err := queryOne(context.Background(), m.db, `select count(*) from pp_petition where guild_id = 3 and state = 'lapsed'`, nil, &lapsed); err != nil || lapsed != 1 {
		t.Errorf("lapsed %d: %v", lapsed, err)
	}

	// The settings list what waits, and post it again.
	s.drafts[5] = &draft{name: "the lamp again", style: ownStyles[0], colors: []uint32{0xa2803e}}
	if err := d.press(t, m, "pp:ownsend", "5", 0); err != nil {
		t.Fatal(err)
	}
	if got := js(m.liverySettings(3)); !strings.Contains(got, "pp:herald:repost") {
		t.Errorf("the settings don't list the waiting petition: %s", got)
	}
	m.Admin = func(discord.Interaction) bool { return true }
	posts := len(d.posts)
	if err := d.press(t, m, "pp:herald:repost", "6", discord.PermissionManageRoles); err != nil || len(d.posts) != posts+2 {
		t.Errorf("reposting: %v, %d posts", err, len(d.posts)-posts)
	}
}

func itoa(n int64) string { return snowflake.ID(n).String() }

// From review: swapping between one's own livery and the yard's takes the
// other off (and one's own goes for good); a change granted after the
// livery faded goes back; a grant a crash cut short waits again and reuses
// the role it made; and a livery only worn offers nothing to send.
func TestPetitionReviewed(t *testing.T) {
	ctx := context.Background()
	m, d := desk(t, 4, 10000)
	s := m.st.server(3)
	mem := s.members[5]
	s.setDye("hide", 30)

	// A trade livery, then one's own granted over it: the trade's role
	// comes off.
	hide, _ := dyeOf("hide")
	if _, err := m.leased(ctx, 3, 5, hide); err != nil {
		t.Fatal(err)
	}
	if err := d.press(t, m, "pp:ownsend", "5", 0); err != nil {
		t.Fatal(err)
	}
	d.writes = nil
	if err := d.press(t, m, "pp:herald:yes:"+itoa(s.asked[5]), "6", discord.PermissionManageRoles); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(d.writes, ","); !strings.Contains(got, "add 99") || !strings.Contains(got, "remove 30") || mem.livery != ownKey {
		t.Fatalf("one's own over a trade's: %s, %q", got, mem.livery)
	}
	if got, _ := m.ownPage(ctx, 3, 5); strings.Contains(js(got), "pp:ownsend") {
		t.Error("a livery only worn offers to be sent")
	}

	// Back to the trade's: one's own comes off and is deleted.
	d.writes = nil
	if _, err := m.leased(ctx, 3, 5, hide); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(d.writes, ","); got != "add 30,remove 99,delete 99" || s.own[5].role != 0 || mem.livery != "hide" {
		t.Fatalf("a trade's over one's own: %s, own %+v", got, s.own[5])
	}

	// A change sent while worn, granted after it faded: the fee goes back.
	m2, d2 := desk(t, 4, 10000)
	s2 := m2.st.server(3)
	if err := d2.press(t, m2, "pp:ownsend", "5", 0); err != nil {
		t.Fatal(err)
	}
	if err := d2.press(t, m2, "pp:herald:yes:"+itoa(s2.asked[5]), "6", discord.PermissionManageRoles); err != nil {
		t.Fatal(err)
	}
	s2.drafts[5] = &draft{name: "a change", style: ownStyles[0], colors: []uint32{0xa2803e}}
	if err := d2.press(t, m2, "pp:ownsend", "5", 0); err != nil {
		t.Fatal(err)
	}
	change := s2.asked[5]
	before := s2.members[5].purse
	m2.fade(ctx, s2.members[5].liveryUntil+fadeDays)
	if err := d2.press(t, m2, "pp:herald:yes:"+itoa(change), "6", discord.PermissionManageRoles); err != nil {
		t.Fatal(err)
	}
	if mem2 := s2.members[5]; mem2.livery != "" || mem2.purse != before+m2.st.pot()*heraldsFee/100 || s2.asked[5] != 0 {
		t.Errorf("a change granted after a fade: %q, purse %d of %d", mem2.livery, mem2.purse, before)
	}

	// A grant a crash cut short.
	m3, d3 := desk(t, 4, 10000)
	if _, err := m3.db.Exec(ctx, `insert into pp_petition (guild_id, user_id, name, style, colors, state, held, asked, role_id)
		values (3, 5, 'cut short', 'plain', '{10649406}', 'granting', 1, 1, 30)`); err != nil {
		t.Fatal(err)
	}
	if err := m3.st.loadPetitions(ctx, m3.db); err != nil {
		t.Fatal(err)
	}
	id := m3.st.server(3).asked[5]
	if id == 0 {
		t.Fatal("a cut short grant doesn't wait again")
	}
	d3.writes = nil
	if err := d3.press(t, m3, "pp:herald:yes:"+itoa(id), "6", discord.PermissionManageRoles); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(d3.writes, ","); !strings.HasPrefix(got, "edit 30,add 30") {
		t.Errorf("the role made before wasn't reused: %s", got)
	}
}
