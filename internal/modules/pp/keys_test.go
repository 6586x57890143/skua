package pp

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/6586x57890143/skua/internal/core/coretest"
)

// keyDB keeps pp_key in memory and answers the three statements keys.go
// sends it.
type keyDB struct {
	fakeDB
	keys []*keyRow
}

type keyRow struct {
	hash             []byte
	guild, by        int64
	expires          int64
	redeemed, onDate int64
}

func (k *keyDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.HasPrefix(sql, "insert into pp_key"):
		k.keys = append(k.keys, &keyRow{hash: args[0].([]byte), guild: args[1].(int64), by: args[2].(int64), expires: args[3].(int64)})
	case strings.Contains(sql, "set redeemed_by = $1"):
		user, now, hash, guild := args[0].(int64), args[1].(int64), args[2].([]byte), args[3].(int64)
		for _, r := range k.keys {
			if bytes.Equal(r.hash, hash) && r.guild == guild && r.redeemed == 0 && r.expires > now {
				r.redeemed, r.onDate = user, now
				return pgconn.NewCommandTag("UPDATE 1"), nil
			}
		}
		return pgconn.NewCommandTag("UPDATE 0"), nil
	case strings.Contains(sql, "set redeemed_by = 0"):
		for _, r := range k.keys {
			if bytes.Equal(r.hash, args[0].([]byte)) && r.redeemed == args[1].(int64) {
				r.redeemed, r.onDate = 0, 0
			}
		}
	}
	return k.fakeDB.Exec(ctx, sql, args...)
}

var keyShape = regexp.MustCompile(`^pp-[a-z2-7]{4}(-[a-z2-7]{4}){3}$`)

// A key is 80 random bits in four groups, and its hash forgives case,
// spaces and dashes.
func TestKeyShape(t *testing.T) {
	a, b := newKey(), newKey()
	if !keyShape.MatchString(a) || a == b {
		t.Fatalf("keys %q and %q", a, b)
	}
	if !bytes.Equal(keyHash(a), keyHash(" "+strings.ToUpper(strings.ReplaceAll(a, "-", " "))+"\n")) {
		t.Error("a key typed loosely hashes differently")
	}
	if bytes.Equal(keyHash(a), keyHash(b)) {
		t.Error("two keys hash the same")
	}
}

// keyForm submitted by user in guild 3 with these answers.
func keyFormFor(t *testing.T, form string, user snowflake.ID, values map[string]string) (*events.ModalSubmitInteractionCreate, *[]discord.MessageCreate) {
	t.Helper()
	return coretest.Modal(t, form, values, func(p map[string]any) {
		p["member"].(map[string]any)["user"] = map[string]any{"id": user.String(), "username": "member"}
	})
}

type redeemRest struct {
	rest.Rest
	updates []discord.MessageUpdate
}

func (r *redeemRest) UpdateInteractionResponse(_ snowflake.ID, _ string, update discord.MessageUpdate, _ ...rest.RequestOpt) (*discord.Message, error) {
	r.updates = append(r.updates, update)
	return &discord.Message{}, nil
}

// Admins issue keys on the help page, only with a beta role and a bird to
// grant it, in bounded batches, shown once and kept as hashes.
func TestIssueKeys(t *testing.T) {
	db := &keyDB{}
	m := testModule(db)
	admin := true
	m.Admin = func(discord.Interaction) bool { return admin }
	ctx := context.Background()
	issue := func(count, days string) (string, error) {
		e, sent := keyFormFor(t, keyForm, 5, map[string]string{"count": count, "days": days})
		err := m.submitKeys(ctx, e)
		if err != nil {
			return "", err
		}
		return js(*sent), nil
	}
	if _, err := issue("3", "14"); !errors.Is(err, errNoGranter) {
		t.Errorf("keys without a bird to grant: %v", err)
	}
	m.Grant = func(context.Context, snowflake.ID, snowflake.ID, snowflake.ID) error { return nil }
	if _, err := issue("3", "14"); !errors.Is(err, errNoBeta) {
		t.Errorf("keys without a beta role: %v", err)
	}
	m.st.server(3).beta = 77
	for _, bad := range [][2]string{{"0", "14"}, {"26", "14"}, {"3", "0"}, {"3", "31"}, {"x", "14"}} {
		if _, err := issue(bad[0], bad[1]); !errors.Is(err, errKeyBatch) {
			t.Errorf("a batch of %v: %v", bad, err)
		}
	}
	got, err := issue("3", "14")
	if err != nil || len(db.keys) != 3 {
		t.Fatalf("issuing: %v, %d kept", err, len(db.keys))
	}
	shown := regexp.MustCompile(`pp-[a-z2-7]{4}(-[a-z2-7]{4}){3}`).FindAllString(got, -1)
	if len(shown) != 3 || !bytes.Equal(db.keys[0].hash, keyHash(shown[0])) || strings.Contains(got, string(db.keys[0].hash)) {
		t.Errorf("shown %q", shown)
	}
	if db.keys[0].guild != 3 || db.keys[0].expires != t0.Add(14*24*time.Hour).Unix() {
		t.Errorf("kept %+v", db.keys[0])
	}
	admin = false
	if _, err := issue("3", "14"); !errors.Is(err, errNotAdmin) {
		t.Errorf("a member issued keys: %v", err)
	}
	e, _ := coretest.Button(t, "pp:keys", nil)
	if err := m.press(ctx, e); !errors.Is(err, errNotAdmin) {
		t.Errorf("a member opened the key form: %v", err)
	}
}

// A member outside the beta gets the redeem card; a good key spends once
// and has the beta role granted; a bad, used, expired or foreign one is
// refused; a failed grant hands the key back; and tries are capped.
func TestRedeemKeys(t *testing.T) {
	db := &keyDB{}
	m := testModule(db)
	m.Admin = func(discord.Interaction) bool { return false }
	ctx := context.Background()

	// Without a bird to grant, /pp just says it's closed.
	e, _ := coretest.Event(t, "pp", nil)
	if err := m.Commands()[0].Run(ctx, e); !errors.Is(err, errBeta) {
		t.Fatalf("closed without keys: %v", err)
	}

	var granted []snowflake.ID
	var fail error
	m.Grant = func(_ context.Context, guild, user, role snowflake.ID) error {
		if fail != nil {
			return fail
		}
		granted = append(granted, guild, user, role)
		return nil
	}
	m.st.server(3).beta = 77
	e, sent := coretest.Event(t, "pp", nil)
	if err := m.Commands()[0].Run(ctx, e); err != nil || !strings.Contains(js(*sent), "pp:redeem") {
		t.Fatalf("the closed card: %v %s", err, js(*sent))
	}

	key, used, old, other := newKey(), newKey(), newKey(), newKey()
	db.keys = []*keyRow{
		{hash: keyHash(key), guild: 3, expires: t0.Add(time.Hour).Unix()},
		{hash: keyHash(used), guild: 3, expires: t0.Add(time.Hour).Unix(), redeemed: 9},
		{hash: keyHash(old), guild: 3, expires: t0.Add(-time.Hour).Unix()},
		{hash: keyHash(other), guild: 4, expires: t0.Add(time.Hour).Unix()},
	}
	redeem := func(user snowflake.ID, k string) (string, error) {
		e, sent := keyFormFor(t, redeemForm, user, map[string]string{"key": k})
		r := &redeemRest{}
		e.Client().Rest = r
		err := m.submitRedeem(ctx, e)
		return js(*sent) + js(r.updates), err
	}
	for _, bad := range []string{used, old, other, "pp-nope"} {
		if _, err := redeem(10, bad); !errors.Is(err, errBadKey) {
			t.Errorf("redeeming %q: %v", bad, err)
		}
	}

	fail = errors.New("merlin is away")
	if _, err := redeem(11, key); !errors.Is(err, errNoRole) || db.keys[0].redeemed != 0 {
		t.Fatalf("a failed grant: %v, key spent by %d", err, db.keys[0].redeemed)
	}
	fail = nil
	got, err := redeem(11, strings.ToUpper(key))
	if err != nil || !strings.Contains(got, "you're in the beta") || db.keys[0].redeemed != 11 {
		t.Fatalf("redeeming: %v %s", err, got)
	}
	if len(granted) != 3 || granted[0] != 3 || granted[1] != 11 || granted[2] != 77 {
		t.Errorf("granted %v", granted)
	}
	if _, err := redeem(12, key); !errors.Is(err, errBadKey) {
		t.Errorf("a key spent twice: %v", err)
	}

	// Member 10 has tried four; a fifth is heard and a sixth this hour is
	// refused, good key or not.
	if _, err := redeem(10, "pp-nope"); !errors.Is(err, errBadKey) {
		t.Errorf("a fifth try: %v", err)
	}
	if _, err := redeem(10, key); !errors.Is(err, errTooMany) {
		t.Errorf("a sixth try: %v", err)
	}
	m.now = func() time.Time { return t0.Add(time.Hour) }
	if _, err := redeem(10, "pp-nope"); !errors.Is(err, errBadKey) {
		t.Errorf("the next hour's try: %v", err)
	}

	m.Admin = func(discord.Interaction) bool { return true }
	if _, err := redeem(13, key); !errors.Is(err, errInAlready) {
		t.Errorf("a player redeeming: %v", err)
	}
}

func TestRedeemAcknowledgesBeforeGrantAndDetachesDeadline(t *testing.T) {
	db := &keyDB{}
	m := testModule(db)
	m.Admin = func(discord.Interaction) bool { return false }
	m.st.server(3).beta = 77
	key := newKey()
	db.keys = []*keyRow{{hash: keyHash(key), guild: 3, expires: t0.Add(time.Hour).Unix()}}
	e, _ := keyFormFor(t, redeemForm, 5, map[string]string{"key": key})
	r := &redeemRest{}
	e.Client().Rest = r
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ack := false
	e.Respond = func(kind discord.InteractionResponseType, data discord.InteractionResponseData, _ ...rest.RequestOpt) error {
		ack = kind == discord.InteractionResponseTypeDeferredCreateMessage && data.(discord.MessageCreate).Flags.Has(discord.MessageFlagEphemeral)
		cancel() // Discord's initial-response window ends after the defer.
		return nil
	}
	m.Grant = func(ctx context.Context, _, _, _ snowflake.ID) error {
		if !ack || ctx.Err() != nil {
			t.Fatal("grant ran before defer or under the expired initial deadline")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 9*time.Second {
			t.Fatal("grant did not get its own bounded deadline")
		}
		return nil
	}
	if err := m.submitRedeem(ctx, e); err != nil {
		t.Fatal(err)
	}
	if len(r.updates) != 1 || !strings.Contains(js(r.updates), "you're in the beta") {
		t.Fatal("deferred response wasn't completed")
	}
}

func TestRedeemFailedDeferDoesNotSpendKey(t *testing.T) {
	db := &keyDB{}
	m := testModule(db)
	m.Admin = func(discord.Interaction) bool { return false }
	m.st.server(3).beta = 77
	key := newKey()
	db.keys = []*keyRow{{hash: keyHash(key), guild: 3, expires: t0.Add(time.Hour).Unix()}}
	e, _ := keyFormFor(t, redeemForm, 5, map[string]string{"key": key})
	fail := errors.New("interaction expired")
	e.Respond = func(discord.InteractionResponseType, discord.InteractionResponseData, ...rest.RequestOpt) error {
		return fail
	}
	m.Grant = func(context.Context, snowflake.ID, snowflake.ID, snowflake.ID) error {
		t.Fatal("grant after failed defer")
		return nil
	}
	if err := m.submitRedeem(t.Context(), e); !errors.Is(err, fail) {
		t.Fatal(err)
	}
	if db.keys[0].redeemed != 0 {
		t.Fatal("failed defer consumed key")
	}
}
