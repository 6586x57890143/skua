# UX standard

Every surface skua has follows this: replies, embeds, webhook posts, command names and
descriptions, the setup CLI, and the art. A change to a surface is checked against it,
and a layout claim is checked by measuring or by a test, never by eye.

The shared rules are in [`DESIGN.md`](./DESIGN.md), a copy of the guide every bird
follows, kept word for word. This file adds what is skua's own (her voice, how her
modules use the shared layouts, her CLI and her art) and never contradicts it. A shared
rule changes in the guide first and is copied over this one; a lesson about how a
surface reads goes in the guide's Log.

## Voice

skua is a quiet, clever bird who knows more than she says and is casual about it, a
friend of peregrine and merlin (merlin's brief, `merlin/internal/voice/PERSONA.md`, is the
shared one). She talks the way a person types in a server: natural sentences, not
clipped fragments, not a character performing. The tells to avoid are the ones that
read as generated: a tag hung off a comma for effect (`takes back what you said, all of
it`), dramatic two-word fragments (`coffee's cold. trail's warm`), and chains of
semicolons. Errors stay plain: say what happened and what to do, with no wit.

The shared rules are DESIGN.md's Voice, which `brand/voice_test.go` holds the status
lines to and `help`'s voice test holds every page to. skua adds two: no oxford commas,
and a wait or a limit is stated as it is, not rounded to sound nicer (`you're sending
too fast; try again in a couple of minutes`).

## Discord

- A reply is ephemeral unless being seen by the channel is the point, and every message
  carries `core.NoPings()`.
- An error a member reads is a `core.Tell`, sent as `✗ <text>`. Any other error is
  logged, and the member sees only `✗ something went wrong on skua's side; try again in
  a moment`. REST bodies and internal wording never reach Discord. To say something and
  keep the cause, wrap it: `fmt.Errorf("%w: %w", tell, err)` shows the `Tell` and logs
  the rest.
- A refusal is tested through `core.Router` with `coretest.Event`, checking the reply the
  member gets, not only the error the handler returns.
- An embed comes from `brand.Embed`, with its mood file attached. The title is one
  lowercase word.
- A page built from components (`/help`) is one Components V2 container, accented in
  a brand colour, wearing its mood icon (`brand.Icon`) as a section thumbnail, with any
  footer as `-#` subtext. It points at synced app emoji, attaching only what hasn't
  synced. Only the title and its `-#` line sit beside the thumbnail; the about goes
  below at full width, because beside a thumbnail a phone gives text a narrow column.
  The about says what the module does and how to use it before anything else.
- An ASCII glyph that leads a line (`├ └ ▸`) leads only a line short enough never to
  wrap: a wrapped line leaves its text hanging under the glyph on a phone. The `/help`
  index has no tree: each module's emoji leads its name line, with what it does as `-#`
  subtext below. A page's commands are facts, one line per subcommand:
  `**/purge now** admin`.
- Every other reply is a card (`brand.Card`, `/purge` is the reference): one
  Components V2 container, accented in the mood of what it says (ok when done, warn
  while working, error when it failed, info for a readout), whose first line is the
  module's emoji, its name in bold and what the reply is: `<emoji> **purge** · status`.
  Below it the content is text: a `✓`/`!` line, facts, `-#` detail, an ASCII bar
  (`▰▱`) or tree (`├ └`). An icon is an app emoji inline, never an attachment, so a
  card uploads nothing and before the emoji sync its head is text alone. Where
  DESIGN.md has every message wear the face and the rule, skua keeps a card free of
  uploads, so the rule waits until it can be shown without attaching it. A thumbnail
  or other asset is for a page that is about the thing it shows (`/help`'s), not for
  decoration. Buttons go inside the container, below the text. A live readout is
  edited as a card too (`discord.NewMessageUpdateV2`) and its accent follows its state.
  Errors stay `core.Tell`, plain text.
- A card that is a menu, a set of buttons that open screens (`/pp`'s status card is
  the reference), lays them out like an old game's menu:
  - Every button wears the app emoji of what it opens or does, so a row reads by
    picture first: on `/pp` the compass is quests, the laurel the board, the scales
    the shop, the quill the journal and the bell share, and class wears the class's
    own emblem. Its label is one or two lowercase words.
  - A row holds at most four buttons, so it never wraps on a desktop. The screens the
    card opens come first, then what the member does from it.
  - The one primary button is the thing the card is for (pick a class, climb a rung);
    the rest are secondary. A way back is `◂ <where>`, first in its row. A yes or no
    pair (approve, refuse) is coloured success and danger instead of wearing icons.
  - A card about one of several things (a shop item) is reached from a select on the
    overview and edits the same message in place, with `◂` back to it.
- An amount a member reads is in pp. Pot days are the economy's internal unit: a
  member never reads them, and only the admins' grant form takes them.
- A notify card is the one card accented by platform, not mood: its platform's
  muted colour from `brand.PlatformColor`, because a channel of them reads by
  platform. Its button and creator line wear the platform's tile, the glyph in the
  platform's colour lifted to read on slate (`platformInk` in `tools/sprites`). Its
  title is the card's heading (`###`), linked to the post or stream. Once its stream
  is over it turns `brand.ColorEnded`, one muted ember for every platform, so live
  reads apart from finished. A stream's card carries skua's picture of it: the
  platform's frame under a slate band holding the bird (128 px, one px a cell),
  the title in the 7 by 13 pixel face at 3 px a cell and who, how long and the
  audience at 2 px, on slate with the bird at 2 px a cell until there is a frame.
- A message skua posts as someone else (a bridge or a webhook) is their text. What
  skua adds to it, such as who it replies to, is a `-#` subtext line below it, never an
  embed: a webhook can't make a real Discord reply, and a box outweighs the message it
  annotates. When skua needs more than a line, it borrows `/help`'s colour identity and
  layout rather than inventing a new one.
- Facts are DESIGN.md's, one line each with the key in bold, written by
  `core.Readout`. Anything that is not a short fact goes on a line below them, detail as
  `-#` subtext. A code block holds only what somebody copies: `/pp`'s keys, a season's
  secret and its hash.
- Numbers follow DESIGN.md, with one decimal under 10 ms.
- Command and option descriptions: lowercase, no full stop, what it does in a few words.

## CLI

`tools/setup` prints on four columns:

```
▸ step title              ▸ in column 0, the title in 2
    ✓ result              marker in column 4, text in 6
      note                plain text in 6
✗ error                   ✗ in column 0, the text in 2
  continued               further lines of an error in 2
```

A blank line comes before every step and before the error. Use `step`, `ok`, `warn` and
`note`. The one bare `fmt.Print` is the token prompt, which has to leave the cursor on
its line. A value the tool did not ask for, such as a token
taken from the environment, is said out loud before it is used.

## Art

- Pixel art on an exact grid. Every cell is one flat colour and every cell is the same
  size: the avatar is 128 cells, so the profile picture is 8 px cells (1024) and the
  mood icons 2 px cells (256). The banner is 320 by 110 at 4 px (1280 by 440).
  `internal/brand` fails if an icon has a cell that is not flat.
- Resample by majority per cell (`cells`), never one sample per cell.
- Centre on the grid: the halo disc leaves 11 cells on every side, and the bird is
  centred in its icon to the nearest cell.
- A badge glyph is 6 by 6 with equal margins on opposite sides, so it sits one 8 px cell
  inside the badge's interior on every side. `tools/sprites` refuses a glyph that is
  not.
- Every module has an icon, the one `/help` and its cards wear: a pixelarticons glyph
  (MIT) traced onto the 24 cell grid in `tools/sprites/icons.go`, one cell per unit of
  the icon's 24 unit viewBox, with the source icon named beside it. A module ships
  with its glyph; `internal/brand` fails any module whose `Name()` has no
  `mod_<name>.png`.
- `/pp`'s item icons are 16 by 16 cell grids in `internal/pixel/icons.go`, in the style of
  merlin's art: a dark outline and each material lit from the top left in three or four
  tones. One grid is drawn on the game's screens at a pixel a cell and written as an
  application emoji at eight, so a button and a screen match. An icon is centred on its
  grid to the nearest cell (`pixel`'s tests hold it), and set beside text it starts two
  pixels above the line so the two share a middle.
- `/pp`'s screens are typeset in one family of four pixel faces (`internal/pixel/faces.go`),
  each with one job:
  - **bold:** tabs, headings and a word that leads a line.
  - **regular:** values and plain text.
  - **light:** a label and the quiet words beside a value.
  - **serif:** the story an item tells.

  Regular and bold are monospaced, so figures line up. Light and serif are set
  proportionally. A member's name is set in the look their rung of the rookery stair
  bought (`lookOf`), wherever others read it.
- An item's card reads like an old game's item screen: a plain line on what it is, its
  story with only a hint of poetry, and an `effect` line saying exactly what it does in
  the game.
- `/pp`'s screens are windows on a 384 by 216 canvas. A window's frame is never broken:
  its tab sits in the corner inside the frame, the dark rule thickening into it.
  Text on a screen is bounded by the room it has, measured and not assumed: a line
  gives up words to `shortForms` first (`raven sorcerer` over `hedge sorcerer` reads
  `raven sorc` over `hedge sorc`), then tightens a cell, and only then is cut with
  `...`. Longer text (an item's lore) wraps between words. `layout_test` holds every
  screen to it: nothing overlaps anything and nothing leaves a window's parchment,
  every mark at least a pixel inside it, so a new screen comes with its fixtures
  there.
- Edit `art/source` or `tools/sprites` and rerun the generator, never the output PNGs.

## performance readout

`/perf` uses a Components V2 card with an aligned, copyable code-block timing
table. Columns keep p50, p99, targets and counts aligned when the timing report is
copied into an issue or compared with another run. A `!` marks a
missed p99 target; those rows lead, then the slowest remaining stages.
