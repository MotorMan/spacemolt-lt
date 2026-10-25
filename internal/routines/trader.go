package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	traderCycleInterval = 10 * time.Second
	traderTradeInterval = 5 * time.Second
)

// Trader is the core trading routine: find profitable trade → buy → travel → sell → repeat.
func Trader(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("trader")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	settings := getTraderSettings(b.Username)
	if settings.HomeSystem == "" {
		settings.HomeSystem = b.System
	}

	// Wait for any pending transit to complete.
	snap := b.Snapshot()
	if snap.InTransit {
		ok, _ := WaitForTransitCompletion(ctx, b, 180)
		if !ok {
			return
		}
		_ = b.RefreshStatus(ctx)
	}

	// Enable cloaking if configured.
	if settings.AutoCloak && !snap.IsCloaked && !snap.Docked {
		_ = enableCloakingIfPossible(ctx, b)
	}

	tradeTicker := time.NewTicker(traderTradeInterval)
	defer tradeTicker.Stop()

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
			case <-time.After(traderCycleInterval):
			}
			continue
		}

		snap = b.Snapshot()

		if snap.InTransit {
			ok, _ := WaitForTransitCompletion(ctx, b, 180)
			if !ok {
				return
			}
			_ = b.RefreshStatus(ctx)
			continue
		}

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "trader")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(traderCycleInterval):
			}
			continue
		}

		// Refuel if low.
		if snap.MaxFuel > 0 && snap.Fuel/snap.MaxFuel < settings.RefuelThreshold/100 {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(traderCycleInterval):
					}
					continue
				}
			}
			if err := EnsureFueled(ctx, b, settings.RefuelThreshold); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(traderCycleInterval):
				}
				continue
			}
			continue
		}

		// Repair if hull low.
		if snap.MaxHull > 0 && snap.Hull/snap.MaxHull < settings.RepairThreshold/100 {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(traderCycleInterval):
					}
					continue
				}
			}
			if err := RepairShip(ctx, b); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(traderCycleInterval):
				}
				continue
			}
			continue
		}

		// Find a profitable trade.
		op, err := FindProfitableTrade(ctx, b, settings)
		if err != nil || op == nil {
			select {
			case <-ctx.Done():
				return
			case <-tradeTicker.C:
			}
			continue
		}

		// Travel to buy station if not there.
		if snap.System != op.BuySystem || snap.POI != op.BuyStation {
			if op.BuySystem != snap.System {
				_, err := NavigateToSystem(ctx, b, op.BuySystem)
				if err != nil {
					select {
					case <-ctx.Done():
						return
					case <-tradeTicker.C:
					}
					continue
				}
			}
			_, err := b.ExecCommand(ctx, "travel", map[string]interface{}{
				"target_poi": op.BuyStation,
			})
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-tradeTicker.C:
				}
				continue
			}
			_ = b.RefreshStatus(ctx)
			continue
		}

		// Dock at buy station.
		if !snap.Docked {
			if err := EnsureDocked(ctx, b); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-tradeTicker.C:
				}
				continue
			}
			continue
		}

		// Buy the item.
		qty := op.Quantity
		if qty > 100 {
			qty = 100
		}
		if qty <= 0 {
			qty = 1
		}
		_, err = b.ExecCommand(ctx, "buy", map[string]interface{}{
			"id":      op.ItemID,
			"quantity": qty,
		})
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-tradeTicker.C:
			}
			continue
		}
		_ = b.RefreshStatus(ctx)
		_ = b.RefreshCargo(ctx)

		// Undock and travel to sell station (home system).
		_, err = b.ExecCommand(ctx, "undock", nil)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-tradeTicker.C:
			}
			continue
		}

		if settings.HomeSystem != snap.System {
			_, err := NavigateToSystem(ctx, b, settings.HomeSystem)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-tradeTicker.C:
				}
				continue
			}
		}

		// Find a station to sell at.
		pois, err := GetSystemPOIs(ctx, b)
		if err != nil || len(pois) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-tradeTicker.C:
			}
			continue
		}
		var sellStation map[string]interface{}
		if settings.HomeStation != "" {
			for _, p := range pois {
				if id, ok := p["id"].(string); ok && id == settings.HomeStation && IsStationPoi(p) {
					sellStation = p
					break
				}
			}
		}
		if sellStation == nil {
			sellStation = FindStation(pois)
		}
		if sellStation == nil {
			select {
			case <-ctx.Done():
				return
			case <-tradeTicker.C:
			}
			continue
		}
		sellStationID, _ := sellStation["id"].(string)

		snap = b.Snapshot()
		if snap.POI != sellStationID {
			_, err := b.ExecCommand(ctx, "travel", map[string]interface{}{
				"target_poi": sellStationID,
			})
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-tradeTicker.C:
				}
				continue
			}
			_ = b.RefreshStatus(ctx)
			continue
		}

		if !snap.Docked {
			if err := EnsureDocked(ctx, b); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-tradeTicker.C:
				}
				continue
			}
			continue
		}

		// Sell the item.
		_, err = b.ExecCommand(ctx, "sell", map[string]interface{}{
			"id":      op.ItemID,
			"quantity": qty,
		})
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-tradeTicker.C:
			}
			continue
		}
		_ = b.RefreshStatus(ctx)
		_ = b.RefreshCargo(ctx)

		select {
		case <-ctx.Done():
			return
		case <-tradeTicker.C:
		}
	}
}

func init() {
	Registry["trader"] = Trader
}

