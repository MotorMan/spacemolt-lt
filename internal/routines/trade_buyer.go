package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	tradeBuyerInterval = 10 * time.Second
)

// TradeBuyerSettings holds per-bot trade buyer configuration.
type TradeBuyerSettings struct {
	HomeSystem    string  `json:"homeSystem"`
	HomeStation   string  `json:"homeStation"`
	MaxPrice      float64 `json:"maxPrice"`
	MinStock      int     `json:"minStock"`
	BuyItems      []string `json:"buyItems"`
	RefuelThreshold float64 `json:"refuelThreshold"`
	RepairThreshold float64 `json:"repairThreshold"`
}

func getTradeBuyerSettings(username string) TradeBuyerSettings {
	settings := readSettings()
	tb := map[string]interface{}{}
	if v, ok := settings["trade_buyer"].(map[string]interface{}); ok {
		tb = v
	}
	getStr := func(m map[string]interface{}, key, def string) string {
		if v, ok := m[key].(string); ok && v != "" {
			return v
		}
		return def
	}
	getFloat := func(m map[string]interface{}, key string, def float64) float64 {
		if v, ok := m[key].(float64); ok {
			return v
		}
		return def
	}
	getInt := func(m map[string]interface{}, key string, def int) int {
		if v, ok := m[key].(float64); ok {
			return int(v)
		}
		return def
	}
	getStringList := func(m map[string]interface{}, key string) []string {
		if v, ok := m[key].([]interface{}); ok {
			out := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
		return nil
	}
	s := TradeBuyerSettings{
		HomeSystem:       getStr(tb, "homeSystem", ""),
		HomeStation:      getStr(tb, "homeStation", ""),
		MaxPrice:         getFloat(tb, "maxPrice", 10000),
		MinStock:         getInt(tb, "minStock", 1),
		RefuelThreshold:  getFloat(tb, "refuelThreshold", 25),
		RepairThreshold:  getFloat(tb, "repairThreshold", 40),
		BuyItems:         getStringList(tb, "buyItems"),
	}
	if username != "" {
		if bots, ok := settings[username].(map[string]interface{}); ok {
			if b, ok := bots["trade_buyer"].(map[string]interface{}); ok {
				s.HomeSystem = getStr(b, "homeSystem", s.HomeSystem)
				s.MaxPrice = getFloat(b, "maxPrice", s.MaxPrice)
				s.BuyItems = getStringList(b, "buyItems")
			}
		}
	}
	return s
}

// TradeBuyer buys needed items at market stations.
func TradeBuyer(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("trade_buyer")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	settings := getTradeBuyerSettings(b.Username)
	if settings.HomeSystem == "" {
		settings.HomeSystem = b.System
	}
	if len(settings.BuyItems) == 0 {
		settings.BuyItems = []string{"fuel_cell", "shield_generator", "armor_plate"}
	}

	snap := b.Snapshot()
	if snap.InTransit {
		ok, _ := WaitForTransitCompletion(ctx, b, 180)
		if !ok {
			return
		}
		_ = b.RefreshStatus(ctx)
	}

	ticker := time.NewTicker(tradeBuyerInterval)
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
			case <-time.After(tradeBuyerInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "trade_buyer")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(tradeBuyerInterval):
			}
			continue
		}

		// Refuel/repair if needed.
		if snap.MaxFuel > 0 && snap.Fuel/snap.MaxFuel < settings.RefuelThreshold/100 {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(tradeBuyerInterval):
					}
					continue
				}
			}
			if err := EnsureFueled(ctx, b, settings.RefuelThreshold); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(tradeBuyerInterval):
				}
				continue
			}
			continue
		}
		if snap.MaxHull > 0 && snap.Hull/snap.MaxHull < settings.RepairThreshold/100 {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(tradeBuyerInterval):
					}
					continue
				}
			}
			if err := RepairShip(ctx, b); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(tradeBuyerInterval):
				}
				continue
			}
			continue
		}

		// Find stations in current system.
		pois, err := GetSystemPOIs(ctx, b)
		if err != nil || len(pois) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}
		var stations []map[string]interface{}
		for _, p := range pois {
			if IsStationPoi(p) {
				stations = append(stations, p)
			}
		}

		bought := false
		for _, itemID := range settings.BuyItems {
			for _, station := range stations {
				stationID, _ := station["id"].(string)
				prices, err := GetStationMarket(ctx, b, stationID)
				if err != nil || len(prices) == 0 {
					continue
				}
				for _, price := range prices {
					if price.ItemID != itemID {
						continue
					}
					if price.BuyPrice > 0 && price.BuyPrice <= settings.MaxPrice && price.Stock >= settings.MinStock {
						// Dock if needed.
						if !snap.Docked {
							if err := EnsureDocked(ctx, b); err != nil {
								break
							}
							snap = b.Snapshot()
						}
						// Buy.
						qty := settings.MinStock
						if qty > price.Stock {
							qty = price.Stock
						}
						_, err := b.ExecCommand(ctx, "buy", map[string]interface{}{
							"id":      itemID,
							"quantity": qty,
						})
						if err != nil {
							break
						}
						_ = b.RefreshStatus(ctx)
						_ = b.RefreshCargo(ctx)
						bought = true
						break
					}
				}
				if bought {
					break
				}
			}
			if bought {
				break
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
	Registry["trade_buyer"] = TradeBuyer
}

