package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	pathfinderTestInterval = 5 * time.Second
)

// PathfinderTest tests pathfinding between systems.
func PathfinderTest(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("pathfinder_test")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	ticker := time.NewTicker(pathfinderTestInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !b.IsRunning() {
			return
		}

		if err := b.RefreshStatus(ctx); err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(pathfinderTestInterval):
			}
			continue
		}

		snap := b.Snapshot()

		if snap.InTransit {
			ok, _ := WaitForTransitCompletion(ctx, b, 180)
			if !ok {
				return
			}
			_ = b.RefreshStatus(ctx)
			continue
		}

		// Periodically get_status to test pathfinding connectivity.
		_, _ = b.ExecCommand(ctx, "get_status", nil)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func init() {
	Registry["pathfinder_test"] = PathfinderTest
}

