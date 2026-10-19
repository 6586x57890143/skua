package sanction

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

func TestLadder(t *testing.T) {
	l := Ladder{Base: 10 * time.Minute, Max: 24 * time.Hour, Doublings: 7}
	want := []time.Duration{10 * time.Minute, 20 * time.Minute, 40 * time.Minute, 80 * time.Minute, 160 * time.Minute, 320 * time.Minute, 640 * time.Minute, 1280 * time.Minute, 1280 * time.Minute}
	for priors, w := range want {
		if got := l.For(priors); got != w {
			t.Errorf("For(%d) = %v, want %v", priors, got, w)
		}
	}
	if l.For(-1) != 10*time.Minute {
		t.Fatal("negative priors")
	}
	if (Ladder{Base: time.Hour, Max: 90 * time.Minute, Doublings: 3}).For(3) != 90*time.Minute {
		t.Fatal("past Max")
	}
}

type history struct {
	priors     int
	readErr    error
	writeErr   error
	recorded   []Sentence
	askedSince time.Time
}

func (h *history) Priors(_ context.Context, _, _ snowflake.ID, since time.Time) (int, error) {
	h.askedSince = since
	return h.priors, h.readErr
}

func (h *history) Record(_ context.Context, s Sentence) error {
	h.recorded = append(h.recorded, s)
	return h.writeErr
}

type fake struct {
	name string
	err  error
	got  []Sentence
}

func (f *fake) Name() string { return f.name }
func (f *fake) Apply(_ context.Context, s Sentence) (string, error) {
	f.got = append(f.got, s)
	if f.err != nil {
		return "said anyway", f.err
	}
	return f.name + " for " + s.For.String(), nil
}

var (
	at     = time.Date(2026, 10, 19, 12, 18, 0, 0, time.UTC)
	ladder = Ladder{Base: 10 * time.Minute, Max: 24 * time.Hour, Doublings: 7, Window: 7 * 24 * time.Hour}
	o      = Offence{Guild: 3, User: 7, Channel: 4, Rule: "spam", At: at}
)

func TestJudgeClimbsTheLadderAndRecordsFirst(t *testing.T) {
	h := &history{priors: 2}
	bird := &fake{name: "bird"}
	v := Judge(context.Background(), h, ladder, o, bird)
	if v.Err != nil || v.By != "bird" || v.For != 40*time.Minute || v.Priors != 2 || v.Said != "bird for 40m0s" {
		t.Fatalf("verdict %+v", v)
	}
	if !h.askedSince.Equal(at.Add(-ladder.Window)) || len(h.recorded) != 1 || h.recorded[0].For != 40*time.Minute {
		t.Fatalf("history asked since %v, recorded %+v", h.askedSince, h.recorded)
	}
}

func TestJudgeLeansLenientAndStillRecords(t *testing.T) {
	h := &history{priors: 5, readErr: errors.New("db down"), writeErr: errors.New("db down")}
	bird := &fake{name: "bird", err: errors.New("discord away")}
	v := Judge(context.Background(), h, ladder, o, bird)
	if v.For != 10*time.Minute {
		t.Fatalf("an unreadable record sentenced %v", v.For)
	}
	if len(h.recorded) != 1 || v.Err == nil || v.By != "bird" || v.Said != "" {
		t.Fatalf("verdict %+v, recorded %d", v, len(h.recorded))
	}
	if v := Judge(context.Background(), nil, ladder, o, &fake{name: "bird"}); v.Err != nil || v.For != 10*time.Minute {
		t.Fatalf("no history: %+v", v)
	}
}

func TestJudgeFallsBack(t *testing.T) {
	jail := &fake{name: "jail", err: ErrUnavailable}
	timeout := &fake{name: "timeout"}
	v := Judge(context.Background(), nil, ladder, o, jail, timeout)
	if v.By != "timeout" || v.Err != nil || len(jail.got) != 1 || len(timeout.got) != 1 {
		t.Fatalf("verdict %+v", v)
	}
	// A real failure stops the line: a jail that broke is not a reason to
	// time them out too.
	jail.err = errors.New("role above skua")
	timeout.got = nil
	if v := Judge(context.Background(), nil, ladder, o, jail, timeout); v.By != "jail" || v.Err == nil || len(timeout.got) != 0 {
		t.Fatalf("verdict %+v", v)
	}
	if v := Judge(context.Background(), nil, ladder, o); !errors.Is(v.Err, ErrUnavailable) || v.By != "" {
		t.Fatalf("no sanctions: %+v", v)
	}
}
