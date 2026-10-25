package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	fleetHunterSubordinateInterval = 10 * time.Second
)

// FleetHunterSubordinate follows fleet hunter commander orders.
func FleetHunterSubordinate(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("fleet_hunter_subordinate")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	ticker := time.NewTicker(fleetHunterSubordinateInterval)
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
			case <-time.After(fleetHunterSubordinateInterval):
			}
			continue
		}

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "fleet_hunter_subordinate")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(fleetHunterSubordinateInterval):
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
	Registry["fleet_hunter_subordinate"] = FleetHunterSubordinate
}

