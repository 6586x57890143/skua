package preen

import (
	"context"
	"sync"
)

// Strategy is one way of putting birds up. add puts one bird up; Fill stops
// starting birds at the first error and returns it.
type Strategy struct {
	Name string
	Fill func(ctx context.Context, birds []string, add func(context.Context, string) error) error
}

// Strategies are what tools/reactbench compares live; preen uses the first.
// They differ only in how many calls skua keeps in flight. Measured on
// 2026-10-05 they fill fifteen birds alike, about 5.5s, because disgo holds
// the reaction bucket across each round trip and Discord paces the route
// itself, so serial won on being the simplest. A new idea for a faster fill
// goes here, then through reactbench, before it goes first.
var Strategies = []Strategy{
	{"serial", workers(1)},
	{"pairs", workers(2)},
	{"quads", workers(4)},
	{"burst", workers(len(Flock))},
}

// workers fills with n goroutines taking birds in order from one queue.
func workers(n int) func(context.Context, []string, func(context.Context, string) error) error {
	return func(ctx context.Context, birds []string, add func(context.Context, string) error) error {
		ctx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		queue := make(chan string, len(birds))
		for _, b := range birds {
			queue <- b
		}
		close(queue)
		var wg sync.WaitGroup
		for range min(n, len(birds)) {
			wg.Go(func() {
				for b := range queue {
					if ctx.Err() != nil {
						return
					}
					if err := add(ctx, b); err != nil {
						cancel(err)
						return
					}
				}
			})
		}
		wg.Wait()
		return context.Cause(ctx)
	}
}
