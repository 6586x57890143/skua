package main

import (
	"testing"
	"time"
)

func TestPercentile(t *testing.T) {
	var d []time.Duration
	for i := 1; i <= 100; i++ {
		d = append(d, time.Duration(i)*time.Millisecond)
	}
	if at(d, 50) != 50*time.Millisecond || at(d, 99) != 99*time.Millisecond || at(d[:1], 99) != time.Millisecond || at(nil, 50) != 0 {
		t.Fatal("percentiles")
	}
	if ms(1500*time.Microsecond) != "1.5ms" {
		t.Fatal(ms(1500 * time.Microsecond))
	}
}
