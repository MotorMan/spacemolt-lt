package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	crafterInterval = 10 * time.Second
)

// Crafter crafts items from blueprints.
func Crafter(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("crafter")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	ticker := time.NewTicker(crafterInterval)
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
			case <-time.After(crafterInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "crafter")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(crafterInterval):
			}
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
	Registry["crafter"] = Crafter
}

