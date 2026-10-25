package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	fuelCellSellerInterval = 10 * time.Second
)

// FuelCellSeller sells excess fuel cells at market stations.
func FuelCellSeller(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("fuel_cell_seller")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	ticker := time.NewTicker(fuelCellSellerInterval)
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
			case <-time.After(fuelCellSellerInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "fuel_cell_seller")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(fuelCellSellerInterval):
			}
			continue
		}

		// Sell fuel cells from cargo.
		cargo, err := GetCargo(ctx, b)
		if err == nil {
			for _, item := range cargo {
				if item.Quantity <= 0 {
					continue
				}
				isFuel := false
				for _, kw := range []string{"fuel", "cell", "energy"} {
					if contains(item.ItemID, kw) || contains(item.Name, kw) {
						isFuel = true
						break
					}
				}
				if !isFuel {
					continue
				}
				if !snap.Docked {
					if err := EnsureDocked(ctx, b); err != nil {
						break
					}
					snap = b.Snapshot()
				}
				_, err := b.ExecCommand(ctx, "sell", map[string]interface{}{
					"id":      item.ItemID,
					"quantity": item.Quantity,
				})
				if err != nil {
					break
				}
				_ = b.RefreshCargo(ctx)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func init() {
	Registry["fuel_cell_seller"] = FuelCellSeller
}

