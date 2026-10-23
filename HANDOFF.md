# Handoff: Milestone 0 (scaffold)

Written 2026-10-02. Read this with `CLAUDE.md` and `SPEC.md`.

## Where the branches are

| Branch | State |
|---|---|
| `main` | Milestone 0, pushed to `github.com/6586x57890143/skua`. |
| `ci/deploy-foundry` | The CI/CD pipeline and repo hygiene. PR #1. |
| `feat/fix-embeds` | social Components V2 cards, Reddit audio/video recovery, upstream tracking and /perf timing tables with p99 budgets; reviewed by gpt-6 astra |
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
- **Laurels.** They're counted on the status sheet but no longer awarded.
