package pp

// Forays (LORE.md, beyond the north gate): a bird goes out by the north
// gate to one place, the near or the far reaches, leaning into one of the
// five winds, and comes back with the day written down. This file is what
// can be written: the winds, and each place's events, the class arcs met
// there and its endings. The places themselves are foray_mire.go and
// foray_wood.go. Every word is LORE.md's; check a new fact against it and
// write it there first.
//
// A day out is a few events drawn from the place's deck, the same draw for
// every bird out there that day, with the bird's own class arc among them.
// At each event the bird takes one option: its class's own if the event has
// one, else the one its lean prefers (Wind.prefers). An option with a need
// is thrown for on the bones, one to a hundred, and passes at the need or
// over; one without always passes. What it found adds up, and the marks it
// left pick a rare ending. Otherwise the day ends on its lean's ending, a
// good one when at least half its throws passed.
//
// Passages are second person and present, the way a book of the kind is
// read, and every one ends a thought: a bird reads them a slice at a time.

// Wind is one of the five winds (LORE.md, the five winds): the outlook a
// bird leans into, and on a foray the way it goes at every fork.
type Wind int

const (
	Writ Wind = iota
	Vaunt
	Lot
	Stray
	Fold
)

// winds are each wind's name and the line the gate shows for leaning into
// it, in the ring's order: each is between the two it gets on with.
var winds = [5]struct{ Key, Line string }{
	{"writ", "keeps to the marked ways and hands its finds in at the desk"},
	{"vaunt", "goes for whatever will be talked about at the pot"},
	{"lot", "takes the chance"},
	{"stray", "goes off the path"},
	{"fold", "looks for company out there"},
}

func (w Wind) String() string { return winds[w].Key }

// prefers is the order a bird leaning w takes options in: w itself, then
// the two it gets on with, then the two it can't stand, the nearer first
// round the ring each time.
func (w Wind) prefers() [5]Wind {
	return [5]Wind{w, (w + 1) % 5, (w + 4) % 5, (w + 2) % 5, (w + 3) % 5}
}

// Place is somewhere a bird can go out to.
type Place struct {
	Key, Name string
	// Find is what the place gives, one and many: peat, amber.
	Find, Finds string
	// Home are the classes that read this country, whose bones count
	// homeBonus more here.
	Home []string
	// Out is the first passage of a day there, near and far: the road from
	// the gate. In is the last before the ending, the road back.
	Out, In [2]string
	// Events are the place's deck. Far ones are only met in the far
	// reaches.
	Events []Event
	// Arcs are each class's own story here, a stage a visit: the bird meets
	// its next stage every time it comes, and a stage moves on only when its
	// throw passes, so a failed one is met again next time. Once the third
	// is told the bird has the arc's epilogue (foray_pages.go).
	Arcs map[string][3]Event
	// Company is the event two fold birds out at the same place on the same
	// day meet each other in; {name} is the other bird.
	Company Event
	// Endings close a day: a good and a hard one for each wind, and the
	// rare ones a mark calls for.
	Endings []Ending
}

// homeBonus is what the bones count more for a bird whose class reads the
// country.
const homeBonus = 15

// Event is one thing met out there.
type Event struct {
	Key string
	Far bool
	// Text is what the bird comes to, and Class a sentence more for a bird
	// of that class, put after it.
	Text  string
	Class map[string]string
	// Options are what the bird can do. A bird takes the class option if
	// there is one for it, else the first its lean prefers.
	Options []Option
}

// Option is one thing a bird can do at an event.
type Option struct {
	Wind Wind
	// Class is the only class that takes this option; it takes it over any
	// lean. Empty is open to any bird.
	Class string
	// After is a mark the bird must already carry for this option to be
	// open to it.
	After string
	// Try is what the bird does, as the screen shows it beside its odds.
	Try string
	// Need is what the bones must reach, 0 for no throw.
	Need       int
	Pass, Fail Outcome
}

// Outcome is how an option went.
type Outcome struct {
	Text string
	// Finds is how many of the place's find the bird gains.
	Finds int
	// Mark is a mark the bird carries from here, for a later option's After
	// or a rare ending.
	Mark string
}

// Ending closes a day.
type Ending struct {
	Key, Title, Text string
	Wind             Wind
	// Good is the day that went well; a hard one is the other.
	Good bool
	// Mark makes this a rare ending, taken over the lean's whenever the
	// bird carries the mark.
	Mark string
	// Finds is what the ending adds.
	Finds int
}

// places are the forays, in the order the gate offers them.
var places = []*Place{&mire, &wood}
