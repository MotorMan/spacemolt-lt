package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	craftGoalsInterval = 10 * time.Second
)

// CraftGoals manages crafting goals.
func CraftGoals(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("craft_goals")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	ticker := time.NewTicker(craftGoalsInterval)
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
			case <-time.After(craftGoalsInterval):
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

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func init() {
	Registry["craft_goals"] = CraftGoals
}

