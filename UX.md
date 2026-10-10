# UX standard

Every surface skua has follows this: replies, embeds, webhook posts, command names and
descriptions, the setup CLI, and the art. A change to a surface is checked against it,
and a layout claim is checked by measuring or by a test, never by eye.

## Voice

skua is a quiet, clever bird who knows more than she says and is casual about it, a
friend of peregrine and merlin (merlin's brief, `merlin/internal/voice/PERSONA.md`, is the
shared one). She talks the way a person types in a server: natural sentences, not
clipped fragments, not a character performing. The tells to avoid are the ones that
read as generated: a tag hung off a comma for effect (`takes back what you said, all of
it`), dramatic two-word fragments (`coffee's cold. trail's warm`), and chains of
semicolons. Errors stay plain: say what happened and what to do, with no wit.

The rules every bird shares, which `brand/voice_test.go` holds the status lines to and
`help`'s voice test holds every page to:

- All lowercase. The exceptions are names that have to be typed exactly as written:
  environment variables (`DISCORD_BOT_TOKEN`), flags, URLs and code.
- No closing full stop. Clauses join with `; `, or with `:` when the second explains the
  first: `slowmode is on: you can send again in 20s`.
- Say what happened, then what to do: `you're sending too fast; try again in a couple of
  minutes`. A wait or a limit is stated as it is, not rounded to sound nicer.
- Plain words and contractions. No oxford commas, no exclamation marks, no emoji. The
  marks are `✓` done, `!` warning, `✗` failed, `▸` step, `·` between facts on one line.
- The prose check applies: no em dashes, ellipsis characters or curly quotes.

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
  wrap, or one inside a code block: a wrapped line leaves its text hanging under the
  glyph on a phone. The `/help` index has no tree: each module's emoji leads its name
  line, with what it does as `-#` subtext below. Its command grid branches subcommands
  inside the code block.
- Every other reply is a card (`brand.Card`, `/purge` is the reference): one
  Components V2 container, accented in the mood of what it says (ok when done, warn
  while working, error when it failed, info for a readout), whose first line is the
  module's emoji, its name in bold and what the reply is: `<emoji> **purge** · status`.
  Below it the content is text: a `✓`/`!` line, a code block grid, `-#` detail, an ASCII
  bar (`▰▱`) or tree (`├ └`). An icon is an app emoji inline, never an attachment, so a
  card uploads nothing and before the emoji sync its head is text alone. A thumbnail
  or other asset is for a page that is about the thing it shows (`/help`'s), not for
  decoration. Buttons go inside the container, below the text. A live readout is
  edited as a card too (`discord.NewMessageUpdateV2`) and its accent follows its state.
  Errors stay `core.Tell`, plain text.
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
- Facts go in a code block grid (`status.readout`): the label column is the longest
  label plus two spaces, a value wraps after a comma inside 28 columns, and a whole line
  stays within 40. A 360 px phone fits only about 34 columns of code block, so a grid
  read on phones keeps to 32 (`/help`'s command grid does). Anything that is not a short fact goes on a line below the block,
  detail as `-#` subtext. A code block, because monospace lines up the same in every
  client, where how embed fields wrap is up to the client.
- Numbers: `42 ms` with a space, one decimal under 10 ms. Durations use the two largest
  units: `45s`, `12m`, `3h 12m`, `2d 4h`. A value not measured yet says so, never `0`.
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
- Edit `art/source` or `tools/sprites` and rerun the generator, never the output PNGs.
