package pp

// Class is a path a member walks: a plain low fantasy job, worn by a real
// bird (never one of the fleet's own: cygnet, merlin, peregrine, skua), with a title for every stretch of levels the way the old tabletop
// classes had. Everyone starts a wanderer. In beta a class is who you are,
// never what you earn.
type Class struct {
	Key, Bird string
	// Titles are worn from the levels in titleFrom on.
	Titles [6]string
}

// titleFrom is the level each title starts at.
var titleFrom = [6]int{0, 5, 10, 20, 30, 50}

// Classes is every path, the wanderer first.
var Classes = []Class{
	{"wanderer", "albatross", [6]string{"drifter", "wayfarer", "roamer", "far-walker", "longwing", "storm wanderer"}},
	{"fighter", "shrike", [6]string{"recruit", "sellsword", "veteran", "swordmaster", "champion", "warlord"}},
	{"ranger", "goshawk", [6]string{"runner", "scout", "tracker", "pathfinder", "warden", "high warden"}},
	{"sorcerer", "raven", [6]string{"hedge sorcerer", "conjurer", "thaumaturge", "warlock", "magus", "archmagus"}},
	{"thief", "magpie", [6]string{"footpad", "cutpurse", "burglar", "sharper", "magsman", "master thief"}},
	{"bard", "nightingale", [6]string{"rhymer", "lyrist", "skald", "troubadour", "minstrel", "master bard"}},
	{"monk", "heron", [6]string{"novice", "initiate", "disciple", "master", "grandmaster", "still water"}},
	{"knight", "crane", [6]string{"squire", "knight", "knight errant", "banneret", "lord commander", "crane lord"}},
}

// ClassOf is the class key names, the wanderer for one it doesn't know.
func ClassOf(key string) Class {
	for _, c := range Classes {
		if c.Key == key {
			return c
		}
	}
	return Classes[0]
}

// Name is how a member of c reads: the bird, then the job.
func (c Class) Name() string { return c.Bird + " " + c.Key }

// Title is what c calls someone at level l.
func (c Class) Title(l int) string {
	t := c.Titles[0]
	for i, from := range titleFrom {
		if l >= from {
			t = c.Titles[i]
		}
	}
	return t
}
