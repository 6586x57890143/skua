# Handoff: Milestone 0 (scaffold)

Written 2026-10-02. Read this with `CLAUDE.md` and `SPEC.md`.

## Where the branches are

| Branch | State |
|---|---|
| `main` | Milestone 0, pushed to `github.com/6586x57890143/skua`. |
| `ci/deploy-foundry` | The CI/CD pipeline and repo hygiene. PR #1. |
| `feat/fix-embeds` | social Components V2 cards, Reddit audio/video recovery, upstream tracking and /perf timing tables with p99 budgets; reviewed by gpt-6 astra |
| `feat/pp-open-warm` | Launch M1 (draft until 11-02, merge after M0 #186): a one-shot "open /pp to everyone" switch on /help, the warm side (+10% labour for a member's first 14 days, first day read from the ledger), and a second, good talk quest on a first deal. Balance 0.14.0. |
| `feat/notify` | Milestone 8, `/notify`: creator alerts for youtube, x, tiktok, twitch and kick. Not yet run against Discord. |

## What M0 did

Built the spine and one module. skua probes the portal, identifies with only what is
granted, registers `/ping` (public) and `/status` (admin) per guild on GuildReady/GuildJoin,
and answers them with branded ephemeral embeds. `/status` reports granted vs identified
intents, skipped modules, database round trip, gateway latency and uptime.

Decisions worth keeping:

- **Our own command map, not disgo's `handler.Mux`.** Dispatch is one map lookup plus
  the tier check, under `recover()`. Mux's path routing buys nothing for top-level
  commands, and owning the map is what lets `Add` refuse an unset tier at boot.
- **The re-probe exits instead of reconnecting.** It's a single `return`, and compose's
  `restart: unless-stopped` does the rest. A clean exit is logged at info, not error.
- **CI builds arm64 only and pushes to GHCR. foundry pulls by commit SHA.** The
  manual `scripts/deploy.sh` builds on foundry itself from `git archive`, under the
  same image name and directory, so the two paths never disagree about what is running.
- **The prune is label-scoped.** GitHub concurrency groups are per repository, so
  nothing serialises skua's deploy against other projects deploying to the same
  daemon. An unscoped prune racing another project's pull fails with `lease does not exist`.
- **The database is optional.** Without `SKUA_DATABASE_URL` skua runs with no DB and
  `/status` says so. That makes the test bed cheap to run anywhere.

## Not done yet, deliberately

- The deploy is held (`DEPLOY_ENABLED` unset) until someone runs `go run ./tools/setup`,
  which needs the bot token typed in and so cannot be done by an agent. It writes the
  token to foundry, flips the variable, deploys and prints the invite. Merges until then
  still build and push the image.
- No migration runner or tables: nothing needs one.
- `/whisper` (milestone 1) is the first module that writes through the guard: webhook
  posts and creations per guild, plus a per-member `WhisperMember` cap so one member
  cannot spend a guild's budget. `main` builds one guard and hands it to every module.
- Webhook posts skip Discord's AutoMod and slowmode, so whisper applies them itself.
  Slowmode is done: per channel and member, with Manage Messages and Manage Channels
  exempt. AutoMod's job is done by `internal/filter`: every whisper and the display name
  it wears are screened before posting. Slurs are rewritten in place with a harmless
  word and the whisper still goes out; a bot token, phishing link or IP grabber refuses
  it. When automod lands, its rung 1 is `filter.Default()` over member messages and its
  rung 0 skips webhooks, so whisper and automod share one definition of a slur. Its
  rewrite-and-repost goes through the same `webhook.Poster` as whisper.
- Members own their whispers: "Edit whisper" and "Delete whisper" under Apps on a right-click.
  No table: a whisper is the member's when it came through skua's webhook (skua's
  application ID on the message) and its last line is the marker with their username.
  An edit opens a one-line box prefilled with the whisper, and the new text passes the
  same gates, cap and filter as a post. Edits aren't charged slowmode, as in Discord.
- The router takes message commands and modals (`core.Modals`, optional), and
  `webhook.Poster` gets, edits and deletes through skua's own webhooks only.
- Not run against Discord yet: needs a dev bot token.
- No `default.pgo` yet (experiment 2 needs real load first).
- The avatar is drawn and recoloured; the banner is still computed. A drawn banner can
  replace `art/skua_banner.png` as long as the file name stays.

## /pp: the pot economy (2026-10-17)

The economy runs on a charter (`internal/modules/pp/charter.go`, v2, its hash pinned
in `charter_test`). Only time creates pp. A 2% levy fills a per-server treasury, which
pauses at 8 weeks of levy. The treasury pays weekly checkpoint prizes and admin grants;
grants open 28 days after the first levy and are capped and posted. Spending is burned.
There are no transfers and nothing scarce, and prices are set in pot days (428 pp). There
is no purse cap: a heavy purse line (keep a tenth past 45 pot days) switches on only if
the shop burns under 5% of what's minted two seasons running.

Merged, in order:

| PR | What |
|---|---|
| #35 | Hash-chained ledger (`pp_ledger`), daily audit, commit-reveal season seeds with drand, ledger head posted daily |
| #36 | Economy sim on a month of real activity (`testdata/activity.json`, aggregates only) |
| #37 | Charter, treasury and levy, member receipts, sim without a cap |
| #38 | Contest grants (a second admin above 1 pot day) |
| #39 | Weekly checkpoints paid from the treasury |
| #40 | Shop: the prestige ladder (the rookery stair) and the backstop trigger |
| #41 | Quests: `Balance.QuestKinds`, selfie quests, one reroll a day |
| #42 | Level board |
| #43 | `core.Edit` lists a file once (it caused "something went wrong" on edits that had acted) |
| #44 | Season 1 starts 2026-11-02 |
| #45 | Picture cards for board, shop, journal (the member ledger) and the admin-only treasury |

Things to know:

- **Sims and the race detector.** The economy sims skip under `-race`; CI runs them in
  a plain step (`go test -run Economy ./internal/modules/pp/`). `SKUA_SIM_REPS=10` runs
  the 90-run matrix, and `SKUA_SIM_FIT=1` runs the sweep.
- **Changing the rules.** A charter change needs a new version and pinned hash. A
  balance change needs a changelog entry, which `changelog_test` enforces.
- **Screen-only art.** Perch icons live in `pixel.screenIcons`, not `Icons`. The guard
  caps a first-boot emoji sync at 50.

Not done yet:

- **The rest of the shop.** Dusts (timed colour roles), wicks (timed channel access,
  admins pick the roles on `/help`, needs Manage Roles), molts, lacquers, carapace
  frames, votive likenesses, spore halos and season relics. Each gets a pixel icon and a
  lore name: a strange, low-fantasy bird world with VanderMeer and early-Souls flavour,
  never on the nose.
- **Lore first.** Each class's background lore is to be written before the shop art.
- **Repricing.** Shop repricing by unique buyers lands with the first catalogue items.

## /pp: company and moving screens (2026-10-24)

Quests in company and animated screens. Lore for all of it is in LORE.md (chirographs
and round robins) and was read by the owner before it shipped.

Merged, in order:

| PR | What |
|---|---|
| #119 | New quests at every tier: crowds (3+ in the room, company only), replies to other birds, streams, cameras |
| #120 | Lore: chirographs, round robins, each class's mark and three signing lore pieces |
| #122 | Chirographs (2) and round robins (3 or 4): time together in voice pays a joinder (60/90/120 pp), the notices screen, `pixel.SoftLoop` |
| #123 | Laurels: every place at a weekly checkpoint wears one for the season |
| #125 | A class's lore card ends on what signing in company teaches |
| #126 | Hotfix: quests' duplicate custom ID; notices still until the WebP break was understood |
| #124 | Every screen with a hand moves: `Canvas.Screen`, the hand drawn last, a box per later frame |
| #127 | A finished pact tells its channel who was paid and who learned a page |

Things to know:

- **A pact is saved on every signer's row** (`pp_member.pact`) and joined back into one
  on load (`joinPacts`). Its key is opener and day; a bird posts one a day, so a key is
  only ever one notice. One joinder a day each.
- **Joinders are in the sim.** A5 holds them under 8% of what's minted; at the generous
  participation the sim assumes they're about 3%.
- **Moving screens are animated WebP**, lossless, from nativewebp (pure Go). The first
  frame goes whole, the rest only the box round the hands. Discord shows a one frame
  animated WebP as a broken image, so a canvas with no hand goes out as JPEG and one
  with a hand always has `len(pixel.Bob)` frames. A screen costs about 170 ms to make,
  nearly all the first frame's encode.
- **Test a new picture format with the test bot first** (`flightless.skua`, see Live
  benchmarks in CLAUDE.md): post it, edit to it and answer an interaction with it
  ephemerally. disgo sends every file as `application/octet-stream`; Discord sniffs it.
- **Every screen's message builds its picture with `screenItem` and `screenAttachment`**,
  so the gallery and the file can't name different files. `TestScreensKeepIDsApart`
  covers quests and notices: Discord refuses a whole message that repeats a custom ID.

Not done yet:

- **The economy report** (economyreport.melting.lol) doesn't know the `joinder`, `lease`,
  `hold` and `release` ledger reasons yet.
- **Still screens**: lore pages, item cards, the treasury and the livery draft have no
  hand and don't move.

## /pp: season 1 launch (2026-11-02)

All launch PRs merged on 11-01 (#186-#195) with deploys paused, then the gate below,
so they went live dark.

- **The launch gate (#196).** /help's open switch arms the launch instead: `pp_guild.launch`
  (migration 041) is the next pp day, and `server.released(today)` is open or that day
  reached. Before it, admins see everything and members see the old screens: the slack
  tide's presses (`launchFronts`) and menu button, the bell, sealed letters going out,
  the morning sheet, the painted stair and yard, the warm side and its first quest, the
  standing order. Balance tuning isn't gated. Something new that waits for a launch asks
  `seesNew` or `released` and lists its presses in `launchFronts`. Arm it before the turn
  it's meant for: arming after sets the next one.

- **M0, launch balance (#186).** The hearth's usual day is the median of the notch stick,
  not the mean, and it lights at 80/100/120%. On the week before launch the old rule
  lit 0 of 7 days and the new one lights 4 (`TestHearthBacktest`). The drum and
  envelope quests ask 12 and 18. Balance 0.13.0.

- **M2, the slack tide lore (docs only).** LORE.md gains the sump as a walkable quarter and the slack tide, its gambling house (the
  *gannet*, the purser, the bones, the slate, the beam, the hold and its waterline, the
  pumps, the drowning hour, the marker, calling out at the rail) and the five winds in
  "How the game maps onto the world". The owner reads it before M3-G1 builds it. The lore
  pays 1.5x, 2.5x and 10x over 1-50 losing, the 7.5% house edge the owner chose. The
  drowning hour rings once or twice a day, leaning to NA and EU evenings, and pays 15x on a
  hundred, so every hour keeps a house edge. The hold is seeded once by a minted 12,500 pp
  note, and every win after that comes out of stakes won. Luck feats go on the deckhead as standing, never pp.

## /pp: the slack tide, G1 (2026-10-31, draft #189, merges on 11-02)

House-banked chance in the wet guild's house aboard the grounded *gannet*. Lore is in
LORE.md (#188). Charter v7, balance 0.15.0, migration 037.

What's in:

- **The house** (`house.go`): every throw settles against the hold alone, so no pp
  moves between birds. 1-50 lose, 51-90 pay 1.5x, 91-99 2.5x, 100 10x (15x in the bell
  hour): 0.925 of a stake on average, 0.975 in the bell, pinned in `TestHouseEdge`.
  Each table's seed comes from `crypto/rand`, and its sha256 (the slate) is posted
  before anyone sits. A throw is `sha256(seed || table || user)`.
- **The hold is its own fund.** The note mints 5,000 pp into it once (ledger reason
  `note`, user 1). It never touches the treasury or the contest fund. At each turn
  everything over the 50,000 pp line is pumped (`pump`, burned). A stake is at most 1%
  of the hold, so the lamp opens at a 10k hold and the lantern at 50k. A bird stakes at
  most 4 pot days a day, doubled each rung (`stakeCap`), and a table never covers more than half the hold at its worst
  case.
- **Money moves once.** Sitting debits the purse into the hold and sets `pp_member.seat`
  in the same flush. A table that never threw (skua stopped) is refunded at boot
  (`refundSeats`), and the audit holds the hold to its rows.
- **The bell** is drawn from the season seed and the day: 45/45/10 across the europe,
  americas and off-peak windows, twice on about 2 days in 7, four hours apart. It is
  one message in the house channel, edited at T-60, T-15, T0 and T+60. Every hundred
  in the hour posts its own all hands line. /pp's title and status screens carry the
  same line. `nextBell` never reads next season's seed early, because reading it fixes
  it.
- **The house sim** (`house_sim_test.go`, A9 to A12): three years of six personas
  through the real code. The house won 35 of 39 seasons in run 1, and A10 holds it to
  3 in 4, not every season, since a lucky season is the point of a house. Every
  persona's median season loses.
- **The surface** (`taproom.go`, `pixel.Animate`): the taproom, table and benches are
  owner-commissioned paintings (`taproom.png`, `board.png`, `settle.png`), finished
  art fitted onto the taproom's 40 colours. Replace them whole. Birds are cut out of
  their portraits. Candles flicker (and lean in the bell), the sand runs, the bones
  tumble, and the winner is chalked on the beam. The hold screen is a cut-away of the
  hull. `SKUA_SHOTS=dir go test ./internal/modules/pp -run TestShots` writes every
  frame as it ships.
- **The admins' money is in sheets**, sheet 1 the treasury and sheet 2 the house. A
  new fund gets a sheet. Health is the hold at half its note (not half the line, which
  it sits under for months) and a positive edge over 7 days.

Not done yet:

- **The economy report** needs the house's reasons (`note`, `stake`, `win`,
  `refund`, `pump`) from the first week of real data.
- **The bell's message is in memory:** a restart mid-bell posts a fresh one.

## /pp: the slack tide, G2 (2026-10-31, draft #190 on #189's branch, merges on 11-02 after it)

Migration 038 (`pp_member.feats`). No balance change; its lines join 0.15.0.

What's in:

- **The sump** (`sump.go`): the slack tide's screen opens on the sump in front of the
  *gannet*, painted at three tides (usual, low, high) in four lights (dusk, night,
  dawn, day, by UTC hour). Low water an hour before the bell, high water while it rings
  or the hold is full. The hold, the beam, the deckhead and the stakes sit in windows
  under it; the card keeps only the admins' shut hint.
- **Living scenes** (`ambient.go`): a still painting with a few chosen things moving a
  small step every frame at their own pace, 12 frames of 200ms. At the sump: the swell
  the length of the shore with the boats riding it, the wall's candles (leaning before
  the bell), the town's windows and her portholes burning a little unevenly, a gull
  over the mast by daylight, the pump at the turn and rings in the bell. At the rail:
  slow bands on the harbour water, the lantern, the windows and the runnel's three
  dyes. No random glints, stars painted still, lit windows never go dark. A card is 1.5
  to 3.6 MB and about 0.6s to draw, which the owner OK'd. `TestLiveDemo` posts every
  surface to the test bot.
- **The beam and the deckhead** (`deckhead.go`): the day's highest throw is chalked on
  the beam and wiped at the turn. Seven luck feats from LORE.md nail a scrap per bird,
  standing only, counted `feat_<key>`. In the taproom the scraps hang dyed in the first
  nailer's livery (wool for none), and unknown ones are chalk outlines. A run is in
  memory, so a restart forgets one.
- **A duel at the rail** (`rail.go`, the lore's calling out): a user picker on the tide card opens a
  two-seat table pinging only the called bird. Same stake for both, each against the
  house, so no transfer, and a loss says the stake goes to the house. Counted
  `house_rail`. Its screen is the view from her rail in four lights.
- **All art is finished art** fitted by box average and k-means (`sump*.png`,
  `rail*.png`, `sprites/`). Replace whole. Every painted surface moves.

Not done yet:

- **The marker**, the newcomer's free throw: it reads M1's warm side, so it lands after
  #187 is on `main`. Its sprite is in `sprites/marker*.png`.
- **The purser** is drawn but not yet on any screen: `portraits/purser.png` (32x48, 15
  colours: an Australian magpie rotoscoped from the owner's photo, dressed by the owner's
  tool after the Tactics Ogre archer) and `figures/purser.png` (64x96, 19 colours, a
  full-art figure with the slate and the ledger, its cheek and crown lit like the portrait's). Next he stands at the rail and the
  table's end.
- **The economy report's house figures**, after a week of real data.

## /pp: sealed letters (M4, 2026-10-31, draft #191)

The north sea company's letters (LORE.md, sealed letters), each holding a code an admin
loaded. Who gets one is deliberately not written down here or in the lore: the code in
`letters.go` is the only record, so keep it out of comments, docs and PR text too.

- **Codes** are loaded by admins from /help ("load letters"). They live in `pp_letter`
  only (migration 039): never in the repo, the ledger or a log line. /help shows each
  holder with their code's last four, enough to check a ticket.
- **Plaintext, not hashed.** A holder reopens the letter on /pp, so the code has to be
  readable.
- Claims are one `update ... for update skip locked` with a unique index on (guild,
  winner), so concurrent claims never share a code. No announcement: the holder finds a lit
  "sealed letter" button under their /pp screens. `letter_sent` in `pp_stat` counts them.
- Live check: `SKUA_LIVE=1 go test ./internal/modules/pp -run TestLiveLetters -v` posts
  both screens to flightless's bench channel.

Not done yet:

- The letter screens move only by the hand. An ambient loop (the ribbon, the wax) waits
  for `pixel.Animate` from #189.
- The owner still has to read LORE.md's sealed letters section.

## /pp: the morning sheet (M5, 2026-11-01)

- **The first `/pp` a member opens each day is a screen of its own**, the morning sheet
  (`dispatch.go`). It shows once they've picked a class: the title stays a newcomer's
  first screen. Its only button is "to the pot" (`pp:status`).
  - **The left panel is the broadsheet.** It holds the day's news (`worldLines` by season
    and day, the same for everyone) and a rumour (`Roll`, slot `slotDispatch`, so it can't
    be rerolled).
  - **The right panel is "your day".** It holds the quests dealt that morning, with the reward
    in the tier's ink and the hand on the first. Below them are notes, as many as fit: livery
    fading tonight, close to a level, hearth lit.
  - Everything is on the picture, as the owner wants.
  - `dispatchDay` is in memory only, so a restart can show the day's sheet again.
- **Content is LORE.md's "The broadsheet"**, written there first, mirrored in
  `dispatch_text.go`. Season 1 week 1 only. Write more once players are seen reading it.

Not done yet:

- **Notes from the warm side** ("cooling in N days") and a guest seen at the rim wait for
  M1 (#187) on main.
- Only the slash command shows it. A button press on an old message doesn't.
- **It moves** (on #192's scenes): the sheet is set over the pressroom at dawn
  (`press.png`, the owner's ChatGPT painting fitted to 384x216 at 40 colours, finished
  art). Both panes are 85% parchment the room shows through (`seeThrough`), placed over its
  quiet wall and bench so the lantern and the window stay clear. The pegged sheets sway
  and the lantern breathes. `pixel.Animate` now keeps a canvas's hand and bobs it, so the
  hand at the first quest points on a moving screen too.

## /pp: a standing order at the yard (2026-11-01)

- **Liveries can renew on their own** (`renew.go`). A switch on the worn livery's card
  (`pp:yardrenew`), on a trade's or faction's card and on one's own page, flips it.
  Opting in is kept in `pp_renew` (migration 040), a table of its own so the member upsert stays as it was.
- **At the turn, before `fade`**, `renewLeases` pays another term for every renewing bird
  whose lease ends that day or the next. It uses that day's price, a `lease` ledger row like a
  press, and no Discord call, since the role is still worn. A purse that can't pay, a perch now too low,
  or one's own livery with no role is left alone, and the livery fades as usual.

Not done yet:

- The morning sheet (#193) still says a livery "fades tonight" for a bird who renews.
  Teach its note about `renew` once both are on main.
