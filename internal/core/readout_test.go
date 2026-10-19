package core

import (
	"testing"
	"time"
)

func TestReadoutIsBoldLabelLines(t *testing.T) {
	got := Readout([][2]string{{"gateway", "42 ms"}, {"kept", "<@1>, <@2>"}, {"/nest leave", ""}})
	if want := "**gateway** 42 ms\n**kept** <@1>, <@2>\n**/nest leave**\n"; got != want {
		t.Errorf("readout %q, want %q", got, want)
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		45 * time.Second: "45s", 12*time.Minute + 59*time.Second: "12m",
		3*time.Hour + 12*time.Minute: "3h 12m", 52 * time.Hour: "2d 4h",
	} {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
}
