package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	marketStreamerInterval = 30 * time.Second
)

// MarketStreamer periodically updates market data.
func MarketStreamer(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("market_streamer")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	ticker := time.NewTicker(marketStreamerInterval)
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
			case <-time.After(marketStreamerInterval):
			}
			continue
		}

		snap := b.Snapshot()

		if snap.InTransit {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}

		// Refresh nearby market data.
		_, _ = GetNearby(ctx, b)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func init() {
	Registry["market_streamer"] = MarketStreamer
}

