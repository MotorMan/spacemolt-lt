package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	cargoMoverInterval = 10 * time.Second
)

// CargoMoverSettings holds per-bot cargo mover configuration.
type CargoMoverSettings struct {
	SourceSystem   string `json:"sourceSystem"`
	SourceStation  string `json:"sourceStation"`
	DestSystem     string `json:"destSystem"`
	DestStation    string `json:"destStation"`
	MoveItems      []string `json:"moveItems"`
	RefuelThreshold float64 `json:"refuelThreshold"`
	RepairThreshold float64 `json:"repairThreshold"`
}

func getCargoMoverSettings(username string) CargoMoverSettings {
	settings := readSettings()
	cm := map[string]interface{}{}
	if v, ok := settings["cargo_mover"].(map[string]interface{}); ok {
		cm = v
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
	s := CargoMoverSettings{
		SourceSystem:    getStr(cm, "sourceSystem", ""),
		SourceStation:   getStr(cm, "sourceStation", ""),
		DestSystem:      getStr(cm, "destSystem", ""),
		DestStation:     getStr(cm, "destStation", ""),
		RefuelThreshold: getFloat(cm, "refuelThreshold", 25),
		RepairThreshold: getFloat(cm, "repairThreshold", 40),
		MoveItems:       getStringList(cm, "moveItems"),
	}
	if username != "" {
		if bots, ok := settings[username].(map[string]interface{}); ok {
			if b, ok := bots["cargo_mover"].(map[string]interface{}); ok {
				s.SourceSystem = getStr(b, "sourceSystem", s.SourceSystem)
				s.DestSystem = getStr(b, "destSystem", s.DestSystem)
				s.MoveItems = getStringList(b, "moveItems")
			}
		}
	}
	return s
}

// CargoMover moves cargo between source and destination stations.
func CargoMover(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("cargo_mover")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	settings := getCargoMoverSettings(b.Username)
	if settings.SourceSystem == "" {
		settings.SourceSystem = b.System
	}

	snap := b.Snapshot()
	if snap.InTransit {
		ok, _ := WaitForTransitCompletion(ctx, b, 180)
		if !ok {
			return
		}
		_ = b.RefreshStatus(ctx)
	}

	ticker := time.NewTicker(cargoMoverInterval)
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
			case <-time.After(cargoMoverInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "cargo_mover")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(cargoMoverInterval):
			}
			continue
		}

		if snap.MaxFuel > 0 && snap.Fuel/snap.MaxFuel < settings.RefuelThreshold/100 {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(cargoMoverInterval):
					}
					continue
				}
			}
			if err := EnsureFueled(ctx, b, settings.RefuelThreshold); err != nil {
				select {
				case <-ctx.Done():
					return
					case <-time.After(cargoMoverInterval):
				}
				continue
			}
			continue
		}

		// Determine current phase: at source picking up, or at destination dropping off.
		atSource := snap.System == settings.SourceSystem && (settings.SourceStation == "" || snap.POI == settings.SourceStation)
		atDest := snap.System == settings.DestSystem && (settings.DestStation == "" || snap.POI == settings.DestStation)

		if !atSource && !atDest {
			// Travel to source first.
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
			// Pick up cargo from source station storage.
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
			// Withdraw items from station storage.
			for _, itemID := range settings.MoveItems {
				_, err := b.ExecCommand(ctx, "spacemolt_storage.withdraw", map[string]interface{}{
					"item_id":  itemID,
					"quantity": 100,
				})
				if err != nil {
					break
				}
			}
			_ = b.RefreshCargo(ctx)
		}

		if atDest {
			// Drop off cargo at destination station storage.
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
	Registry["cargo_mover"] = CargoMover
}

