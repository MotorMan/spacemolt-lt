package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	fuelServiceInterval = 10 * time.Second
)

// FuelService refuels other boats on request.
func FuelService(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("fuel_service")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	ticker := time.NewTicker(fuelServiceInterval)
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
			case <-time.After(fuelServiceInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "fuel_service")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(fuelServiceInterval):
			}
			continue
		}

		// Refuel self if low.
		if snap.MaxFuel > 0 && snap.Fuel/snap.MaxFuel < 25 {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(fuelServiceInterval):
					}
					continue
				}
			}
			if err := EnsureFueled(ctx, b, 25); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(fuelServiceInterval):
				}
				continue
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
	Registry["fuel_service"] = FuelService
}

