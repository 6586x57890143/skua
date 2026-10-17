package pp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Every roll /pp makes (a day's pool, its idle pace, what a message pays)
// is drawn from its season's seed, and the seed is committed before the
// season starts, so nobody can pick the draws, skua's operator included:
//
//   - a season ahead, skua makes a secret and posts sha256(secret);
//   - a day before the season, drand's public beacon draws a number nobody
//     could know when the secret was posted;
//   - the season's seed is sha256(secret || beacon);
//   - once the season is over, skua posts the secret, and anyone can check
//     it against what was posted and draw every roll again with Roll and
//     Balance.Pools.
//
// A season that started before its secret could be posted in time draws
// from the seed skua was started with, and can't be drawn again.

// drand's quicknet: a beacon every 3 seconds since its genesis.
const (
	drandGenesis = 1692803367
	drandPeriod  = 3
)

// drandURL is where a round's beacon is fetched, a variable for tests.
var drandURL = "https://api.drand.sh/52db9ba70e0cc0f6eaf7803dd07447a1f5477735fd3f661792ba94600c84e971/public/"

// beaconLead is how long before a season starts its beacon is drawn.
const beaconLead = 24 * time.Hour

// seasonSeed is one season's committed seed.
type seasonSeed struct {
	season     int64
	secret     []byte
	round      int64
	randomness []byte // nil until drawn, empty if drand never answered
	revealed   bool
	seed       uint64 // once randomness is settled
	settled    bool
	dirty      bool // randomness or revealed changed since it was saved
}

// commitment is what is posted before the season: sha256(secret).
func (ss *seasonSeed) commitment() string {
	sum := sha256.Sum256(ss.secret)
	return hex.EncodeToString(sum[:])
}

// settle fixes the seed from the secret and whatever randomness there is.
// From the first roll on it can't change. st.mu held.
func (ss *seasonSeed) settle() uint64 {
	if !ss.settled {
		if ss.randomness == nil {
			ss.randomness, ss.dirty = []byte{}, true
		}
		sum := sha256.Sum256(append(append([]byte{}, ss.secret...), ss.randomness...))
		ss.seed, ss.settled = binary.BigEndian.Uint64(sum[:8]), true
	}
	return ss.seed
}

// seedOf is the seed day's rolls draw from.
func (st *state) seedOf(day int64) uint64 {
	season, _ := st.b.Season(day)
	st.mu.Lock()
	defer st.mu.Unlock()
	if ss, ok := st.seeds[season]; ok {
		return ss.settle()
	}
	return st.seed
}

// roundAt is drand's round at t, the first one drawn at or after it.
func roundAt(t time.Time) int64 {
	since := t.Unix() - drandGenesis
	return (since+drandPeriod-1)/drandPeriod + 1
}

// roundTime is when drand draws round r.
func roundTime(r int64) time.Time { return time.Unix(drandGenesis+(r-1)*drandPeriod, 0) }

// seasonStart is when season begins.
func (b *Balance) seasonStart(season int64) time.Time {
	return time.Unix((b.SeasonStart+(season-1)*7*b.SeasonWeeks)*86400, 0)
}

// Beacon fetches drand's randomness for a round.
type Beacon func(ctx context.Context, round int64) ([]byte, error)

// drand is the Beacon from drand's public API.
func drand(ctx context.Context, round int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprint(drandURL, round), nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("drand: round %d: %s", round, res.Status)
	}
	var body struct {
		Round      int64  `json:"round"`
		Randomness string `json:"randomness"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Round != round {
		return nil, fmt.Errorf("drand: asked for round %d, got %d", round, body.Round)
	}
	return hex.DecodeString(body.Randomness)
}

// loadSeeds reads every season's seed.
func (st *state) loadSeeds(ctx context.Context, db DB) error {
	rows, err := db.Query(ctx, `select season, secret, round, randomness, revealed from pp_season`)
	if err != nil {
		return err
	}
	defer rows.Close()
	st.mu.Lock()
	defer st.mu.Unlock()
	for rows.Next() {
		ss := &seasonSeed{}
		if err := rows.Scan(&ss.season, &ss.secret, &ss.round, &ss.randomness, &ss.revealed); err != nil {
			return err
		}
		st.seeds[ss.season] = ss
	}
	return rows.Err()
}

// seasonNews is what a tick of the seeds has to tell the servers.
type seasonNews struct {
	committed, revealed []*seasonSeed
}

// tickSeeds keeps the seeds moving: it commits to the next season's secret
// while there is still time to before its beacon, draws a beacon once it is
// out, and reveals the secret of every season that has ended. What it
// commits and reveals comes back to be posted.
func (st *state) tickSeeds(ctx context.Context, db DB, beacon Beacon, now time.Time) (seasonNews, error) {
	var news seasonNews
	season, _ := st.b.Season(DayOf(now))
	next := season + 1
	st.mu.Lock()
	_, have := st.seeds[next]
	st.mu.Unlock()
	if at := st.b.seasonStart(next).Add(-beaconLead); !have && at.After(now) {
		ss := &seasonSeed{season: next, secret: make([]byte, 32), round: roundAt(at)}
		rand.Read(ss.secret)
		if _, err := db.Exec(ctx, `insert into pp_season (season, secret, round) values ($1, $2, $3) on conflict do nothing`,
			ss.season, ss.secret, ss.round); err != nil {
			return news, err
		}
		st.mu.Lock()
		st.seeds[next] = ss
		st.mu.Unlock()
		news.committed = append(news.committed, ss)
	}

	st.mu.Lock()
	var draw []*seasonSeed
	for _, ss := range st.seeds {
		if ss.randomness == nil && !roundTime(ss.round).After(now) {
			draw = append(draw, ss)
		}
		if ss.season < season && !ss.revealed {
			ss.settle()
			ss.revealed, ss.dirty = true, true
			news.revealed = append(news.revealed, ss)
		}
	}
	st.mu.Unlock()
	var failed error
	for _, ss := range draw {
		r, err := beacon(ctx, ss.round)
		if err != nil {
			failed = err
			continue
		}
		st.mu.Lock()
		if !ss.settled {
			ss.randomness, ss.dirty = r, true
		}
		st.mu.Unlock()
	}
	if err := st.saveSeeds(ctx, db); err != nil {
		return news, err
	}
	return news, failed
}

// saveSeeds writes the seeds that changed.
func (st *state) saveSeeds(ctx context.Context, db DB) error {
	st.mu.Lock()
	var changed []seasonSeed
	for _, ss := range st.seeds {
		if ss.dirty {
			changed = append(changed, *ss)
			ss.dirty = false
		}
	}
	st.mu.Unlock()
	for i, ss := range changed {
		if _, err := db.Exec(ctx, `update pp_season set randomness = $2, revealed = $3 where season = $1`,
			ss.season, ss.randomness, ss.revealed); err != nil {
			st.mu.Lock()
			for _, c := range changed[i:] {
				st.seeds[c.season].dirty = true
			}
			st.mu.Unlock()
			return err
		}
	}
	return nil
}
