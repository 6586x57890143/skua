// Package bird is /bird, a prank and an experiment: for a few minutes every
// message one member sends is replaced by a short bird recording from
// xeno-canto, posted through skua's webhook wearing their name and avatar.
//
// One recording per prank: the member is that bird until it wears off, so
// /bird makes one API call and one download, and each replaced message costs
// a webhook post and a delete, both spent through the guard. The post goes
// up before the original comes down, so a failure leaves their message where
// it was. Like whisper, every post carries a subtext marker saying what
// happened, the species linking to the recording's xeno-canto entry, which
// carries its credit and licence.
package bird

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/filter"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
)

// query is short, top quality bird recordings.
const query = "grp:birds q:A len:3-15"

// maxAudio is under Discord's attachment limit with room to spare; a 15s
// recording is a few hundred KB.
const maxAudio = 8 << 20

// maxMinutes caps a prank at an hour.
const maxMinutes = 60

// replaceBy bounds one replacement: the post and the delete.
const replaceBy = 10 * time.Second

// screen is the slice of filter.Filter bird uses.
type screen interface {
	Check(text string) filter.Verdict
}

// poster is the slice of webhook.Poster bird uses.
type poster interface {
	Send(ctx context.Context, r rest.Rest, guild, channel, app snowflake.ID, msg discord.WebhookMessageCreate) error
}

type target struct{ guild, user snowflake.ID }

// prank is one member being one bird until a time.
type prank struct {
	until time.Time
	rec   recording
	audio []byte
}

// recording is the part of a xeno-canto API v3 recording bird reads.
type recording struct {
	ID   string `json:"id"`
	En   string `json:"en"`
	File string `json:"file"`
}

type Module struct {
	guard  *guard.Guard
	post   poster
	screen screen
	http   *http.Client
	api    string // xeno-canto's recordings endpoint
	key    string
	pages  atomic.Int64 // result pages last seen, so the next pick can be any of them
	// ponytail: in memory, so a restart lifts every prank, and a prank whose
	// member never speaks again stays until a restart. Both are fine at
	// minutes long and admin-only; a table if pranks ever need to outlive
	// the process.
	pranks sync.Map // target -> *prank
	now    func() time.Time
}

// New takes the process's one guard, the poster replacements go out
// through, the screen the member's display name passes, and a xeno-canto
// API key. Without a key /bird says so instead of running.
func New(g *guard.Guard, p poster, s screen, key string) *Module {
	return &Module{
		guard: g, post: p, screen: s, key: key, now: time.Now,
		http: &http.Client{Timeout: 10 * time.Second},
		api:  "https://xeno-canto.org/api/3/recordings",
	}
}

func (*Module) Name() string { return "bird" }

// Want is guild messages to see the member speak. Content is not needed:
// the author is enough, since what they said is never posted.
func (*Module) Want() intents.Want { return intents.Want{Required: gateway.IntentGuildMessages} }

// Perms is the webhook, plus deleting the member's message.
func (*Module) Perms() discord.Permissions {
	return discord.PermissionViewChannel | discord.PermissionManageWebhooks | discord.PermissionManageMessages
}

func (m *Module) Commands() []core.Command {
	return []core.Command{{
		Create: discord.SlashCommandCreate{
			Name:        "bird",
			Description: "turn a member into a bird for a few minutes",
			Contexts:    []discord.InteractionContextType{discord.InteractionContextTypeGuild},
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionUser{Name: "member", Description: "who", Required: true},
				discord.ApplicationCommandOptionInt{
					Name: "minutes", Description: "how long; 0 turns them back", Required: true,
					MinValue: new(0), MaxValue: new(maxMinutes),
				},
			},
		},
		Tier: core.Admin,
		Run:  m.bird,
	}}
}

var errNoBird = core.Tell("xeno-canto didn't hand over a bird; try again in a moment")

func (m *Module) bird(ctx context.Context, e *events.ApplicationCommandInteractionCreate) error {
	guild := e.GuildID()
	if guild == nil {
		return core.Tell("/bird only works in a server")
	}
	data := e.SlashCommandInteractionData()
	user := data.User("member")
	if user.Bot {
		return core.Tell("bots are already strange enough; pick a member")
	}
	k, minutes := target{*guild, user.ID}, data.Int("minutes")
	if minutes == 0 {
		if _, ok := m.pranks.LoadAndDelete(k); !ok {
			return core.Tell("@" + user.Username + " isn't a bird")
		}
		return reply(e, "✓ @"+user.Username+" is themselves again")
	}
	if m.key == "" {
		return core.Tell("skua has no xeno-canto key; set SKUA_XENO_CANTO_KEY and restart")
	}
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	rec, audio, err := m.fetch(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", errNoBird, err)
	}
	until := m.now().Add(time.Duration(minutes) * time.Minute)
	m.pranks.Store(k, &prank{until: until, rec: rec, audio: audio})
	_, err = e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), discord.MessageUpdate{
		Content:         new(fmt.Sprintf("✓ @%s is a bird for %dm: %s", user.Username, minutes, strings.ToLower(rec.En))),
		AllowedMentions: core.NoPings(),
	}, rest.WithCtx(ctx))
	return err
}

func reply(e *events.ApplicationCommandInteractionCreate, text string) error {
	return e.CreateMessage(discord.MessageCreate{Content: text, Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
}

// fetch picks a random recording from a random page of results and
// downloads it. The first pick only knows page 1 exists.
func (m *Module) fetch(ctx context.Context) (recording, []byte, error) {
	page := 1 + rand.Int64N(max(m.pages.Load(), 1))
	q := url.Values{"query": {query}, "key": {m.key}, "page": {strconv.FormatInt(page, 10)}}
	var res struct {
		NumPages   json.Number `json:"numPages"`
		Recordings []recording `json:"recordings"`
	}
	body, err := m.get(ctx, m.api+"?"+q.Encode(), 4<<20)
	if err != nil {
		return recording{}, nil, err
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return recording{}, nil, fmt.Errorf("decoding recordings: %w", err)
	}
	if n, err := res.NumPages.Int64(); err == nil && n > 0 {
		m.pages.Store(n)
	}
	// Restricted species come without a file.
	var usable []recording
	for _, r := range res.Recordings {
		if r.File != "" {
			usable = append(usable, r)
		}
	}
	if len(usable) == 0 {
		return recording{}, nil, fmt.Errorf("page %d has no downloadable recording", page)
	}
	rec := usable[rand.IntN(len(usable))]
	audio, err := m.get(ctx, rec.File, maxAudio)
	return rec, audio, err
}

func (m *Module) get(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "skua (https://skua.melting.lol)")
	res, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xeno-canto answered %s", res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = errors.New("xeno-canto sent more than expected")
	}
	return b, err
}

// OnEvent replaces a pranked member's messages. It returns at once: the
// work runs on its own goroutine, never on the gateway's.
func (m *Module) OnEvent(ev bot.Event) {
	e, ok := ev.(*events.GuildMessageCreate)
	if !ok {
		return
	}
	msg := e.Message
	if msg.Author.Bot || msg.WebhookID != nil || (msg.Type != discord.MessageTypeDefault && msg.Type != discord.MessageTypeReply) {
		return
	}
	k := target{e.GuildID, msg.Author.ID}
	v, ok := m.pranks.Load(k)
	if !ok {
		return
	}
	p := v.(*prank)
	if !m.now().Before(p.until) {
		m.pranks.CompareAndDelete(k, v)
		return
	}
	go m.replace(e.Client().Rest, e.Client().ApplicationID, e.GuildID, msg, p)
}

// replace posts the bird, then deletes the original. In a thread the
// webhook lookup fails and the message stays, as it does on any failure.
func (m *Module) replace(r rest.Rest, app, guild snowflake.ID, msg discord.Message, p *prank) {
	ctx, cancel := context.WithTimeout(context.Background(), replaceBy)
	defer cancel()
	name := msg.Author.EffectiveName()
	if msg.Member != nil && msg.Member.Nick != nil {
		name = *msg.Member.Nick
	}
	if n := strings.ToLower(name); strings.Contains(n, "discord") || strings.Contains(n, "clyde") {
		name = msg.Author.Username
	}
	post := discord.WebhookMessageCreate{
		Content:         marker(msg.Author.Username, p.until.Sub(m.now()), p.rec),
		Username:        m.screen.Check(name).Text,
		AvatarURL:       msg.Author.EffectiveAvatarURL(),
		AllowedMentions: core.NoPings(),
		Flags:           discord.MessageFlagSuppressEmbeds,
		Files:           []*discord.File{discord.NewFile("xc"+p.rec.ID+".mp3", "", bytes.NewReader(p.audio))},
	}
	if err := m.post.Send(ctx, r, guild, msg.ChannelID, app, post); err != nil {
		return
	}
	if m.guard.Allow(guild, guard.MessageDelete) != nil {
		return
	}
	err := r.DeleteMessage(msg.ChannelID, msg.ID, rest.WithCtx(ctx))
	m.guard.Report(guild, struggling(err))
}

// markdown escapes brackets too, so a name cannot close the species link.
var markdown = strings.NewReplacer(`\`, `\\`, `_`, `\_`, `*`, `\*`, `~`, `\~`, "`", "\\`", `|`, `\|`, `>`, `\>`, `[`, `\[`, `]`, `\]`)

// marker is the whole post: who is which bird for how long, the species
// linking to its xeno-canto entry, which carries the credit and licence.
// Everything is escaped and lowercase, the link held from unfurling.
func marker(username string, left time.Duration, rec recording) string {
	mins := max(int(left.Round(time.Minute)/time.Minute), 1)
	species := strings.ToLower(rec.En)
	// "eu", "ura" and "uni" sound like "you": a eurasian wren, a ural owl.
	article := "a"
	if species != "" && strings.ContainsRune("aeiou", rune(species[0])) &&
		!strings.HasPrefix(species, "eu") && !strings.HasPrefix(species, "ura") && !strings.HasPrefix(species, "uni") {
		article = "an"
	}
	return fmt.Sprintf("-# @%s is %s [%s](<https://xeno-canto.org/%s>) for %dm",
		markdown.Replace(username), article, markdown.Replace(species), rec.ID, mins)
}

// struggling is true only for answers that say Discord is, not the request.
func struggling(err error) bool {
	re, ok := errors.AsType[*rest.Error](err)
	if !ok || re.Response == nil {
		return false
	}
	s := re.Response.StatusCode
	return s == 429 || s >= 500
}
