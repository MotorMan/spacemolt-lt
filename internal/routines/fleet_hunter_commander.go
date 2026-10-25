package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	fleetHunterCommanderInterval = 10 * time.Second
)

// FleetHunterCommander coordinates fleet hunter operations.
func FleetHunterCommander(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("fleet_hunter_commander")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	ticker := time.NewTicker(fleetHunterCommanderInterval)
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
			case <-time.After(fleetHunterCommanderInterval):
			}
			continue
		}

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "fleet_hunter_commander")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(fleetHunterCommanderInterval):
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
	Registry["fleet_hunter_commander"] = FleetHunterCommander
}

