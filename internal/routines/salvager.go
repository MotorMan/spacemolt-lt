package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	salvagerInterval = 10 * time.Second
)

// SalvagerSettings holds per-bot salvager configuration.
type SalvagerSettings struct {
	HomeSystem      string  `json:"homeSystem"`
	HomeStation     string  `json:"homeStation"`
	RefuelThreshold float64 `json:"refuelThreshold"`
	RepairThreshold float64 `json:"repairThreshold"`
	AutoCloak       bool    `json:"autoCloak"`
}

func getSalvagerSettings(username string) SalvagerSettings {
	settings := readSettings()
	sal := map[string]interface{}{}
	if v, ok := settings["salvager"].(map[string]interface{}); ok {
		sal = v
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
	getBool := func(m map[string]interface{}, key string, def bool) bool {
		if v, ok := m[key].(bool); ok {
			return v
		}
		return def
	}
	s := SalvagerSettings{
		HomeSystem:      getStr(sal, "homeSystem", ""),
		HomeStation:     getStr(sal, "homeStation", ""),
		RefuelThreshold: getFloat(sal, "refuelThreshold", 25),
		RepairThreshold: getFloat(sal, "repairThreshold", 40),
		AutoCloak:       getBool(sal, "autoCloak", true),
	}
	if username != "" {
		if bots, ok := settings[username].(map[string]interface{}); ok {
			if b, ok := bots["salvager"].(map[string]interface{}); ok {
				s.HomeSystem = getStr(b, "homeSystem", s.HomeSystem)
			}
		}
	}
	return s
}

// Salvager finds wrecks and scavenges them.
func Salvager(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("salvager")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	settings := getSalvagerSettings(b.Username)
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

	ticker := time.NewTicker(salvagerInterval)
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
			case <-time.After(salvagerInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "salvager")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(salvagerInterval):
			}
			continue
		}

		if snap.MaxFuel > 0 && snap.Fuel/snap.MaxFuel < settings.RefuelThreshold/100 {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(salvagerInterval):
					}
					continue
				}
			}
			if err := EnsureFueled(ctx, b, settings.RefuelThreshold); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(salvagerInterval):
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
					case <-time.After(salvagerInterval):
					}
					continue
				}
			}
			if err := RepairShip(ctx, b); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(salvagerInterval):
				}
				continue
			}
			continue
		}

		// Scan for wrecks nearby.
		pois, err := GetNearby(ctx, b)
		if err != nil || len(pois) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}

		for _, p := range pois {
			t, _ := p["type"].(string)
			if t == "wreck" || t == "debris" || t == "wreckage" {
				poiID, _ := p["id"].(string)
				if snap.POI != poiID {
					_, err := b.ExecCommand(ctx, "travel", map[string]interface{}{
						"target_poi": poiID,
					})
					if err != nil {
						break
					}
					_ = b.RefreshStatus(ctx)
					break
				}
				// Scavenge the wreck.
				_, err := b.ExecCommand(ctx, "scavenge", map[string]interface{}{
					"target_id": poiID,
				})
				if err != nil {
					break
				}
				_ = b.RefreshStatus(ctx)
				_ = b.RefreshCargo(ctx)
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
	Registry["salvager"] = Salvager
}

