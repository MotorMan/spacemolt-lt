package routines

import (
	"context"
	"encoding/json"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	returnHomeMaxWaitAttempts = 5
	returnHomeWaitInterval    = 2 * time.Second
	returnHomeHullRepairPct   = 95
)

// ReturnHome navigates the bot to its home system, docks, repairs, and refuels.
func ReturnHome(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("return_home")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	// Wait for any pending action from previous routine to clear.
	for attempt := 0; attempt < returnHomeMaxWaitAttempts; attempt++ {
		if err := b.RefreshStatus(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(returnHomeWaitInterval):
		}
	}

	// Check for ongoing transit before starting.
	snap := b.Snapshot()
	if snap.InTransit {
		ok, _ := WaitForTransitCompletion(ctx, b, 180)
		if !ok {
			return
		}
		_ = b.RefreshStatus(ctx)
	}

	// Read home settings.
	settings := readSettings()
	returnHome := map[string]interface{}{}
	if v, ok := settings["return_home"].(map[string]interface{}); ok {
		returnHome = v
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

	homeSystem := getStr(returnHome, "homeSystem", "sol")
	homeStation := getStr(returnHome, "homeStation", "")
	refuelThreshold := getFloat(returnHome, "refuelThreshold", 50)
	enableCloak := getBool(returnHome, "enableCloak", true)
	decloakBeforeDock := getBool(returnHome, "decloakBeforeDock", false)

	if homeSystem == "" {
		return
	}

	// Read current state.
	_ = b.RefreshStatus(ctx)
	snap = b.Snapshot()
	atHomeSys := snap.System == homeSystem
	atHomeStation := homeStation != "" && snap.POI == homeStation
	docked := snap.Docked
	isCloaked := snap.IsCloaked

	// If already at home station, handle dock/repair and complete.
	if atHomeSys {
		if atHomeStation && !docked {
			if isCloaked && decloakBeforeDock {
				_ = decloakAndSettle(ctx, b)
			}
			if err := EnsureDocked(ctx, b); err != nil {
				return
			}
		}
		if homeStation == "" && docked {
			return
		}
		if atHomeStation {
			if snap.MaxHull > 0 && snap.Hull/snap.MaxHull < returnHomeHullRepairPct/100 {
				_ = RepairShip(ctx, b)
			}
			if snap.MaxFuel > 0 && snap.Fuel/snap.MaxFuel < refuelThreshold/100 {
				_ = EnsureFueled(ctx, b, refuelThreshold)
			}
			return
		}
	}

	// Enable cloaking if configured.
	if enableCloak && !isCloaked {
		_ = enableCloakingIfPossible(ctx, b)
		snap = b.Snapshot()
		isCloaked = snap.IsCloaked
	}

	// Battle check before starting.
	inBattle, _ := CheckAndFleeFromBattle(ctx, b, "return_home")
	if inBattle {
		return
	}

	// Navigate to home system.
	if snap.System != homeSystem {
		_, _ = CheckAndFleeFromBattle(ctx, b, "return_home")
		arrived, err := NavigateToSystem(ctx, b, homeSystem)
		if err != nil || !arrived {
			return
		}
	}

	// Find and travel to home station.
	pois, err := GetSystemInfo(ctx, b)
	if err != nil {
		return
	}
	var targetStation map[string]interface{}
	if homeStation != "" {
		for _, p := range pois {
			if id, ok := p["id"].(string); ok && id == homeStation && IsStationPoi(p) {
				targetStation = p
				break
			}
		}
	}
	if targetStation == nil {
		targetStation = FindStation(pois)
	}
	if targetStation == nil {
		return
	}

	stationID, _ := targetStation["id"].(string)
	snap = b.Snapshot()
	if snap.POI != stationID {
		_, err := b.ExecCommand(ctx, "travel", map[string]interface{}{
			"target_poi": stationID,
		})
		if err != nil {
			return
		}
		_ = b.RefreshStatus(ctx)
		snap = b.Snapshot()
		if snap.POI != stationID {
			return
		}
	}

	// Decloak before docking if configured.
	if isCloaked && decloakBeforeDock {
		_ = decloakAndSettle(ctx, b)
	}

	// Dock at station.
	if err := EnsureDocked(ctx, b); err != nil {
		return
	}

	// Repair and refuel after docking.
	_ = b.RefreshStatus(ctx)
	snap = b.Snapshot()
	if snap.MaxHull > 0 && snap.Hull/snap.MaxHull < returnHomeHullRepairPct/100 {
		_ = RepairShip(ctx, b)
	}
	if snap.MaxFuel > 0 && snap.Fuel/snap.MaxFuel < refuelThreshold/100 {
		_ = EnsureFueled(ctx, b, refuelThreshold)
	}
}

func decloakAndSettle(ctx context.Context, b *bot.Bot) error {
	snap := b.Snapshot()
	if !snap.IsCloaked {
		return nil
	}
	_, err := b.ExecCommand(ctx, "cloak", map[string]interface{}{"enable": false})
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		if err := b.RefreshStatus(ctx); err != nil {
			continue
		}
		snap = b.Snapshot()
		if !snap.IsCloaked {
			break
		}
	}
	b.SetDocked(false)
	return nil
}

func enableCloakingIfPossible(ctx context.Context, b *bot.Bot) error {
	snap := b.Snapshot()
	if snap.IsCloaked {
		return nil
	}
	res, err := b.ExecCommand(ctx, "cloak", map[string]interface{}{"enable": true})
	if err != nil || res == nil {
		return err
	}
	var data struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(res, &data); err != nil {
		return err
	}
	if data.Error != "" {
		b.SetCloaked(true)
		b.SetDocked(false)
	}
	return nil
}

func init() {
	Registry["return_home"] = ReturnHome
}

