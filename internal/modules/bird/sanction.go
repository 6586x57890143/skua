package bird

import (
	"context"

	"github.com/6586x57890143/skua/internal/core"
	"github.com/6586x57890143/skua/internal/sanction"
)

// Sanction is the bird as a sanction: the member is one bird for the whole
// sentence, and a member already a bird stays that bird for at least as
// long. Without a xeno-canto key it is unavailable, and the next sanction
// in line is tried.
func (m *Module) Sanction() sanction.Sanction { return sentence{m} }

type sentence struct{ m *Module }

func (sentence) Name() string { return "bird" }

// Apply says "a common blackbird for 20m".
func (s sentence) Apply(ctx context.Context, sn sanction.Sentence) (string, error) {
	if s.m.key == "" {
		return "", sanction.ErrUnavailable
	}
	species, err := s.m.Turn(ctx, sn.Guild, sn.User, sn.Rule, sn.For)
	if err != nil {
		return "", err
	}
	if species == "" {
		species = "bird"
	}
	return article(species) + " " + species + " for " + core.Duration(sn.For), nil
}
