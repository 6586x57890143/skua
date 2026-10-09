# skua spec

skua is a lean Discord bot that exists to try things: new Discord surfaces, faster
Go, sharper safety at no measurable cost. Every experiment is a hypothesis with a way
to tell whether it worked, and the log is below.

## Principles

1. **One binary, modules by package.** A module implements `core.Module` (`Name`,
   `Want`, `Commands`) and is listed in `cmd/skua/main.go`. Nothing is loaded at runtime.
   Modules never import each other. Whatever one needs it takes as a constructor
   argument, as a narrow interface in its own package.
2. **Ask Discord, don't assume.** Intents come from the portal's application flags
   (`internal/intents`). skua identifies with `wants ∩ granted`, never more, so close
   code 4014 cannot happen. A module missing a required intent is skipped by name. The
   portal is re-read every 10 minutes, and a change restarts the process. The same goes
   the other way: at boot skua writes the app's install settings from what its running
   modules declare in `Perms`, so the invite asks for exactly what is in use.
3. **Fail closed, cheaply.** A command with no tier fails boot. Admin means the
   bootstrap user, the Administrator bit, or the guild owner, and nothing else.
   `internal/guard` caps writes per guild and per op with one CAS (~7ns, 0 allocs), and
   a per-guild breaker opens on repeated 429/5xx. Safety that costs nothing measurable
   is the only kind skua takes.
4. **Nothing pings.** Every message skua sends carries `core.NoPings()` (`"parse":[]`). A
   send that genuinely needs to ping gets its own named function with an explicit
   allowlist.
5. **Smallest thing that works.** No table, cache, job or config option until a module
   needs it. Deliberate shortcuts carry a `ponytail:` comment that names their ceiling.

## Stack

| | |
|---|---|
| Discord | `disgoorg/disgo` v0.19, zstd-stream gateway compression (its default), every cache off except guilds |
| Database | Postgres 16 via `pgx/v5` `pgxpool`, optional. SQL migrations in `internal/store/migrations`, compiled in and applied at boot in one transaction |
| Build | Go 1.27, built for the deploy host's platform (`VPS_PLATFORM`), `CGO_ENABLED=0 -trimpath -s -w`, distroless nonroot, PGO from `cmd/skua/default.pgo` when present |
| Deploy | `scripts/deploy.sh`: builds natively on `foundry` (arm64) from `git archive`, compose, rollback via `previous-tag.env` |

## Experiments

Each one is a hypothesis with a way to tell whether it worked. Record the result here.

| # | Experiment | State |
|---|---|---|
| 1 | zstd-stream gateway compression | **On**, disgo's default. Compare bytes/s against `zlib-stream` with `gateway.WithCompression` |
| 2 | PGO: capture 30s of CPU from `SKUA_PPROF` under real load into `cmd/skua/default.pgo` | open |
| 3 | GCRA cells instead of a mutex-guarded sliding window | **Done**, 6.9ns/op, 0 allocs under `RunParallel` |
| 4 | Intent probe instead of a gateway-ready watchdog | **Done**, pending a live portal-toggle test |
| 7 | Filling a post with reactions: how many calls in flight (1, 2, 4, all), and a raw path paced ahead of disgo by Discord's headers, measured live with `tools/reactbench` | **Done**: disgo waited a whole second on every 429 (`Retry-After` is whole seconds) and Discord's per-user reaction limit draws about 7 a fill, so 15 birds took 10-15s. `internal/ratelimit` waits the 429 body's `retry_after` instead (never `X-RateLimit-Reset-After`, the bucket's reset, which a shared 429 can put far short of its real wait; shared, global and Cloudflare 429s are left to disgo): about 5.5s, every in-flight count alike. The raw path hit 20 429s in a fill and was dropped. preen fills serially. After about 16 fills back to back (around 240 reactions in two minutes) Discord adds a shared-scope limit on the channel and every strategy slows to about 20s; one self-react never gets near it |
| 6 | Always-on tracing from the metal to the API: every module timed in four legs (in, run, wait, http) by lock-free half-octave histograms, the Go runtime's scheduler latency and GC pauses beside them, and a flight recorder of the last 10s of execution trace with a region per module. `/perf` shows it slowest first | **Done**: 33 ns and 0 allocs a sample (28 ns under `RunParallel`); not run under real load yet |
| 5 | Input filter: Unicode folding, and a consonant-skeleton key that gates each rule's regex, instead of running every rule's ASCII pattern on every message | **Done**: clean message 3.0us and 0 allocs against 14us for every rule in turn (20us as one alternation); catches lookalikes, fullwidth, zero-width, Zalgo and held letters |

## Milestones

| # | Milestone | State |
|---|---|---|
| 0 | Scaffold: router, intents probe, guard, pgx, brand, `/ping` `/status`, deploy | done |
| 1 | `/whisper`: a chat-restricted member posts one line through a per-channel webhook, marked with their username | done |
| 2 | `internal/filter` for every module that posts member text; whisper screens through it and members can edit and delete their own whispers | done |
| 3 | `/bird` (experimental, admin): for up to an hour every message one member sends is replaced by one short xeno-canto bird recording, posted through skua's webhook as them with a credit line. Needs `SKUA_XENO_CANTO_KEY` | done, not run against Discord or a live key yet |
| 4 | `/purge`: members delete their own messages in a server on demand, on a schedule or live, at the fastest rate one token allows. The break-glass admin can run it for a member; server admins cannot. Each server is read once into an index of who wrote which message (IDs and authors, never content, about 6 bytes a message), forgotten when skua leaves; a purge reads only what is new, then deletes many channels at once. A readout that outlives its 15 minute token carries on in a DM, or on a progress button for members with DMs closed | done: `now`, `stop`, `live`, `every`, `status`, break-glass with `jobs` (every purge running or coming up, in every server) and the index; run against Discord |
| 6 | `/perf` and the build stamp: admins see where the time goes, `/status` and `/help` say which commit is running, and `/help`'s index is compact, one avatar and each module's name with its line as subtext below | done, not run against Discord yet |
| 5 | `/help`: the field guide. One Components V2 container per page, an index of every running module with a page and a page per module, turned with a select menu; stateless, so any guide ever sent keeps working. Admins can post it, and switch a module off or on for their server from its page (`help` and `status` stay on) | in progress |
| 7 | `armada`: chosen Discord channels and chosen channels of one Armada community, paired in `SKUA_ARMADA_LINKS` so the layouts stay their own. Text, replies, edits, deletes, attachments and reactions both ways (an Armada member's reaction shows as skua's own, one per emoji; an Armada custom emoji not drawn from Discord stays in Armada). Each linked server's custom emoji and stickers go up as one emoji pack under skua's key (stickers after the emoji, never taking a name one has; a Discord sticker crosses as its image at the same URL, a Lottie one as its name) (kind 30030, d `discord-<guild>`, refreshed with the invite) and every puppet's kind 10030 names it, so an Armada member tapping a bridged emoji can add the pack and use the server's emoji, which cross back as the native ones whatever their palette renamed them to; a Discord member speaks in Armada as a puppet key derived from their id, an Armada member in Discord through the webhook under their kind 0 name. Armada's Control Plane is folded as Armada folds it (`internal/concord`, checked against vectors from upstream's TypeScript), so bans and moderator deletes hold. A private channel is read once skua is granted it in Armada: the key arrives as a Direct Invite, taken only from a MANAGE_CHANNELS holder. Not yet: typing, backfill | built and tested, not run against Armada or Discord yet |
| 8 | `/notify`: a card in a channel when a followed account posts on youtube, x or tiktok, or goes live on youtube, twitch or kick, optionally pinging one role, and, when skua can hand that role out (Manage Roles, above it), carrying a ping me button that gives it to whoever presses it or takes it back. Run from a panel laid out like `/help` with each platform's pixel icon: a channel select binds the server's notify channel, a platform select opens a form (account, an optional channel of its own, an optional role), and a select drops follows. Each account is polled once however many servers follow it (streams every minute, posts every few), the first look is a silent baseline, and what was seen is kept in Postgres so a deploy neither repeats nor misses a card. youtube is keyless (its feed and /live page), x and tiktok come through an RSSHub beside skua, twitch and kick through app client credentials; a platform left unset isn't offered, and one that breaks stops only its own cards. With `SKUA_HOOKS_URL` set they push instead, through a Cloudflare Tunnel to skua: twitch by EventSub, kick by its signed webhooks and youtube uploads by WebSub, each push answered by checking that one account at once; the timers keep their pace behind it, since one call covers fifty kick channels or a hundred twitch logins and a push that never arrives is otherwise silent, so a lost push costs a minute. Subscriptions are brought back in line hourly, so one a platform drops returns within the hour, and each platform's push shows on notify's /help page to admins: subscribed, refused with the platform's reason, ok with its last push, or missed once two cards in a row came by timer with no push between. A card's preview is checked before it goes out, so Discord never shows one as not found: youtube's largest falls back to its smaller, and a stream whose platform hasn't made one yet (kick, for its first minutes) gets it edited in once it has; while a stream is on its cards stay current, a new title or category at once and a fresh preview every 10 minutes on a new link, so Discord never keeps a stale or missing one. When a stream ends its live card is edited where it stands into the stream's VOD, in one muted ember for every platform: "was live", how long it ran, and a button to the VOD (twitch's archive; kick's straight from the session list its own site reads, which the public API lacks, and its videos page when that list doesn't answer; youtube's watch page, which becomes the VOD); the cards are kept in Postgres so a restart still finds them, and twitch pushes stream.offline and kick its end so the edit comes at once | built and tested, youtube and tiktok read live; not run against Discord, twitch, kick or x yet |
