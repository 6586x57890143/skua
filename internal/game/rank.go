package game

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

// Standing is one player on a board.
type Standing[K cmp.Ordered] struct {
	Key   K
	Score int64
	Place int
}

// Rank is every player with a score above zero, best first, with standard
// competition places (1, 2, 2, 4), ties in key order.
func Rank[K cmp.Ordered](scores map[K]int64) []Standing[K] {
	out := make([]Standing[K], 0, len(scores))
	for k, s := range scores {
		if s > 0 {
			out = append(out, Standing[K]{Key: k, Score: s})
		}
	}
	slices.SortFunc(out, func(a, b Standing[K]) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(a.Key, b.Key))
	})
	for i := range out {
		if i > 0 && out[i].Score == out[i-1].Score {
			out[i].Place = out[i-1].Place
		} else {
			out[i].Place = i + 1
		}
	}
	return out
}

// Top is a board's first n rows, the player's own line in the last place
// when they're further down.
func Top[K cmp.Ordered](board []Standing[K], me K, n int) []Standing[K] {
	if len(board) <= n {
		return board
	}
	out := slices.Clone(board[:n])
	in := func(s Standing[K]) bool { return s.Key == me }
	if n > 0 && !slices.ContainsFunc(out, in) {
		if i := slices.IndexFunc(board, in); i >= 0 {
			out[n-1] = board[i]
		}
	}
	return out
}

// Month is the UTC calendar month holding t, as its start and the next
// month's start, with its key ("2026-11").
func Month(t time.Time) (start, end time.Time, key string) {
	t = t.UTC()
	start = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0), start.Format("2006-01")
}

// Week is the ISO week holding t, Monday 00:00 UTC to the next, with its
// key ("2026-W48").
func Week(t time.Time) (start, end time.Time, key string) {
	t = t.UTC()
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	start = day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	y, w := start.ISOWeek()
	return start, start.AddDate(0, 0, 7), fmt.Sprintf("%d-W%02d", y, w)
}
