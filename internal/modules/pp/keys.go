package pp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
)

// Beta keys let admins hand the beta out one member at a time. An admin
// issues a batch on /pp's help page and sees them once; skua keeps only
// their hashes. A member without the beta role who opens /pp gets a card
// with a button to redeem one, and a good key has the beta role granted
// to them.
//
// skua doesn't grant the role herself: Grant asks another bird to, merlin
// over cygnet's link (GrantMethod). The call carries three IDs and nothing
// else, and the bird granting decides for itself whether skua may ask for
// that role, so a key can never come to mean more than the beta role.

// GrantMethod is the link method that grants a role: merlin's, which
// grants only roles its admins allowed skua to ask for.
const GrantMethod = "merlin.grant"

// GrantRequest is GrantMethod's body: who gets which role where, each a
// snowflake in decimal. Nothing else rides on it.
type GrantRequest struct {
	Guild string `json:"guild"`
	User  string `json:"user"`
	Role  string `json:"role"`
}

// Granter grants user role in guild, or says why not.
type Granter func(ctx context.Context, guild, user, role snowflake.ID) error

const (
	keyForm, redeemForm = "ppkeys", "ppredeem"
	// mostKeys and longestKey bound a batch: how many keys and how many
	// days they last.
	mostKeys, longestKey = 25, 30
	// redeemTries is how many keys a member may try an hour. A key is 80
	// random bits, so this is about mistyping, not guessing.
	redeemTries = 5
)

var (
	errNoGranter = core.Tell("keys need merlin linked through cygnet to hand out the beta role, and it isn't")
	errNoBeta    = core.Tell("pick the beta testers' role first: a key hands it out")
	errKeyBatch  = core.Tell(fmt.Sprintf("say how many keys, 1 to %d, and how many days they last, 1 to %d", mostKeys, longestKey))
	errBadKey    = core.Tell("that key isn't one for this server, or it's been used or has run out")
	errTooMany   = core.Tell("that's a lot of keys tried this hour; try again in a while")
	errInAlready = core.Tell("you're in the beta already: run /pp")
	errNoRole    = core.Tell("the key is good, but the beta role didn't come through; it still works, so try again in a few minutes")
)

// keyAlphabet is base32 in lowercase, so a key reads like the rest of skua.
var keyAlphabet = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newKey is a fresh key: 80 random bits as pp- and four groups of four.
func newKey() string {
	var b [10]byte
	_, _ = rand.Read(b[:]) // crypto/rand never fails
	s := keyAlphabet.EncodeToString(b[:])
	return "pp-" + s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
}

// keyHash is what's kept of a key: the SHA-256 of it as typed, minus case,
// spaces and dashes, so a key pasted with a stray space still works.
func keyHash(key string) []byte {
	k := strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' || r == '\n' {
			return -1
		}
		return r
	}, strings.ToLower(key))
	sum := sha256.Sum256([]byte(k))
	return sum[:]
}

// tries counts each member's redeems this hour.
type tries struct {
	mu   sync.Mutex
	hour int64
	n    map[snowflake.ID]int
}

// take counts one try for user at now and reports whether it's within
// redeemTries this hour.
func (t *tries) take(user snowflake.ID, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if h := now.Unix() / 3600; h != t.hour || t.n == nil {
		t.hour, t.n = h, map[snowflake.ID]int{}
	}
	t.n[user]++
	return t.n[user] <= redeemTries
}

// keyControls is the button for /pp's help page that issues keys.
func (m *Module) keyControls() []discord.ContainerSubComponent {
	return []discord.ContainerSubComponent{
		discord.NewTextDisplay("### beta keys\n-# each key lets one member into the beta: they redeem it on /pp and get the beta testers' role. a key works once, and skua keeps only its hash, so copy them when they're shown"),
		discord.NewActionRow(withIcon(discord.NewSecondaryButton("issue keys", "pp:keys"), "envelope")),
	}
}

// keyModal asks how many keys and how long they last.
func keyModal() discord.ModalCreate {
	return discord.ModalCreate{
		CustomID: keyForm,
		Title:    "issue beta keys",
		Components: []discord.LayoutComponent{
			discord.NewLabel("how many", discord.TextInputComponent{CustomID: "count", Style: discord.TextInputStyleShort, Required: true, MaxLength: 2, Placeholder: "5"}),
			discord.NewLabel("days they last", discord.TextInputComponent{CustomID: "days", Style: discord.TextInputStyleShort, Required: true, MaxLength: 2, Placeholder: "14"}),
		},
	}
}

// openKeys opens the key form for an admin.
func (m *Module) openKeys(e *events.ComponentInteractionCreate) error {
	if m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	return e.Modal(keyModal())
}

// submitKeys issues the batch the form asks for and shows it once.
func (m *Module) submitKeys(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil || m.Admin == nil || !m.Admin(e) {
		return errNotAdmin
	}
	if m.Grant == nil {
		return errNoGranter
	}
	if m.betaRole(*guild) == 0 {
		return errNoBeta
	}
	count, err1 := strconv.Atoi(strings.TrimSpace(e.Data.Text("count")))
	days, err2 := strconv.Atoi(strings.TrimSpace(e.Data.Text("days")))
	if err1 != nil || err2 != nil || count < 1 || count > mostKeys || days < 1 || days > longestKey {
		return errKeyBatch
	}
	expires := m.now().Add(time.Duration(days) * 24 * time.Hour)
	keys := make([]string, count)
	for i := range keys {
		keys[i] = newKey()
		if _, err := m.db.Exec(ctx, `insert into pp_key (hash, guild_id, issued_by, expires) values ($1, $2, $3, $4)`,
			keyHash(keys[i]), int64(*guild), int64(e.User().ID), expires.Unix()); err != nil {
			return err
		}
	}
	m.log.Info("pp: beta keys issued", "guild", *guild, "by", e.User().ID, "count", count, "days", days)
	body := fmt.Sprintf("```\n%s\n```\n-# %d keys, good until %s utc. skua keeps only their hashes, so copy them now",
		strings.Join(keys, "\n"), count, strings.ToLower(expires.UTC().Format("2 Jan 15:04")))
	return e.CreateMessage(discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(brand.ColorOK, "pp", "beta keys", body)},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	})
}

// betaRole is guild's beta testers' role, 0 for none.
func (m *Module) betaRole(guild snowflake.ID) snowflake.ID {
	s := m.st.server(guild)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.beta
}

// closedMessage is what /pp shows a member who isn't in the beta: what it
// is, and while keys can hand the role out, the button to redeem one.
func (m *Module) closedMessage(guild snowflake.ID) (discord.MessageCreate, error) {
	if m.Grant == nil || m.betaRole(guild) == 0 {
		return discord.MessageCreate{}, errBeta
	}
	return discord.MessageCreate{
		Components: []discord.LayoutComponent{brand.Card(brand.ColorNotice, "pp", "closed beta",
			"/pp is in closed beta here for now\n-# have a key? redeem it and you're in",
			discord.NewActionRow(withIcon(discord.NewPrimaryButton("redeem a key", "pp:redeem"), "envelope")))},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	}, nil
}

// redeemModal asks for the key.
func redeemModal() discord.ModalCreate {
	return discord.ModalCreate{
		CustomID: redeemForm,
		Title:    "redeem a beta key",
		Components: []discord.LayoutComponent{
			discord.NewLabel("your key", discord.TextInputComponent{CustomID: "key", Style: discord.TextInputStyleShort, Required: true, MaxLength: 40, Placeholder: "pp-xxxx-xxxx-xxxx-xxxx"}),
		},
	}
}

// submitRedeem takes a key: it's spent first, so two members can't race
// one key in, then the role is asked for, and if that fails the key is
// handed back.
func (m *Module) submitRedeem(ctx context.Context, e *events.ModalSubmitInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return errNotServer
	}
	if m.plays(e) {
		return errInAlready
	}
	role := m.betaRole(*guild)
	if m.Grant == nil || role == 0 {
		return errBeta
	}
	user, now := e.User().ID, m.now()
	if !m.redeems.take(user, now) {
		return errTooMany
	}
	hash := keyHash(e.Data.Text("key"))
	tag, err := m.db.Exec(ctx, `update pp_key set redeemed_by = $1, redeemed_at = $2
		where hash = $3 and guild_id = $4 and redeemed_by = 0 and expires > $2`, int64(user), now.Unix(), hash, int64(*guild))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errBadKey
	}
	gctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := m.Grant(gctx, *guild, user, role); err != nil {
		if _, uerr := m.db.Exec(context.WithoutCancel(ctx), `update pp_key set redeemed_by = 0, redeemed_at = 0
			where hash = $1 and redeemed_by = $2`, hash, int64(user)); uerr != nil {
			m.log.Error("pp: handing a key back", "guild", *guild, "user", user, "err", uerr)
		}
		return fmt.Errorf("%w: %w", errNoRole, err)
	}
	m.log.Info("pp: beta key redeemed", "guild", *guild, "user", user)
	return e.CreateMessage(discord.MessageCreate{
		Components:      []discord.LayoutComponent{brand.Card(brand.ColorOK, "pp", "closed beta", "✓ you're in the beta: run /pp to begin")},
		Flags:           discord.MessageFlagEphemeral | discord.MessageFlagIsComponentsV2,
		AllowedMentions: core.NoPings(),
	})
}
