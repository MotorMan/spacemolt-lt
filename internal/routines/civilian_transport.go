package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	civilianTransportInterval = 10 * time.Second
)

// CivilianTransport moves passengers between stations.
func CivilianTransport(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("civilian_transport")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	ticker := time.NewTicker(civilianTransportInterval)
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
			case <-time.After(civilianTransportInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "civilian_transport")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(civilianTransportInterval):
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
	Registry["civilian_transport"] = CivilianTransport
}

