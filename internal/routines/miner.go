package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

const (
	minerCycleInterval = 5 * time.Second
	minerMineInterval  = 3 * time.Second
)

// isMiningPoi checks if a POI is a mineable resource (ore belt, gas cloud, ice field).
func isMiningPoi(poi map[string]interface{}) bool {
	t, _ := poi["type"].(string)
	switch t {
	case "ore_belt", "OreBelt", "gas_cloud", "GasCloud", "ice_field", "IceField", "asteroid_belt", "AsteroidBelt":
		return true
	}
	name, _ := poi["name"].(string)
	lname := ""
	for _, c := range name {
		if c >= 'A' && c <= 'Z' {
			lname += string(c + 32)
		} else {
			lname += string(c)
		}
	}
	for _, kw := range []string{"ore", "gas", "ice", "asteroid", "belt", "cloud", "field"} {
		if contains(lname, kw) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// Miner is the core mining routine: find ore -> travel -> mine -> deposit -> refuel/repair -> repeat.
func Miner(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("miner")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	settings := getMinerSettings(b.Username)
	if settings.HomeSystem == "" {
		settings.HomeSystem = b.System
	}

	// Register chat handler for escort location queries (no-op stub - hook into bot chat channel later)
	_ = settings

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
	if settings.EnableCloak && !snap.IsCloaked {
		_ = enableCloakingIfPossible(ctx, b)
	}

	mineTicker := time.NewTicker(minerMineInterval)
	defer mineTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !b.IsRunning() {
			return
		}

		// Refresh status each cycle.
		if err := b.RefreshStatus(ctx); err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(minerCycleInterval):
			}
			continue
		}

		snap = b.Snapshot()

		// If in transit, wait.
		if snap.InTransit {
			ok, _ := WaitForTransitCompletion(ctx, b, 180)
			if !ok {
				return
			}
			_ = b.RefreshStatus(ctx)
			continue
		}

		// Check battle and flee if needed.
		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "miner")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(minerCycleInterval):
			}
			continue
		}

		// If cargo is near threshold, go deposit.
		if snap.MaxFuel > 0 && shouldDeposit(ctx, b, settings) {
			if err := handleReturnAndDeposit(ctx, b, settings); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(minerCycleInterval):
				}
				continue
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
					case <-time.After(minerCycleInterval):
					}
					continue
				}
			}
			if err := EnsureFueled(ctx, b, settings.RefuelThreshold); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(minerCycleInterval):
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
					case <-time.After(minerCycleInterval):
					}
					continue
				}
			}
			if err := RepairShip(ctx, b); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(minerCycleInterval):
				}
				continue
			}
			continue
		}

		// Find a mining POI nearby.
		pois, err := GetNearby(ctx, b)
		if err != nil || len(pois) == 0 {
			// No nearby POIs - try system info.
			pois, err = GetSystemPOIs(ctx, b)
			if err != nil || len(pois) == 0 {
				select {
				case <-ctx.Done():
					return
				case <-mineTicker.C:
				}
				continue
			}
		}

		var target map[string]interface{}
		for _, p := range pois {
			if isMiningPoi(p) {
				target = p
				break
			}
		}

		if target == nil {
			// No mineable POI nearby - wait and rescan.
			select {
			case <-ctx.Done():
				return
			case <-mineTicker.C:
			}
			continue
		}

		poiID, _ := target["id"].(string)
		poiType, _ := target["type"].(string)
		poiSystem, _ := target["system"].(string)
		if poiSystem == "" {
			poiSystem = snap.System
		}

		// If not at the target POI, travel there.
		if snap.POI != poiID || snap.System != poiSystem {
			if poiSystem != snap.System {
				_, err := NavigateToSystem(ctx, b, poiSystem)
				if err != nil {
					select {
					case <-ctx.Done():
						return
					case <-mineTicker.C:
					}
					continue
				}
			}
			_, err := b.ExecCommand(ctx, "travel", map[string]interface{}{
				"target_poi": poiID,
			})
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-mineTicker.C:
				}
				continue
			}
			_ = b.RefreshStatus(ctx)
			continue
		}

		// At the mining POI - mine.
		if !snap.Docked {
			_, err := b.ExecCommand(ctx, "mine", map[string]interface{}{
				"target_poi": poiID,
				"type":       poiType,
			})
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-mineTicker.C:
				}
				continue
			}
			_ = b.RefreshStatus(ctx)
			_ = b.RefreshCargo(ctx)
		}

		select {
		case <-ctx.Done():
			return
		case <-mineTicker.C:
		}
	}
}

// shouldDeposit returns true if cargo is at/above the threshold.
func shouldDeposit(ctx context.Context, b *bot.Bot, settings MinerSettings) bool {
	snap := b.Snapshot()
	if snap.MaxFuel <= 0 {
		return false
	}
	// Use cargo count as a proxy for fullness if cargoMax isn't available.
	total := 0
	for _, q := range snap.Cargo {
		total += q
	}
	// If we have any cargo and are docked, deposit.
	if total > 0 && snap.Docked {
		return true
	}
	// Heuristic: deposit when cargo count exceeds a rough threshold.
	// The bridge status should provide cargoMax; without it, use a default of 100 stacks.
	const defaultMaxStacks = 100
	if total >= int(float64(defaultMaxStacks)*settings.CargoThreshold/100) {
		return true
	}
	return false
}

// handleReturnAndDeposit navigates to home system, docks, and deposits cargo.
func handleReturnAndDeposit(ctx context.Context, b *bot.Bot, settings MinerSettings) error {
	snap := b.Snapshot()
	if snap.System != settings.HomeSystem {
		if _, err := NavigateToSystem(ctx, b, settings.HomeSystem); err != nil {
			return err
		}
		_ = b.RefreshStatus(ctx)
		snap = b.Snapshot()
	}

	// Find a station in the current system.
	pois, err := GetSystemPOIs(ctx, b)
	if err != nil || len(pois) == 0 {
		return err
	}
	var station map[string]interface{}
	if settings.HomeStation != "" {
		for _, p := range pois {
			if id, ok := p["id"].(string); ok && id == settings.HomeStation && IsStationPoi(p) {
				station = p
				break
			}
		}
	}
	if station == nil {
		station = FindStation(pois)
	}
	if station == nil {
		return nil
	}
	stationID, _ := station["id"].(string)

	snap = b.Snapshot()
	if snap.POI != stationID {
		_, err := b.ExecCommand(ctx, "travel", map[string]interface{}{
			"target_poi": stationID,
		})
		if err != nil {
			return err
		}
		_ = b.RefreshStatus(ctx)
	}

	if err := EnsureDocked(ctx, b); err != nil {
		return err
	}

	if err := DepositCargo(ctx, b, settings); err != nil {
		return err
	}

	return nil
}

// GetSystemPOIs returns POIs in the bot's current system.
func GetSystemPOIs(ctx context.Context, b *bot.Bot) ([]map[string]interface{}, error) {
	snap := b.Snapshot()
	res, err := b.ExecCommand(ctx, "get_system_info", map[string]interface{}{"system": snap.System})
	if err != nil || res == nil {
		return nil, err
	}
	var data struct {
		POIs []map[string]interface{} `json:"pois"`
	}
	if err := jsonUnmarshal(res, &data); err != nil {
		return nil, err
	}
	return data.POIs, nil
}

func init() {
	Registry["miner"] = Miner
}

