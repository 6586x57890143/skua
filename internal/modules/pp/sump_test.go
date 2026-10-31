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
	out, in := Sump{Hold: 0, Line: line}, Sump{Hold: line, Line: line}
	if out.lift() >= in.lift() {
		t.Error("the tide doesn't come further up the shingle as the hold fills")
	}
	if (Sump{Stage: bellDrawing, Line: line}).lift() >= out.lift() || (Sump{Stage: bellRinging, Line: line}).lift() <= in.lift()-1 {
		t.Error("the water isn't drawn back before the bell, or back up while it rings")
	}
	if !(Sump{Stage: bellBoats}).boatsUp() || (Sump{}).boatsUp() || !(Sump{Stage: bellRinging}).leaning() {
		t.Error("the boats and the candles don't follow the bell")
	}
	for _, s := range []Sump{
		{Line: line, Light: dusk}, {Line: line, Light: night}, {Line: line, Light: dawn, Pumping: true},
		{Line: line, Light: daylight, Hold: line}, {Line: line, Stage: bellDrawing}, {Line: line, Stage: bellBoats}, {Line: line, Stage: bellRinging},
	} {
		frames := sumpScene(pixel.New(sumpSize.X, sumpSize.Y), s)
		if len(frames) != sumpFrames || bytes.Equal(frames[0].Pix, frames[1].Pix) {
			t.Errorf("%+v doesn't move", s)
		}
	}
	if len(sumpArts[night]().stars) == 0 || len(sumpArts[night]().lamps) <= len(sumpArts[daylight]().lamps) {
		t.Error("the night has no stars, or no more lamps lit than the day")
	}
}
