package pp

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A season's seed is committed before its beacon is drawn, drawn after,
// fixed from the first roll and revealed once the season is over; and
// anyone with the secret and beacon can draw its rolls again.
func TestSeasonSeeds(t *testing.T) {
	db := &fakeDB{}
	st := newState(&Default, 42)
	beacon := []byte("drand says")
	asked := 0
	draw := func(_ context.Context, round int64) ([]byte, error) {
		asked++
		return beacon, nil
	}
	two := Default.seasonStart(2)

	// Early in season 1, season 2 is committed and season 1 draws from the
	// seed skua started with.
	now := Default.seasonStart(1).Add(48 * time.Hour)
	news, err := st.tickSeeds(context.Background(), db, draw, now)
	if err != nil || len(news.committed) != 1 || news.committed[0].season != 2 {
		t.Fatalf("committed %+v, %v", news.committed, err)
	}
	ss := st.seeds[2]
	if roundTime(ss.round).Before(two.Add(-beaconLead)) || ss.randomness != nil || asked != 0 {
		t.Fatalf("season 2's beacon is round %d at %v, drawn %v", ss.round, roundTime(ss.round), ss.randomness)
	}
	if st.seedOf(DayOf(now)) != 42 {
		t.Error("season 1 has a seed it never committed to")
	}
	if news, _ := st.tickSeeds(context.Background(), db, draw, now.Add(time.Hour)); len(news.committed) != 0 {
		t.Error("a season was committed twice")
	}

	// Once its round is out, the beacon is drawn, and the seed is the
	// secret and the beacon together.
	if _, err := st.tickSeeds(context.Background(), db, draw, roundTime(ss.round)); err != nil || asked != 1 {
		t.Fatalf("drawing: %v, asked %d", err, asked)
	}
	sum := sha256.Sum256(append(append([]byte{}, ss.secret...), beacon...))
	if got := st.seedOf(DayOf(two)); got != binary.BigEndian.Uint64(sum[:8]) {
		t.Errorf("season 2 draws from %x", got)
	}
	day := DayOf(two)
	if Roll(st.seedOf(day), 3, 5, day, 0) != Roll(binary.BigEndian.Uint64(sum[:8]), 3, 5, day, 0) {
		t.Error("a roll can't be drawn again from the revealed seed")
	}

	// When season 2 is over its secret comes out, and only once.
	news, _ = st.tickSeeds(context.Background(), db, draw, Default.seasonStart(3).Add(time.Hour))
	if len(news.revealed) != 1 || news.revealed[0].season != 2 || !ss.revealed {
		t.Fatalf("revealed %+v", news.revealed)
	}
	if fmt.Sprintf("%x", sha256.Sum256(ss.secret)) != ss.commitment() {
		t.Error("the secret doesn't match what was committed")
	}
	if news, _ := st.tickSeeds(context.Background(), db, draw, Default.seasonStart(3).Add(2*time.Hour)); len(news.revealed) != 0 {
		t.Error("a season was revealed twice")
	}
	if len(db.execs) == 0 || !strings.Contains(strings.Join(db.execs, " "), "pp_season") {
		t.Errorf("nothing was saved: %v", db.execs)
	}
}

// A season rolled before drand answered is fixed without it, and a beacon
// coming in late can't change a seed already in use.
func TestSeedFixedAtFirstRoll(t *testing.T) {
	st := newState(&Default, 42)
	ss := &seasonSeed{season: 2, secret: []byte("s"), round: 1}
	st.seeds[2] = ss
	first := st.seedOf(DayOf(Default.seasonStart(2)))
	if ss.randomness == nil || len(ss.randomness) != 0 {
		t.Fatal("a roll before the beacon left the seed open")
	}
	failing := func(context.Context, int64) ([]byte, error) { return nil, errors.New("down") }
	if _, err := st.tickSeeds(context.Background(), &fakeDB{}, failing, Default.seasonStart(2)); err != nil {
		t.Fatalf("a settled seed asked drand: %v", err)
	}
	if st.seedOf(DayOf(Default.seasonStart(2))) != first {
		t.Error("the seed moved")
	}

	// A season too close to commit in time is left to the start seed, and a
	// failing beacon is reported but the rest still saves.
	late := newState(&Default, 42)
	if news, _ := late.tickSeeds(context.Background(), &fakeDB{}, failing, Default.seasonStart(2).Add(-time.Hour)); len(news.committed) != 0 {
		t.Error("a season was committed after its beacon was out")
	}
	late.seeds[3] = &seasonSeed{season: 3, secret: []byte("s"), round: 1}
	if _, err := late.tickSeeds(context.Background(), &fakeDB{}, failing, Default.seasonStart(2)); err == nil {
		t.Error("a failing beacon went unreported")
	}
	if _, err := late.tickSeeds(context.Background(), &fakeDB{fail: errors.New("down")}, failing, Default.seasonStart(2).Add(-30*24*time.Hour)); err == nil {
		t.Error("a commit that couldn't be saved went unreported")
	}
}

func TestRounds(t *testing.T) {
	for _, r := range []int64{1, 2, 1000, 25_000_000} {
		if got := roundAt(roundTime(r)); got != r {
			t.Errorf("round %d comes back as %d", r, got)
		}
	}
	if at := time.Unix(drandGenesis+4, 0); roundTime(roundAt(at)).Before(at) {
		t.Error("roundAt picks a round drawn before the time")
	}
}

// drand's API answers a round with its randomness in hex.
func TestDrand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/1000":
			fmt.Fprint(w, `{"round":1000,"randomness":"fe29","signature":"b4"}`)
		case "/7":
			fmt.Fprint(w, `{"round":8,"randomness":"00"}`)
		case "/9":
			fmt.Fprint(w, `not json`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	was := drandURL
	drandURL = srv.URL + "/"
	defer func() { drandURL = was }()
	ctx := context.Background()
	if r, err := drand(ctx, 1000); err != nil || fmt.Sprintf("%x", r) != "fe29" {
		t.Errorf("round 1000: %x, %v", r, err)
	}
	for _, round := range []int64{7, 9, 404} {
		if _, err := drand(ctx, round); err == nil {
			t.Errorf("round %d came back fine", round)
		}
	}
}

// Against a real database the seeds come back as they were saved.
func TestSeedsRoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, `delete from pp_season`); err != nil {
		t.Fatal(err)
	}
	st := newState(&Default, 42)
	draw := func(context.Context, int64) ([]byte, error) { return []byte{1, 2}, nil }
	if _, err := st.tickSeeds(ctx, db, draw, Default.seasonStart(1).Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.tickSeeds(ctx, db, draw, roundTime(st.seeds[2].round)); err != nil {
		t.Fatal(err)
	}
	back := newState(&Default, 42)
	if err := back.loadSeeds(ctx, db); err != nil {
		t.Fatal(err)
	}
	if ss := back.seeds[2]; ss == nil || string(ss.secret) != string(st.seeds[2].secret) || len(ss.randomness) != 2 {
		t.Fatalf("season 2 came back as %+v", ss)
	}
}
