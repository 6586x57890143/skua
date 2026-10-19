//go:build live

package bird

import (
	"context"
	"testing"
	"time"

	"github.com/6586x57890143/skua/internal/filter"
	"github.com/6586x57890143/skua/internal/guard"
)

// go test -tags live -run Live -v ./internal/modules/bird: the real
// iNaturalist and Wikipedia, for birds xeno-canto names. Not run in CI.
func TestLiveLookUp(t *testing.T) {
	m := New(guard.New(), &fakePoster{}, filter.Default(), "")
	for _, sp := range [][2]string{{"Turdus", "merula"}, {"Troglodytes", "troglodytes"}, {"Erithacus", "rubecula"}, {"Strix", "uralensis"}, {"Cyanocitta", "cristata"}, {"Pitangus", "sulphuratus"}, {"Pyriglena", "maura"}, {"Thamnophilus", "doliatus"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		a := m.lookUp(ctx, recording{ID: "1", Gen: sp[0], Sp: sp[1]})
		cancel()
		t.Logf("%s %s: photo %d bytes %s (%s)\n  line: %s %s", sp[0], sp[1], len(a.photo), a.photoName, a.credit, a.line, a.article)
	}
}
