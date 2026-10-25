package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	stealthGrindInterval = 10 * time.Second
)

// StealthGrind grinds skills while cloaked.
func StealthGrind(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("stealth_grind")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	ticker := time.NewTicker(stealthGrindInterval)
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
			case <-time.After(stealthGrindInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "stealth_grind")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(stealthGrindInterval):
			}
			continue
		}

		if !snap.IsCloaked {
			_ = enableCloakingIfPossible(ctx, b)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func init() {
	Registry["stealth_grind"] = StealthGrind
}

