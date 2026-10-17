//go:build !race

package pp

import "testing"

func skipRace(*testing.T) {}
