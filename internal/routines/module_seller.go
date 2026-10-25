package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	moduleSellerInterval = 10 * time.Second
)

// ModuleSeller sells excess ship modules at market.
func ModuleSeller(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("module_seller")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	ticker := time.NewTicker(moduleSellerInterval)
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
			case <-time.After(moduleSellerInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "module_seller")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(moduleSellerInterval):
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
	Registry["module_seller"] = ModuleSeller
}

