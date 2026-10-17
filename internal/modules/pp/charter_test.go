package pp

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// charterHashes pins every charter version's rules. A change to Rules fails
// here until it has a new Version, its hash pinned below and a changelog
// line saying what changed.
var charterHashes = map[string]string{
	"1": "4c203cc5694c",
	"2": "ff8920b9861d",
}

func TestCharterPinned(t *testing.T) {
	got := fmt.Sprintf("%x", sha256.Sum256(fmt.Appendf(nil, "%#v", Rules)))[:12]
	if want, ok := charterHashes[Rules.Version]; !ok || want != got {
		t.Fatalf("charter %s hashes to %s, pinned as %q: a change to the rules needs a new version and a changelog line", Rules.Version, got, want)
	}
}

// The levy takes its share until the treasury holds TreasuryWeeks of what a
// week's levy comes to, then stops, and starts again as it's spent.
func TestLevyPauses(t *testing.T) {
	s := newServer(guild)
	if !s.levying() {
		t.Fatal("a new treasury isn't taking")
	}
	s.levyDays = []int64{100, 100, 100, 100, 100, 100, 100}
	s.treasury = Rules.TreasuryWeeks*700 - 1
	if !s.levying() {
		t.Error("a treasury short of its weeks stopped taking")
	}
	s.treasury++
	if s.levying() {
		t.Error("a full treasury kept taking")
	}

	// Earning into a full treasury pays the member it all.
	st := newState(&Default, 42)
	srv := st.server(guild)
	srv.levyDays, srv.treasury = s.levyDays, s.treasury
	st.chat(srv, guild, old(1), "a full treasury takes nothing", t0)
	if m := srv.members[old(1)]; m.dayLevy != 0 || m.purse != m.total || srv.treasury != s.treasury {
		t.Errorf("a full treasury took %d", m.dayLevy)
	}
}

// The pot day is a core member's median day and a quest.
func TestPotDay(t *testing.T) {
	if pd := Default.PotDay(Rules); pd/unit < 300 || pd/unit > 600 {
		t.Errorf("a pot day is %d pp", pd/unit)
	}
	if ev := Default.QuestEV(); ev <= Default.QuestPP[0] || ev >= Default.QuestPP[2] {
		t.Errorf("a quest is worth %d on average", ev)
	}
}
