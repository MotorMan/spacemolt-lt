package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	fuelTransportInterval = 10 * time.Second
)

// FuelTransportSettings holds per-bot fuel transport configuration.
type FuelTransportSettings struct {
	SourceSystem   string `json:"sourceSystem"`
	SourceStation  string `json:"sourceStation"`
	DestSystem     string `json:"destSystem"`
	DestStation    string `json:"destStation"`
	RefuelThreshold float64 `json:"refuelThreshold"`
	RepairThreshold float64 `json:"repairThreshold"`
}

func getFuelTransportSettings(username string) FuelTransportSettings {
	settings := readSettings()
	ft := map[string]interface{}{}
	if v, ok := settings["fuel_transport"].(map[string]interface{}); ok {
		ft = v
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
	s := FuelTransportSettings{
		SourceSystem:   getStr(ft, "sourceSystem", ""),
		SourceStation:  getStr(ft, "sourceStation", ""),
		DestSystem:     getStr(ft, "destSystem", ""),
		DestStation:    getStr(ft, "destStation", ""),
		RefuelThreshold: getFloat(ft, "refuelThreshold", 25),
		RepairThreshold: getFloat(ft, "repairThreshold", 40),
	}
	if username != "" {
		if bots, ok := settings[username].(map[string]interface{}); ok {
			if b, ok := bots["fuel_transport"].(map[string]interface{}); ok {
				s.SourceSystem = getStr(b, "sourceSystem", s.SourceSystem)
				s.DestSystem = getStr(b, "destSystem", s.DestSystem)
			}
		}
	}
	return s
}

// FuelTransport moves fuel cells between stations.
func FuelTransport(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("fuel_transport")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	settings := getFuelTransportSettings(b.Username)
	if settings.SourceSystem == "" {
		settings.SourceSystem = b.System
	}

	ticker := time.NewTicker(fuelTransportInterval)
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
			case <-time.After(fuelTransportInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "fuel_transport")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(fuelTransportInterval):
			}
			continue
		}

		if snap.MaxFuel > 0 && snap.Fuel/snap.MaxFuel < settings.RefuelThreshold/100 {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(fuelTransportInterval):
					}
					continue
				}
			}
			if err := EnsureFueled(ctx, b, settings.RefuelThreshold); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(fuelTransportInterval):
				}
				continue
			}
			continue
		}

		atSource := snap.System == settings.SourceSystem && (settings.SourceStation == "" || snap.POI == settings.SourceStation)
		atDest := snap.System == settings.DestSystem && (settings.DestStation == "" || snap.POI == settings.DestStation)

		if !atSource && !atDest {
			if settings.SourceSystem != snap.System {
				_, err := NavigateToSystem(ctx, b, settings.SourceSystem)
				if err != nil {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
					continue
				}
			}
			if settings.SourceStation != "" && snap.POI != settings.SourceStation {
				_, err := b.ExecCommand(ctx, "travel", map[string]interface{}{
					"target_poi": settings.SourceStation,
				})
				if err != nil {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
					continue
				}
			}
			_ = b.RefreshStatus(ctx)
			continue
		}

		if atSource {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
					continue
				}
			}
			_, err := b.ExecCommand(ctx, "spacemolt_storage.withdraw", map[string]interface{}{
				"item_id":  "fuel_cell",
				"quantity": 100,
			})
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				continue
			}
			_ = b.RefreshCargo(ctx)
		}

		if atDest {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
					continue
				}
			}
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
					_, err := b.ExecCommand(ctx, "spacemolt_storage.deposit", map[string]interface{}{
						"item_id":  item.ItemID,
						"quantity": item.Quantity,
					})
					if err != nil {
						break
					}
				}
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
	Registry["fuel_transport"] = FuelTransport
}

