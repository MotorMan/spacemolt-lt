package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	hunterInterval = 10 * time.Second
)

// HunterSettings holds per-bot hunter configuration.
type HunterSettings struct {
	HomeSystem      string  `json:"homeSystem"`
	HomeStation     string  `json:"homeStation"`
	PatrolSystems   []string `json:"patrolSystems"`
	AggressionLevel string  `json:"aggressionLevel"`
	RefuelThreshold float64 `json:"refuelThreshold"`
	RepairThreshold float64 `json:"repairThreshold"`
	AutoCloak       bool    `json:"autoCloak"`
}

func getHunterSettings(username string) HunterSettings {
	settings := readSettings()
	h := map[string]interface{}{}
	if v, ok := settings["hunter"].(map[string]interface{}); ok {
		h = v
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
	getBool := func(m map[string]interface{}, key string, def bool) bool {
		if v, ok := m[key].(bool); ok {
			return v
		}
		return def
	}
	s := HunterSettings{
		HomeSystem:      getStr(h, "homeSystem", ""),
		HomeStation:     getStr(h, "homeStation", ""),
		PatrolSystems:   getStringList(h, "patrolSystems"),
		AggressionLevel: getStr(h, "aggressionLevel", "defensive"),
		RefuelThreshold: getFloat(h, "refuelThreshold", 25),
		RepairThreshold: getFloat(h, "repairThreshold", 40),
		AutoCloak:       getBool(h, "autoCloak", true),
	}
	if username != "" {
		if bots, ok := settings[username].(map[string]interface{}); ok {
			if b, ok := bots["hunter"].(map[string]interface{}); ok {
				s.HomeSystem = getStr(b, "homeSystem", s.HomeSystem)
				s.PatrolSystems = getStringList(b, "patrolSystems")
				s.AggressionLevel = getStr(b, "aggressionLevel", s.AggressionLevel)
			}
		}
	}
	return s
}

// Hunter patrols systems and engages hostiles.
func Hunter(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("hunter")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	settings := getHunterSettings(b.Username)
	if settings.HomeSystem == "" {
		settings.HomeSystem = b.System
	}
	if len(settings.PatrolSystems) == 0 {
		settings.PatrolSystems = []string{b.System}
	}

	snap := b.Snapshot()
	if snap.InTransit {
		ok, _ := WaitForTransitCompletion(ctx, b, 180)
		if !ok {
			return
		}
		_ = b.RefreshStatus(ctx)
	}

	patrolIdx := 0
	ticker := time.NewTicker(hunterInterval)
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
			case <-time.After(hunterInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "hunter")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(hunterInterval):
			}
			continue
		}

		if snap.MaxFuel > 0 && snap.Fuel/snap.MaxFuel < settings.RefuelThreshold/100 {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(hunterInterval):
					}
					continue
				}
			}
			if err := EnsureFueled(ctx, b, settings.RefuelThreshold); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(hunterInterval):
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
					case <-time.After(hunterInterval):
					}
					continue
				}
			}
			if err := RepairShip(ctx, b); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(hunterInterval):
				}
				continue
			}
			continue
		}

		// Patrol: move to next patrol system.
		targetSystem := settings.PatrolSystems[patrolIdx]
		patrolIdx = (patrolIdx + 1) % len(settings.PatrolSystems)

		if snap.System != targetSystem {
			_, err := NavigateToSystem(ctx, b, targetSystem)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				continue
			}
			_ = b.RefreshStatus(ctx)
			continue
		}

		// Scan for hostiles (get_nearby returns entities).
		pois, err := GetNearby(ctx, b)
		if err != nil || len(pois) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}

		// Engage any hostile ships found.
		for _, p := range pois {
			t, _ := p["type"].(string)
			if t == "ship" || t == "hostile" || t == "pirate" {
				_, err := b.ExecCommand(ctx, "engage", map[string]interface{}{
					"target_id": p["id"],
				})
				if err != nil {
					continue
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
	Registry["hunter"] = Hunter
}

