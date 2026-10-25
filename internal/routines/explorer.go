package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	explorerInterval = 10 * time.Second
)

// ExplorerSettings holds per-bot explorer configuration.
type ExplorerSettings struct {
	HomeSystem       string  `json:"homeSystem"`
	HomeStation      string  `json:"homeStation"`
	FocusArea        string  `json:"focusArea"`
	MaxJumps         int     `json:"maxJumps"`
	RefuelThreshold  float64 `json:"refuelThreshold"`
	RepairThreshold  float64 `json:"repairThreshold"`
	AutoCloak        bool    `json:"autoCloak"`
	ScavengeEnabled  bool    `json:"scavengeEnabled"`
}

func getExplorerSettings(username string) ExplorerSettings {
	settings := readSettings()
	ex := map[string]interface{}{}
	if v, ok := settings["explorer"].(map[string]interface{}); ok {
		ex = v
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
	getBool := func(m map[string]interface{}, key string, def bool) bool {
		if v, ok := m[key].(bool); ok {
			return v
		}
		return def
	}
	s := ExplorerSettings{
		HomeSystem:       getStr(ex, "homeSystem", ""),
		HomeStation:      getStr(ex, "homeStation", ""),
		FocusArea:        getStr(ex, "focusArea", ""),
		MaxJumps:         getInt(ex, "maxJumps", 5),
		RefuelThreshold:  getFloat(ex, "refuelThreshold", 25),
		RepairThreshold:  getFloat(ex, "repairThreshold", 40),
		AutoCloak:        getBool(ex, "autoCloak", true),
		ScavengeEnabled:  getBool(ex, "scavengeEnabled", false),
	}
	if username != "" {
		if bots, ok := settings[username].(map[string]interface{}); ok {
			if b, ok := bots["explorer"].(map[string]interface{}); ok {
				s.HomeSystem = getStr(b, "homeSystem", s.HomeSystem)
				s.FocusArea = getStr(b, "focusArea", s.FocusArea)
				s.MaxJumps = getInt(b, "maxJumps", s.MaxJumps)
			}
		}
	}
	return s
}

// Explorer explores unknown systems and scans POIs.
func Explorer(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("explorer")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	settings := getExplorerSettings(b.Username)
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

	ticker := time.NewTicker(explorerInterval)
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
			case <-time.After(explorerInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "explorer")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(explorerInterval):
			}
			continue
		}

		if snap.MaxFuel > 0 && snap.Fuel/snap.MaxFuel < settings.RefuelThreshold/100 {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(explorerInterval):
					}
					continue
				}
			}
			if err := EnsureFueled(ctx, b, settings.RefuelThreshold); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(explorerInterval):
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
					case <-time.After(explorerInterval):
					}
					continue
				}
			}
			if err := RepairShip(ctx, b); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(explorerInterval):
				}
				continue
			}
			continue
		}

		// Scan nearby POIs to map the system.
		pois, err := GetNearby(ctx, b)
		if err != nil || len(pois) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}

		// Visit unexplored POIs in current system.
		for _, p := range pois {
			t, _ := p["type"].(string)
			if t == "jump_gate" || t == "wormhole" || t == "anomaly" || t == "unknown" {
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
				// Scan the POI.
				_, err := b.ExecCommand(ctx, "scan", map[string]interface{}{
					"target_id": poiID,
				})
				if err != nil {
					break
				}
				_ = b.RefreshStatus(ctx)
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
	Registry["explorer"] = Explorer
}

