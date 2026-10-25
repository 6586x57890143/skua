package pp

import (
	"os"
	"sync"
	"testing"

	"github.com/6586x57890143/skua/internal/pixel"
)

func TestMain(m *testing.M) {
	pixel.Draft.Store(true)
	os.Exit(m.Run())
}

// realWanted counts the tests running now that look at their screens.
var realWanted struct {
	sync.Mutex
	n int
}

// realScreens draws and encodes screens for real while t runs, and runs t
// in parallel with the others that do: they are the slow ones. A test that
// presses buttons without looking may get a real screen meanwhile, which
// only costs it time.
func realScreens(t *testing.T) {
	// First: a parallel test waits here for the serial ones, which must
	// not be kept drawing for real meanwhile.
	t.Parallel()
	realWanted.Lock()
	realWanted.n++
	pixel.Draft.Store(false)
	realWanted.Unlock()
	t.Cleanup(func() {
		realWanted.Lock()
		defer realWanted.Unlock()
		if realWanted.n--; realWanted.n == 0 {
			pixel.Draft.Store(true)
		}
	})
}
