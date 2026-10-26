package fix

import (
	"time"

	"github.com/6586x57890143/skua/internal/obs"
)

// Soft p99 targets are distinct from request cancellation deadlines. Slow
// upstreams remain visible as misses rather than being disguised as local CPU.
func targets() {
	obs.Default.SetTarget("fix build", obs.Run, 5*time.Millisecond)
	obs.Default.SetTarget("fix media", obs.HTTP, 8*time.Second)
	obs.Default.SetTarget("fix source", obs.HTTP, 4*time.Second)
	obs.Default.SetTarget("fix instagram direct", obs.HTTP, time.Second)
	obs.Default.SetTarget("fix reddit audio", obs.HTTP, 2*time.Second)
	obs.Default.SetTarget("fix end-to-end", obs.Run, 6*time.Second)
}
