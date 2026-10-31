package pp

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/6586x57890143/skua/internal/pixel"
)

// TestShots writes every slack tide frame as a PNG to SKUA_SHOTS, for
// looking at while the art is worked on. Without it, it does nothing.
func TestShots(t *testing.T) {
	dir := os.Getenv("SKUA_SHOTS")
	if dir == "" {
		t.Skip("SKUA_SHOTS unset")
	}
	seats := []TableSeat{
		{Name: "heavywinds", Class: Classes[1], Stake: stakes[0].pp, Throw: 37},
		{Name: "steve", Class: Classes[3], Stake: stakes[3].pp, Throw: 100, Won: stakes[3].pp * 10},
		{Name: "a very long name indeed", Class: Classes[5], Stake: stakes[1].pp, Throw: 64, Won: stakes[1].pp * 3 / 2},
		{Name: "tester", Class: Classes[7], Stake: stakes[2].pp, Throw: 92, Won: stakes[2].pp * 5 / 2},
	}
	write := func(name string, frames []*pixel.Canvas) {
		for i, f := range frames {
			// As it ships: softened and dithered the way every screen is.
			var buf bytes.Buffer
			if err := png.Encode(&buf, f.SoftImage(screenScale)); err != nil {
				t.Fatal(err)
			}
			b := buf.Bytes()
			if err := os.WriteFile(filepath.Join(dir, name+"_"+strconv.Itoa(i)+".png"), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	f, _ := tableFrames(Table{Seats: seats[:2], Slate: "9f2c41ab77e0"})
	write("table_open", f)
	f, _ = tableFrames(Table{Seats: seats, Thrown: true, Slate: "3e8d0b5512aa"})
	write("table_thrown", f)
	f, _ = tableFrames(Table{Seats: seats, Bell: true})
	write("table_bell", f)
	f, _ = holdFrames(House{Hold: 31000 * unit, Line: houseLine(), MaxStake: 310 * unit, Staked: 125 * unit, Cap: 1264 * unit})
	write("hold", f)
	f, _ = holdFrames(House{Hold: houseLine(), Line: houseLine(), MaxStake: 500 * unit, Bell: true, Cap: 1264 * unit})
	write("hold_full", f)
}
