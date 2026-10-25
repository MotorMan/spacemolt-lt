package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	escortInterval = 10 * time.Second
)

// EscortSettings holds per-bot escort configuration.
type EscortSettings struct {
	LeadBot      string  `json:"leadBot"`
	HomeSystem   string  `json:"homeSystem"`
	RefuelThreshold float64 `json:"refuelThreshold"`
	RepairThreshold float64 `json:"repairThreshold"`
	AutoCloak     bool    `json:"autoCloak"`
}

func getEscortSettings(username string) EscortSettings {
	settings := readSettings()
	esc := map[string]interface{}{}
	if v, ok := settings["escort"].(map[string]interface{}); ok {
		esc = v
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
	s := EscortSettings{
		LeadBot:         getStr(esc, "leadBot", ""),
		HomeSystem:      getStr(esc, "homeSystem", ""),
		RefuelThreshold: getFloat(esc, "refuelThreshold", 25),
		RepairThreshold: getFloat(esc, "repairThreshold", 40),
		AutoCloak:       getBool(esc, "autoCloak", true),
	}
	if username != "" {
		if bots, ok := settings[username].(map[string]interface{}); ok {
			if b, ok := bots["escort"].(map[string]interface{}); ok {
				s.LeadBot = getStr(b, "leadBot", s.LeadBot)
				s.HomeSystem = getStr(b, "homeSystem", s.HomeSystem)
			}
		}
	}
	return s
}

// Escort follows a lead bot and provides combat support.
func Escort(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("escort")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	settings := getEscortSettings(b.Username)
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

	ticker := time.NewTicker(escortInterval)
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
			case <-time.After(escortInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "escort")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(escortInterval):
			}
			continue
		}

		if snap.MaxFuel > 0 && snap.Fuel/snap.MaxFuel < settings.RefuelThreshold/100 {
			if !snap.Docked {
				if err := EnsureDocked(ctx, b); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(escortInterval):
					}
					continue
				}
			}
			if err := EnsureFueled(ctx, b, settings.RefuelThreshold); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(escortInterval):
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
					case <-time.After(escortInterval):
					}
					continue
				}
			}
			if err := RepairShip(ctx, b); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(escortInterval):
				}
				continue
			}
			continue
		}

		// Follow lead bot: travel to lead bot's last known system.
		if settings.LeadBot != "" {
			if settings.HomeSystem != snap.System {
				_, err := NavigateToSystem(ctx, b, settings.HomeSystem)
				if err != nil {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
					continue
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
	Registry["escort"] = Escort
}

