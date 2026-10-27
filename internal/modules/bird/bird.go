// Package bird is /bird, a prank and an experiment: for a few minutes every
// message one member sends is replaced by a short bird recording from
// xeno-canto, posted through skua's webhook wearing their name and avatar.
//
// One recording per prank: the member is that bird until it wears off, so
// /bird makes one API call and one download. The first message gets the bird;
// later messages are deleted, keeping one recording up. The post goes up
// before the original comes down, so a failure leaves their message where
// it was. Like whisper, every post carries a subtext marker saying what
// happened, the species linking to the recording's xeno-canto entry, which
// carries its credit and licence. The species' photo and a line about it
// come with it when two databases agree on the bird (about.go).
//
// The spam module turns members into birds too, through Turn.
package bird

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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

	"github.com/6586x57890143/skua/internal/brand"
	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/filter"
	"github.com/6586x57890143/skua/internal/guard"
	"github.com/6586x57890143/skua/internal/intents"
)

// query is short, top quality bird recordings.
const query = "grp:birds q:A len:3-15"

const retryBirdAfter = 30 * time.Second

// pendingBirdLimit bounds originals waiting for the first successful post.
const pendingBirdLimit = 100000

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
	Send(ctx context.Context, r rest.Rest, guild, channel, app snowflake.ID, msg discord.WebhookMessageCreate) (*discord.Message, error)
	Delete(ctx context.Context, r rest.Rest, guild, channel, app, webhookID, message snowflake.ID) error
}

type target struct{ guild, user snowflake.ID }

type original struct{ channel, id snowflake.ID }

// prank is one member being one bird until a time.
type prank struct {
	by    string // who started it, for the audit log
	rec   recording
	audio []byte
	about about

	mu      sync.Mutex
	until   time.Time // moved only by extend once stored
	posting bool
	posted  bool
	retryAt time.Time
	pending []original
	queued  map[snowflake.ID]struct{}
	overflow bool
}

func (p *prank) end() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.until
}

// extend keeps a running prank going until at least to. It reports false
// for one already over at now.
func (p *prank) extend(now, to time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !now.Before(p.until) {
		return false
	}
	if to.After(p.until) {
		p.until = to
	}
	return true
}

// recording is the part of a xeno-canto API v3 recording bird reads.
type recording struct {
	ID   string `json:"id"`
	En   string `json:"en"`
	Gen  string `json:"gen"`
	Sp   string `json:"sp"`
	File string `json:"file"`
}

// species is the scientific name, the one key the photo and the line are
// matched on.
func (r recording) species() string { return strings.TrimSpace(r.Gen + " " + r.Sp) }

type Module struct {
	guard  *guard.Guard
	post   poster
	screen screen
	http   *http.Client
	api    string // xeno-canto's recordings endpoint
	inat   string // iNaturalist's taxa endpoint
	wiki   string // Wikipedia's page summary endpoint, ending in a slash
	photos string // the only prefix an iNaturalist photo is fetched from
	// commons is Wikimedia Commons' API, and wikimedia the only prefixes a
	// Commons file is taken from: its originals and its thumbnails.
	commons   string
	wikimedia []string
	key       string
	// turning is the members a fetch is under way for, so two trips at
	// once fetch once.
	turning sync.Map     // target -> struct{}
	pages   atomic.Int64 // result pages last seen, so the next pick can be any of them
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
		http:    &http.Client{Timeout: 10 * time.Second},
		api:     "https://xeno-canto.org/api/3/recordings",
		inat:    "https://api.inaturalist.org/v1/taxa",
		wiki:    "https://en.wikipedia.org/api/rest_v1/page/summary/",
		photos:  "https://inaturalist-open-data.s3.amazonaws.com/",
		commons: "https://commons.wikimedia.org/w/api.php",
		wikimedia: []string{
			"https://upload.wikimedia.org/wikipedia/commons/",
			"https://thumb.wikimedia.org/wikipedia/commons/",
		},
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

// Help is bird's page in /help.
func (*Module) Help() core.Help {
	return core.Help{
		Color: brand.ColorOK,
		Line:  "turns a member into a bird for a while",
		About: "an admin runs /bird on a member for up to 60 minutes. the first message they send is swapped for a real bird call from xeno-canto, posted under their name with the species linked to its recording. later messages are removed while one recording stays up. an openly licensed photo of it comes along from inaturalist or else its wikipedia article. so does a line from wikipedia, both only when the two agree on which bird it is. run it again with 0 minutes to turn them back early",
	}
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

var (
	errNoBird = core.Tell("xeno-canto didn't hand over a bird; try again in a moment")
	errNoKey  = core.Tell("skua has no xeno-canto key; set SKUA_XENO_CANTO_KEY and restart")
	errBusy   = core.Tell("a bird is already on its way for them")
)

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
		return errNoKey
	}
	if err := e.DeferCreateMessage(true); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	species, err := m.Turn(ctx, k.guild, k.user, e.User().ID.String(), time.Duration(minutes)*time.Minute)
	if err != nil {
		return err
	}
	_, err = e.Client().Rest.UpdateInteractionResponse(e.ApplicationID(), e.Token(), discord.MessageUpdate{
		Content:         new(fmt.Sprintf("✓ @%s is a bird for %dm: %s", user.Username, minutes, species)),
		AllowedMentions: core.NoPings(),
	}, rest.WithCtx(ctx))
	return err
}

// Turn makes user a bird for d, started by by, and says which bird. A
// member who already is one stays that bird, for at least d from now, with
// nothing fetched; otherwise it fetches a recording with its photo and
// line, inside ctx. Any length goes: /bird caps its own at an hour.
func (m *Module) Turn(ctx context.Context, guild, user snowflake.ID, by string, d time.Duration) (string, error) {
	k := target{guild, user}
	now := m.now()
	if v, ok := m.pranks.Load(k); ok && v.(*prank).extend(now, now.Add(d)) {
		return strings.ToLower(v.(*prank).rec.En), nil
	}
	if m.key == "" {
		return "", errNoKey
	}
	if _, busy := m.turning.LoadOrStore(k, struct{}{}); busy {
		return "", errBusy
	}
	defer m.turning.Delete(k)
	rec, audio, ab, err := m.fetch(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errNoBird, err)
	}
	m.pranks.Store(k, &prank{by: by, until: m.now().Add(d), rec: rec, audio: audio, about: ab})
	return strings.ToLower(rec.En), nil
}

// Bird is whether user is a bird right now.
func (m *Module) Bird(guild, user snowflake.ID) bool {
	v, ok := m.pranks.Load(target{guild, user})
	return ok && m.now().Before(v.(*prank).end())
}

func reply(e *events.ApplicationCommandInteractionCreate, text string) error {
	return e.CreateMessage(discord.MessageCreate{Content: text, Flags: discord.MessageFlagEphemeral, AllowedMentions: core.NoPings()})
}

// fetch picks a random recording from a random page of results and
// downloads it, looking up its photo and line meanwhile. The first pick
// only knows page 1 exists. A missing photo or line is no failure.
func (m *Module) fetch(ctx context.Context) (recording, []byte, about, error) {
	page := 1 + rand.Int64N(max(m.pages.Load(), 1))
	q := url.Values{"query": {query}, "key": {m.key}, "page": {strconv.FormatInt(page, 10)}}
	var res struct {
		NumPages   json.Number `json:"numPages"`
		Recordings []recording `json:"recordings"`
	}
	body, _, err := m.get(ctx, m.api+"?"+q.Encode(), 4<<20)
	if err != nil {
		return recording{}, nil, about{}, err
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return recording{}, nil, about{}, fmt.Errorf("decoding recordings: %w", err)
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
		return recording{}, nil, about{}, fmt.Errorf("page %d has no downloadable recording", page)
	}
	rec := usable[rand.IntN(len(usable))]
	looked := make(chan about, 1)
	go func() { looked <- m.lookUp(ctx, rec) }()
	audio, _, err := m.get(ctx, rec.File, maxAudio)
	ab := <-looked
	if err != nil {
		return recording{}, nil, about{}, err
	}
	return rec, audio, ab, nil
}

// get reads u, up to limit bytes, and says what type the answer was.
func (m *Module) get(ctx context.Context, u string, limit int64) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "skua (https://skua.lol)")
	res, err := m.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%s answered %s", req.URL.Host, res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = fmt.Errorf("%s sent more than expected", req.URL.Host)
	}
	return b, res.Header.Get("Content-Type"), err
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
	if !m.now().Before(p.end()) {
		m.pranks.CompareAndDelete(k, v)
		return
	}
	go m.replace(e.Client().Rest, e.Client().ApplicationID, e.GuildID, msg, p)
}

// replace posts the bird for the first message, then removes that original.
// Later messages are removed without another webhook post. In a thread the
// webhook lookup fails and the message stays, as it does on any failure.
func (m *Module) replace(r rest.Rest, app, guild snowflake.ID, msg discord.Message, p *prank) {
	ctx, cancel := context.WithTimeout(context.Background(), replaceBy)
	defer cancel()
	// Only one send may be in flight. Failed sends can retry after a cooldown.
	p.mu.Lock()
	if p.posted {
		p.mu.Unlock()
		m.deleteOriginal(ctx, r, guild, msg.ChannelID, msg.ID, p.by)
		return
	}
	if len(p.pending) < pendingBirdLimit {
		if p.queued == nil {
			p.queued = make(map[snowflake.ID]struct{})
		}
		if _, exists := p.queued[msg.ID]; !exists {
			p.pending = append(p.pending, original{channel: msg.ChannelID, id: msg.ID})
			p.queued[msg.ID] = struct{}{}
		}
	} else if !p.overflow {
		p.overflow = true
		slog.Error("bird: pending original queue overflow", "guild", guild, "user", msg.Author.ID, "limit", pendingBirdLimit)
	}
	now := m.now()
	if p.posting || now.Before(p.retryAt) {
		p.mu.Unlock()
		return
	}
	p.posting = true
	until := p.until
	p.mu.Unlock()
	name := msg.Author.EffectiveName()
	if msg.Member != nil && msg.Member.Nick != nil {
		name = *msg.Member.Nick
	}
	if n := strings.ToLower(name); strings.Contains(n, "discord") || strings.Contains(n, "clyde") {
		name = msg.Author.Username
	}
	files := []*discord.File{discord.NewFile("xc"+p.rec.ID+".mp3", "", bytes.NewReader(p.audio))}
	if p.about.photo != nil {
		files = append(files, discord.NewFile(p.about.photoName, "", bytes.NewReader(p.about.photo)))
	}
	post := discord.WebhookMessageCreate{
		Content:         marker(msg.Author.Username, until.Sub(now), p.rec) + p.about.lines(),
		Username:        m.screen.Check(name).Text,
		AvatarURL:       msg.Author.EffectiveAvatarURL(),
		AllowedMentions: core.NoPings(),
		Flags:           discord.MessageFlagSuppressEmbeds,
		Files:           files,
	}
	_, err := m.post.Send(ctx, r, guild, msg.ChannelID, app, post)
	p.mu.Lock()
	p.posting = false
	if err != nil {
		p.retryAt = m.now().Add(retryBirdAfter)
		p.mu.Unlock()
		return
	}
	p.posted = true
	pending := p.pending
	p.pending = nil
	p.queued = nil
	p.overflow = false
	p.mu.Unlock()
	for _, original := range pending {
		m.deleteOriginal(ctx, r, guild, original.channel, original.id, p.by)
	}
}

func (m *Module) deleteOriginal(ctx context.Context, r rest.Rest, guild, channel, id snowflake.ID, by string) {
	if m.guard.Allow(guild, guard.SpamMessageDelete) != nil {
		return
	}
	err := r.DeleteMessage(channel, id, rest.WithCtx(ctx), rest.WithReason("bird: replaced with a recording, started by "+by))
	m.guard.Done(guild, err)
}

// markdown escapes brackets too, so a name cannot close the species link.
var markdown = strings.NewReplacer(`\`, `\\`, `_`, `\_`, `*`, `\*`, `~`, `\~`, "`", "\\`", `|`, `\|`, `>`, `\>`, `[`, `\[`, `]`, `\]`)

// marker is the whole post: who is which bird for how long, the species
// linking to its xeno-canto entry, which carries the credit and licence.
// Everything is escaped and lowercase, the link held from unfurling.
func marker(username string, left time.Duration, rec recording) string {
	mins := max(int(left.Round(time.Minute)/time.Minute), 1)
	species := strings.ToLower(rec.En)
	return fmt.Sprintf("-# @%s is %s [%s](<https://xeno-canto.org/%s>) for %dm",
		markdown.Replace(username), article(species), markdown.Replace(species), rec.ID, mins)
}

// article is "a" or "an" for species. "eu", "ura" and "uni" sound like
// "you": a eurasian wren, a ural owl.
func article(species string) string {
	if species != "" && strings.ContainsRune("aeiou", rune(species[0])) &&
		!strings.HasPrefix(species, "eu") && !strings.HasPrefix(species, "ura") && !strings.HasPrefix(species, "uni") {
		return "an"
	}
	return "a"
}
