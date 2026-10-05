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
  footer as `-#` subtext. It uploads exactly the files it points at.
- Facts go in a code block grid (`status.readout`): the label column is the longest
  label plus two spaces, a value wraps after a comma inside 28 columns, and a whole line
  stays within 40. Anything that is not a short fact goes on a line below the block,
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
- Edit `art/source` or `tools/sprites` and rerun the generator, never the output PNGs.
