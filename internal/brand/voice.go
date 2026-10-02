package brand

// Status is skua's line in the member list, one per re-probe tick, round
// robin so the same line never shows twice running. The voice: a noir
// detective by day, quietly the one who finds the open doors and tells you
// before anyone else does. Lowercase, plain punctuation, said once and left
// there. voice_test holds every line to it.
var Status = []string{
	"working the night shift",
	"watching the ports",
	"reading the logs by lamplight",
	"nobody saw me come in",
	"someone left a door open again",
	"checking the locks. all of them",
	"coffee's cold. trail's warm",
	"same city, different password",
	"fixed it. left a note",
	"off the record",
	"i only take the cases nobody else will",
	"left everything how i found it",
}

// StatusAt is the line for tick n.
func StatusAt(n int) string { return Status[n%len(Status)] }
