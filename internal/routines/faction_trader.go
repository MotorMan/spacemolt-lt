package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	factionTraderInterval = 10 * time.Second
)

// FactionTraderSettings holds per-bot faction trader configuration.
type FactionTraderSettings struct {
	HomeSystem         string  `json:"homeSystem"`
	HomeStation        string  `json:"homeStation"`
	FactionStorageFee  float64 `json:"factionStorageFee"`
	MinProfit          float64 `json:"minProfit"`
	RefuelThreshold    float64 `json:"refuelThreshold"`
	RepairThreshold    float64 `json:"repairThreshold"`
}

func getFactionTraderSettings(username string) FactionTraderSettings {
	settings := readSettings()
	ft := map[string]interface{}{}
	if v, ok := settings["faction_trader"].(map[string]interface{}); ok {
		ft = v
	}
	getFloat := func(m map[string]interface{}, key string, def float64) float64 {
		if v, ok := m[key].(float64); ok {
			return v
		}
		return def
	}
	getStr := func(m map[string]interface{}, key, def string) string {
		if v, ok := m[key].(string); ok && v != "" {
			return v
		}
		return def
	}
	s := FactionTraderSettings{
		HomeSystem:      getStr(ft, "homeSystem", ""),
		HomeStation:     getStr(ft, "homeStation", ""),
		FactionStorageFee: getFloat(ft, "factionStorageFee", 0),
		MinProfit:       getFloat(ft, "minProfit", 100),
		RefuelThreshold: getFloat(ft, "refuelThreshold", 25),
		RepairThreshold: getFloat(ft, "repairThreshold", 40),
	}
	if username != "" {
		if bots, ok := settings[username].(map[string]interface{}); ok {
			if b, ok := bots["faction_trader"].(map[string]interface{}); ok {
				s.HomeSystem = getStr(b, "homeSystem", s.HomeSystem)
				s.MinProfit = getFloat(b, "minProfit", s.MinProfit)
			}
		}
	}
	return s
}

// FactionTrader trades using faction storage as a marketplace.
func FactionTrader(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("faction_trader")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	settings := getFactionTraderSettings(b.Username)
	if settings.HomeSystem == "" {
		settings.HomeSystem = b.System
	}

	snap := b.Snapshot()
	if snap.InTransit {
		ok, _ := WaitForTransitCompletion(ctx, b, 180)
		if !ok {
			return
		}
		_ = b.RefreshStatus(ctx)
	}

	ticker := time.NewTicker(factionTraderInterval)
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
			case <-time.After(factionTraderInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "faction_trader")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(factionTraderInterval):
			}
			continue
		}

		if snap.MaxFuel > 0 && snap.Fuel/snap.MaxFuel < settings.RefuelThreshold/100 {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(factionTraderInterval):
					}
					continue
				}
			}
			if err := EnsureFueled(ctx, b, settings.RefuelThreshold); err != nil {
				select {
				case <-ctx.Done():
					return
					case <-time.After(factionTraderInterval):
				}
				continue
			}
			continue
		}

		// Deposit surplus cargo to faction storage.
		cargo, err := GetCargo(ctx, b)
		if err == nil && len(cargo) > 0 {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(factionTraderInterval):
					}
					continue
				}
			}
			for _, item := range cargo {
				if item.Quantity <= 0 {
					continue
				}
				_, err := b.ExecCommand(ctx, "spacemolt_storage.deposit", map[string]interface{}{
					"item_id":  item.ItemID,
					"quantity": item.Quantity,
					"target":   "faction",
				})
				if err != nil {
					break
				}
			}
			_ = b.RefreshCargo(ctx)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func init() {
	Registry["faction_trader"] = FactionTrader
}

