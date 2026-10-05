package brand

// Status is skua's line in the member list, one per re-probe tick, round
// robin so the same line never shows twice running. The voice is UX.md's:
// a quiet, clever bird who knows more than she says, casual about it, and a
// friend of peregrine and merlin. Something she'd actually say, not a line
// written to sound like a character. voice_test holds every line to it.
var Status = []string{
	"keeping an eye on things",
	"reading along quietly",
	"somewhere above the server",
	"around if you need me",
	"listening more than talking",
	"out flying with peregrine",
	"merlin says hi",
	"on the high perch tonight",
	"taking the long way round",
	"just passing through",
	"knows more than she lets on",
	"back before you notice",
}

// StatusAt is the line for tick n.
func StatusAt(n int) string { return Status[n%len(Status)] }
