package pp

import (
	"bytes"
	"testing"
	"time"

	"github.com/6586x57890143/skua/internal/pixel"
)

// Every light and every stage of the bell moves, and the hour and the
// hold put the sump where the lore says.
func TestSump(t *testing.T) {
	for h, want := range map[int]int{6: dawn, 12: daylight, 19: dusk, 23: night, 2: night} {
		if got := lightAt(time.Date(2026, 11, 2, h, 0, 0, 0, time.UTC)); got != want {
			t.Errorf("at %d:00 the light is %d, want %d", h, got, want)
		}
	}
	line := houseLine()
	low, half := Sump{Hold: 0, Line: line}, Sump{Hold: line * 3 / 4, Line: line}
	if low.lift() >= half.lift() || low.tide() != tideUsual {
		t.Error("the tide doesn't come further up the shingle as the hold fills")
	}
	for s, want := range map[Sump]int{
		{Hold: line, Line: line}: tideHigh, {Stage: bellDrawing, Line: line}: tideLow,
		{Stage: bellBoats, Line: line}: tideLow, {Stage: bellRinging, Line: line}: tideHigh,
	} {
		if s.tide() != want || s.lift() != 0 {
			t.Errorf("%+v is painted at tide %d, want %d as painted", s, s.tide(), want)
		}
	}
	// High water stands well up the shingle, low water off it.
	mean := func(e []int) int {
		n := 0
		for x := shingle[0]; x < shingle[1]; x++ {
			n += e[x]
		}
		return n / (shingle[1] - shingle[0])
	}
	high, usual, lowWater := mean(shores[tideHigh]()), mean(shores[tideUsual]()), mean(shores[tideLow]())
	if high >= usual-8 || lowWater <= usual {
		t.Errorf("waterlines high %d, usual %d, low %d", mean(shores[tideHigh]()), mean(shores[tideUsual]()), mean(shores[tideLow]()))
	}
	if !(Sump{Stage: bellBoats}).boatsUp() || (Sump{}).boatsUp() || !(Sump{Stage: bellRinging}).leaning() {
		t.Error("the boats and the candles don't follow the bell")
	}
	for _, s := range []Sump{
		{Line: line, Light: dusk}, {Line: line, Light: night}, {Line: line, Light: dawn, Pumping: true},
		{Line: line, Light: daylight, Hold: line}, {Line: line, Stage: bellDrawing}, {Line: line, Stage: bellBoats}, {Line: line, Stage: bellRinging},
	} {
		frames := sumpScene(pixel.New(sumpSize.X, sumpSize.Y), s)
		if len(frames) < 2*ambientSteps || bytes.Equal(frames[0].Pix, frames[1].Pix) {
			t.Errorf("%+v doesn't move", s)
		}
	}
}
