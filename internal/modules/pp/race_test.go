//go:build race

package pp

import "testing"

// skipRace skips a long simulation under the race detector, which slows it
// twentyfold and has nothing to find in one goroutine's arithmetic. CI runs
// the simulations in a step of their own, without it.
func skipRace(t *testing.T) {
	t.Helper()
	t.Skip("the economy simulations run without the race detector")
}
