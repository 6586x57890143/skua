# social cards

`fix` is Skua's Go module for social previews. It builds Discord Components V2
cards itself, using maintained upstream metadata APIs instead of running another
Discord bot or maintaining a fork of every platform scraper.

An admin enables **fix** on its `/help` page. It starts off, per server. Turning it
off hides its commands and stops automatic conversions. With a database that
switch survives restart, through the existing module switches. Without one it
returns to off on restart.

- `/fix link:<https post URL>` previews a post privately
- `/fix link:<URL> share:true` posts it for the channel
- message apps menu: **fix embed** previews the first supported link privately
- automatic conversion replies with up to three cards per message

Automatic conversion needs the message content intent enabled in the Developer
Portal. Commands keep working without it. Bot, webhook and system messages are
ignored. Code, spoilers and angle-suppressed links are left alone. Originals are
preserved; she never asks for Manage Messages or deletes someone else's message.
Cards cannot ping anyone. Creator names, captions and alt text keep their original
case, while Skua's own labels follow the shared lowercase voice.

## providers

| platform | primary metadata |
| --- | --- |
| x / twitter | FxEmbed API v2; FixEmbed fallback |
| bluesky | FxEmbed API v2; FixEmbed fallback |
| reddit, instagram, threads, pixiv | FixEmbed |
| bilibili, youtube community posts, pinterest | FixEmbed |
| tiktok, tumblr, twitch, deviantart / sta.sh | FixEmbed |

The defaults need no API secrets. The source URL is sent to the selected service;
Discord fetches the resulting remote media. Credentials from notify are therefore
not needed. A future authenticated provider should receive its narrow interface
and credentials from main, rather than importing another module.

For a self-hosted service, set an HTTPS base URL in `SKUA_FIX_TWITTER_API`,
`SKUA_FIX_BLUESKY_API` or `SKUA_FIX_API`. These are trusted operator settings, not
member input. The provider client has deadlines, a 2 MiB response limit, a fixed
User-Agent and no redirect following. It checks response success, platform and
post ID where the API exposes one. No provider error body is sent to Discord.

Supported link shapes are tested in `links_test.go`. YouTube support is for
community posts, not ordinary videos that Discord already handles. Short links
are resolved by the maintained metadata service, not by unrestricted local fetches.

## reddit video and audio

Reddit video goes into a Media Gallery as video, never as its poster image. Signed
MP4 query parameters are preserved. The upstream video is preferred to thumbnail
fallbacks and mixed video/image posts keep both kinds of media.

Reddit often publishes separate audio and video tracks. If the API returns a
`v.redd.it` stream, Skua also asks the fixed `embed.reddit.com` endpoint for that
post and selects a combined `packaged-media.redd.it` MP4, preferring up to 720p.
This recovers a live regression found during implementation: Reddit now emits
`dimensions` before `url`, which upstream's ordered regex misses. Skua uses named
JSON fields. It fetches at most 512 KiB of embed HTML within four seconds, allows
only the exact packaged-media host and passes the signed URL through unchanged.

If a combined rendition cannot be recovered, the card says the preview may be
silent and keeps the original link. The six-hour live canary fetches a bounded MP4
initialization range and checks both audio and video track handlers. Playback in
Discord still needs a live Discord check; tests do not post to a server.

## limits and lifecycle

Up to eight fetch workers run at once. The existing per-guild message guard
limits both API admissions and Discord writes; every write reports struggling
responses to the guard. Requests stop at shutdown. Cards have a bounded text
budget, up to 20 media items in galleries of ten, and up to six context sections.
Source-sensitive media is spoilered and sensitive avatars are omitted. A failed
provider yields a V2 card with the original link.

Automatic replies follow edits and deletes for the most recent 512 source
messages across the process, including edits that hide or remove a link. Tracking
keeps source/reply IDs and validated URLs for pending or published links; it stores
no captions or message text and survives neither restart nor eviction. Older cards
therefore remain when their source is later edited/deleted. Edits to an untracked source do
not create a new card. Manual shares are standalone cards. Per-server counters
are on the module's admin help page; no post contents are retained. Cleanup is
prioritized and transient delete failures retry with a single timer. Missing
replies count as cleaned. Permission refusals keep their IDs without retrying;
that source cannot create replacement cards until its state is evicted or reset.
Pending edits coalesce, and a partially sent multi-link message resumes at its
first unfinished link rather than repeatedly spending its budget on the first.

## staying current

Hosted APIs pick up their maintainers' deployed platform fixes without a Skua
release. That is different from following the tip of a GitHub branch: unmerged
contributions are not production code. `upstreams.json` records the exact source
revisions used to review these contracts and NOTICE credits both projects.

`go run ./tools/fix-upstream` reports commits since those revisions, relevant
changed paths and open pull requests, with links and contributor commit history.
It is read-only. `-check` exits nonzero on any source drift;
`-check-relevant` fails for extraction, API, dependency or license changes. The
watch paths in `upstreams.json` cover both source trees and dependency manifests. Paths match exact files,
directory descendants or Go path globs; `*.py` watches all root Python extractors
and shared helpers, including newly added platforms.
Renames out of watched paths, rewritten history and potentially truncated GitHub
comparisons also require review. A green comparison means no watched file change
was found, not proof that a platform's API is unchanged.

The **embed upstream review** workflow runs every six hours and manually. Its two
independent jobs separate **source changes needing review** from **live contract
failures**. X text/video/GIF, Bluesky and Reddit audio canaries keep running even
when GitHub comparison requests fail. A platform can break without an upstream
commit, so both signals matter. Other supported platforms currently rely on source
review and offline fixtures; they do not yet have live canaries.

Each source review saves a 90-day artifact containing the report, a JSON manifest
of exact reviewed/observed SHAs, complete source tarballs with their original
licenses, and SHA-256 checksums. The source is never executed or added to Skua's
binary. Both the comparison and archive use the same resolved SHA, so a new commit
during the run cannot change what was reviewed. Open contributions remain linked
in the report; unmerged patches require separate review. Live logs are retained
for 30 days. Downloads are bounded to 128 MiB per archive and 90 seconds each.

Enable GitHub Actions failure notifications for this repository to receive these
signals; this workflow does not send Discord messages or open issues. Scheduled
runs can be delayed by GitHub. Use **Run workflow** for an immediate check. Source
drift keeps failing until a maintainer completes the review and updates the lock;
successful canaries alone never advance it.

When a signal fails:

1. Read the failing job and download its artifact. A deleted canary post or service
   outage can fail the contract check too; confirm the failure before changing code.
2. Compare the retained sources and upstream commits. If the hosted service already
   fixed extraction and Skua's contract still holds, no Go port is needed. Otherwise
   adapt the Go parser or Reddit recovery and add a minimal regression fixture.
3. Keep attribution in NOTICE and any applicable source license. FixEmbed's AGPL
   implementation remains upstream; consuming its API is the existing design.
4. Run race tests, the 85% per-internal-package coverage gate and live canaries.
   For runtime changes, recheck the obs/perf targets and benchmarks below.
5. Open a PR with the fix, fixture and exact reviewed SHA/date in `upstreams.json`.
   Explain a no-code review when only the pin changes. Archive checksums establish
   artifact integrity, not maintainer approval. Never advance a pin just to clear a
   red job. Merge through the normal CI gate.

For a local report and source manifest:

```sh
go run ./tools/fix-upstream -check-relevant -sources upstream-sources.json
```

```powershell
go test ./internal/modules/fix -race -cover
$env:SKUA_FIX_LIVE = '1'
go test ./internal/modules/fix -run TestLiveProviders -count=1 -v
```

## measured budgets

`obs` records local build time, upstream HTTP and complete delivered previews
separately. `/perf` shows soft p99 targets in its copyable code-block table and
puts missed targets first. The initial budgets are 5 ms for card construction,
4 s per metadata request, 2 s for Reddit audio recovery and 6 s for complete
preview delivery, including queue delay for automatic cards. Rejected commands
and unfinished retries do not count as completed-preview samples. HTTP errors
still appear in source timing and failure counters. All targets are operational
goals; deadlines remain longer so a temporary miss can still produce a card.

The ordinary-message path must allocate zero objects, enforced by a test.
`go test ./internal/modules/fix -run '^$' -bench . -benchmem` measures link scanning,
canonicalization, metadata decoding and rendering. Public API canaries also
exercise X video and GIF MP4s, Bluesky fractional timestamps and Reddit audio.

Measured on 2026-10-23 with Go 1.27.3, Windows/amd64 and a Ryzen 5 9600X,
three local benchmark runs of 300 ms each:

| operation | time per operation | allocations |
| --- | --- | --- |
| scan an ordinary 1.8 KiB message | 252-260 ns | 0 |
| recognize three social links | 2.43-2.52 us | 18 |
| canonicalize a Reddit post link | 637-747 ns | 5 |
| decode representative FxEmbed metadata | 3.34-3.58 us | 10 |
| build a mixed video/image card | 4.64-5.32 us | 86 |
| record an obs sample | 31.5-32.3 ns | 0 |

These are benchmark averages, not production p99 measurements. Across five live
trials for each of X text, video, GIF, Bluesky and Reddit audio/video, the slowest
metadata result was 1.633 s, below the 4 s canary budget. Every Reddit combined
MP4 probe contained both audio and video tracks. Discord delivery has not been
live-tested; its complete-delivery budget is instrumented for deployment.
