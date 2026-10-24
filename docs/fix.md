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
ignored. Code, spoilers and angle-suppressed links are left alone. Message text is
preserved. After every supported link has a successfully delivered card, she
suppresses the source message's native previews using Manage Messages. This is a
flags-only edit, never a deletion. Without that permission the card still works
and the original preview remains. Metadata/delivery failures, unsupported links
and links beyond the three-card limit keep the original previews, because Discord
can only suppress all embeds on a message at once. Private command previews never
change the source. Permission refusals are not retried. If an edit makes replacement incomplete,
she restores native previews only when she set the suppression flag herself;
preexisting author suppression is preserved. Transient restore failures use the
bounded retry queue. This follows the same 512-message tracking window as card
edits: restart or eviction ends automatic preview-flag restoration.
Cards cannot ping anyone. The platform icon sits beside the author's name, which
links to the original post; Reddit instead links the post title to the post and
the author to their Reddit profile. There is no oversized avatar or open button. Body URLs
and quote sources are clickable, engagement uses app icons, and Discord renders
the timestamp in each reader's timezone. Creator names, captions and alt text keep their original
case, while Skua's own labels follow the shared lowercase voice.

## providers

| platform | primary metadata |
| --- | --- |
| x / twitter | FxEmbed API v2; FixEmbed fallback |
| bluesky | FxEmbed API v2; FixEmbed fallback |
| reddit, instagram, threads, pixiv | FixEmbed |
| bilibili, youtube community posts, pinterest | FixEmbed |
| tiktok, tumblr, twitch, deviantart / sta.sh | FixEmbed |
| kick clips | Kick website API; signed MP4 relay for HLS |

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

Reddit often publishes separate audio and video tracks. If the API returns no media at all or a
`v.redd.it` stream, Skua also asks the fixed `embed.reddit.com` endpoint for that
post and selects a combined `packaged-media.redd.it` MP4, preferring up to 720p.
This recovers a live regression found during implementation: Reddit now emits
`dimensions` before `url`, which upstream's ordered regex misses. Skua uses named
JSON fields. It fetches at most 512 KiB of embed HTML within four seconds, allows
only the exact packaged-media host and passes the signed URL through unchanged.

A successful metadata response can still omit the video entirely, as happened on
`r/Destiny/comments/1x7ehs9`. Missing media now triggers the same bounded recovery.
Existing image galleries and quoted media stay intact. Comment URLs never inherit
the parent post's video. A readable page without a recognized rendition remains
inconclusive, so a challenge page or a changed payload cannot hide native media. If this check cannot be
completed, the card says so and automatic replacement keeps the native preview.
The reported post is a live audio/video canary alongside the original Reddit test.

If a combined rendition cannot be recovered, the card says the preview may be
silent and keeps the original link. The six-hour live canary fetches a bounded MP4
initialization range and checks both audio and video track handlers. Playback in
Discord still needs a live Discord check; tests do not post to a server.

## limits and lifecycle

Up to eight fetch workers run at once. The existing per-guild message guard
limits both API admissions and Discord writes; every write reports struggling
responses to the guard. Requests stop at shutdown. Cards have a bounded text
budget, up to 20 media items in galleries of ten, and up to six context sections.
Source-sensitive media is spoilered; the header uses a platform icon instead of a remote avatar. A failed
provider yields a V2 card with the original link.

Automatic replies follow edits and deletes for the most recent 512 source
messages across the process, including edits that hide or remove a link. Tracking
keeps a content hash for duplicate-event detection, source flags, source/reply IDs and validated URLs for pending or published links; it stores
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

The compact-card revision was rechecked on the same machine: mixed-media cards
rendered in 4.85-4.92 us (87 allocations), ordinary-message scanning in 245-255 ns
with zero allocations, obs sampling in 31.7-32.3 ns with zero allocations and guard
admission in 4.98-5.53 ns with zero allocations (three 300 ms runs each). These are
local averages. Native-preview suppression adds one guarded Discord write after
successful delivery and remains inside the existing end-to-end measurement.

## compact presentation and loading

Only fix changes in this revision; notify remains the visual reference. Reddit
uses a linked post title with a smaller author/profile line. Other platforms use
a linked author with a smaller handle/profile line where the provider supplies a
valid X or Bluesky profile. Quote blocks keep their own identity and media.
Engagement is ordered replies, reposts, likes, views, with notify-style counts
such as 7.2k. Views are shown only when supplied. The footer reserves its share of
the 4000 UTF-16 text budget and uses Discord relative time with the exact time
available through the client.

The container accent uses a muted platform colour when ready, slate while
loading, amber for incomplete media, red when unavailable and grey for sensitive
posts. Colours require no image download or palette extraction.

A fix command immediately shows a tiny animated Skua, then replaces that same
message with the result. This uses the same initial response and final update as
the previous deferred response, without periodic edits or extra REST calls on successful requests. A failed final
delivery gets one guarded, best-effort terminal error update with a fresh three
second deadline; authentication, permission and missing-message refusals are not
retried. The source animation also has a finite loop count.
Private previews remain private. Automatic conversions have no loading message.
The app emoji is generated from Skua's existing source art, loops gently in three
frames and stays below the 10 KiB asset budget. Missing emoji sync falls back to
a static module mark or text. Client animation settings control playback.

Measured after the compact layout on the same Windows/amd64 host (three 300 ms
runs): mixed-media rendering 4.97-5.81 us, 87 allocations; ordinary-message scanning
249-250 ns, zero allocations. These are local averages, not production p99.

Reddit link posts resolve one supported external post through the maintained
provider APIs, with a four-second child deadline inside the existing fetch
budget. Resolved media replaces the Reddit link thumbnail; the Reddit title
and author remain the card identity. Unsupported article URLs stay links, and
Reddit crossposts are not followed recursively. Failed linked-media extraction
keeps the source preview visible. The linked Twitch clip regression is part of
the live audio/video canaries. Masked username links preserve literal underscores.


### Kick clips and the one-line footer

Kick clip paths and channel URLs with a `clip` query resolve directly, including
when linked from a Reddit post. Direct MP4s stay on Kick's CDN. HLS clips are
repackaged as MP4 in Go without re-encoding H.264 video or AAC audio. Skua finishes
preparation before treating the card as a complete replacement. A failed fetch,
unsupported stream or exceeded limit keeps the native preview visible.

The signed `/fix/media/kick/` route shares the configured `SKUA_HOOKS_URL` listener
and `SKUA_HOOKS_SECRET`, using a separate signature namespace. Only Kick clip IDs
issued by Skua are accepted. Every fetched segment stays in the exact clip CDN
directory; redirects, encrypted/live playlists and external playlist URLs are
rejected. Limits: 64 MiB input/output, 181 seconds, 128 segments, one preparation,
two concurrent responses, a one-clip 15-minute memory cache and 20-second response
write deadlines. HLS preparation requires the existing public hooks configuration;
direct MP4 clips need none. Metadata contains no credential or secret.

Kick child preparation has a ten-second cancellation deadline inside the existing
12-second fetch deadline. The separate `fix media` HTTP soft p99 target is eight
seconds; normal metadata and card-building targets remain in place. Playback
serves HEAD and byte ranges. The reported Reddit-to-Kick regression and actual
MP4 audio/video tracks run in the six-hour live contract workflow.

The footer uses one subtext row: stats, provider attribution, relative timestamp.
Components V2 text has no independent right-alignment column; Discord may wrap the
row on narrow clients. No whitespace padding or new action buttons are inserted.

### Reddit uploads without a packaged MP4

Newer Reddit uploads list only CMAF video and audio files in their HLS master
playlist. Skua picks the largest rendition up to 720p and the audio group it
names, then joins the two fragmented MP4s into one file on the signed
`/fix/media/reddit/` route, copying samples without re-encoding. Without the
relay, or when joining fails, the video track plays alone with a silent note.
Uploads without an audio group play the video file directly.
