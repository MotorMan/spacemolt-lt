package routines

import (
	"context"
	"encoding/json"
	"time"

	"spacemolt-lt/internal/bot"
	"spacemolt-lt/internal/store"
)

// readSettings reads settings from data/settings.json.
func readSettings() map[string]interface{} {
	s := store.New("data/settings.json")
	var out map[string]interface{}
	if err := s.Read(&out); err != nil || out == nil {
		return map[string]interface{}{}
	}
	return out
}

// GetSystemInfo returns POIs in the bot's current system via get_system_info.
func GetSystemInfo(ctx context.Context, b *bot.Bot) ([]map[string]interface{}, error) {
	snap := b.Snapshot()
	res, err := b.ExecCommand(ctx, "get_system_info", map[string]interface{}{"system": snap.System})
	if err != nil || res == nil {
		return nil, err
	}
	var data struct {
		POIs []map[string]interface{} `json:"pois"`
	}
	if err := json.Unmarshal(res, &data); err != nil {
		return nil, err
	}
	return data.POIs, nil
}

// IsStationPoi checks if a POI is a station.
func IsStationPoi(poi map[string]interface{}) bool {
	t, _ := poi["type"].(string)
	return t == "station" || t == "Station"
}

// FindStation finds any station POI in the list.
func FindStation(pois []map[string]interface{}) map[string]interface{} {
	for _, p := range pois {
		if IsStationPoi(p) {
			return p
		}
	}
	return nil
}

// IsUsableFuelStation checks if a station POI offers fuel.
func IsUsableFuelStation(stationID string, settings map[string]interface{}, system string) bool {
	return true
}

// EnsureDocked docks the bot if not already docked.
func EnsureDocked(ctx context.Context, b *bot.Bot) error {
	snap := b.Snapshot()
	if snap.Docked {
		return nil
	}
	_, err := b.ExecCommand(ctx, "dock", map[string]interface{}{})
	if err != nil {
		return err
	}
	return b.RefreshStatus(ctx)
}

// NavigateToSystem navigates to the target system via the bridge travel command.
func NavigateToSystem(ctx context.Context, b *bot.Bot, targetSystem string) (bool, error) {
	_, err := b.ExecCommand(ctx, "travel", map[string]interface{}{
		"destination": targetSystem,
	})
	if err != nil {
		return false, err
	}
	if err := b.RefreshLocation(ctx); err != nil {
		return false, err
	}
	snap := b.Snapshot()
	return snap.System == targetSystem, nil
}

// EnsureFueled refuels the bot if fuel is below the threshold percentage.
func EnsureFueled(ctx context.Context, b *bot.Bot, threshold float64) error {
	snap := b.Snapshot()
	if snap.MaxFuel <= 0 {
		return nil
	}
	pct := (snap.Fuel / snap.MaxFuel) * 100
	if pct >= threshold {
		return nil
	}
	if !snap.Docked {
		if err := EnsureDocked(ctx, b); err != nil {
			return err
		}
	}
	_, err := b.ExecCommand(ctx, "refuel", map[string]interface{}{})
	if err != nil {
		return err
	}
	return b.RefreshStatus(ctx)
}

// RepairShip repairs the bot's ship via the bridge.
func RepairShip(ctx context.Context, b *bot.Bot) error {
	_, err := b.ExecCommand(ctx, "repair", map[string]interface{}{})
	if err != nil {
		return err
	}
	return b.RefreshStatus(ctx)
}

// CheckAndFleeFromBattle checks if the bot is in battle and attempts to flee.
func CheckAndFleeFromBattle(ctx context.Context, b *bot.Bot, reason string) (bool, error) {
	res, err := b.ExecCommand(ctx, "get_battle", nil)
	if err != nil || res == nil {
		return false, err
	}
	var data struct {
		InBattle bool `json:"inBattle"`
	}
	if err := json.Unmarshal(res, &data); err != nil {
		return false, err
	}
	if !data.InBattle {
		return false, nil
	}
	_, err = b.ExecCommand(ctx, "flee", map[string]interface{}{})
	if err != nil {
		return true, err
	}
	return true, nil
}

// WaitForTransitCompletion waits for the bot's transit to complete.
func WaitForTransitCompletion(ctx context.Context, b *bot.Bot, timeoutSeconds int) (bool, error) {
	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
			if err := b.RefreshStatus(ctx); err != nil {
				return false, err
			}
			snap := b.Snapshot()
			if !snap.InTransit {
				return true, nil
			}
			if time.Now().After(deadline) {
				return false, nil
			}
		}
	}
}

func jsonUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// TraderSettings holds per-bot trading configuration read from settings.json.
type TraderSettings struct {
	HomeSystem          string  `json:"homeSystem"`
	HomeStation         string  `json:"homeStation"`
	MinProfit           float64 `json:"minProfit"`
	MinProfitPct        float64 `json:"minProfitPct"`
	MaxJumpDistance     int     `json:"maxJumpDistance"`
	RefuelThreshold     float64 `json:"refuelThreshold"`
	RepairThreshold     float64 `json:"repairThreshold"`
	AutoCloak           bool    `json:"autoCloak"`
	IgnoreBlacklist     bool    `json:"ignoreBlacklist"`
	MaxCargoUtilization float64 `json:"maxCargoUtilization"`
	TradeItems          []string `json:"tradeItems"`
}

// getTraderSettings reads trader settings for a bot from settings.json.
func getTraderSettings(username string) TraderSettings {
	settings := readSettings()
	trader := map[string]interface{}{}
	if v, ok := settings["trader"].(map[string]interface{}); ok {
		trader = v
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
	s := TraderSettings{
		HomeSystem:          getStr(trader, "homeSystem", ""),
		HomeStation:         getStr(trader, "homeStation", ""),
		MinProfit:           getFloat(trader, "minProfit", 100),
		MinProfitPct:        getFloat(trader, "minProfitPct", 10),
		MaxJumpDistance:     getInt(trader, "maxJumpDistance", 5),
		RefuelThreshold:     getFloat(trader, "refuelThreshold", 25),
		RepairThreshold:     getFloat(trader, "repairThreshold", 40),
		AutoCloak:           getBool(trader, "autoCloak", true),
		IgnoreBlacklist:     getBool(trader, "ignoreBlacklist", false),
		MaxCargoUtilization: getFloat(trader, "maxCargoUtilization", 90),
		TradeItems:          getStringList(trader, "tradeItems"),
	}
	if username != "" {
		if bots, ok := settings[username].(map[string]interface{}); ok {
			if b, ok := bots["trader"].(map[string]interface{}); ok {
				s.HomeSystem = getStr(b, "homeSystem", s.HomeSystem)
				s.HomeStation = getStr(b, "homeStation", s.HomeStation)
				s.MinProfit = getFloat(b, "minProfit", s.MinProfit)
				s.MinProfitPct = getFloat(b, "minProfitPct", s.MinProfitPct)
				s.MaxJumpDistance = getInt(b, "maxJumpDistance", s.MaxJumpDistance)
				s.AutoCloak = getBool(b, "autoCloak", s.AutoCloak)
				s.TradeItems = getStringList(b, "tradeItems")
			}
		}
	}
	return s
}

// MarketPrice represents a buy/sell price for an item at a station.
type MarketPrice struct {
	ItemID    string  `json:"item_id"`
	Name      string  `json:"name"`
	BuyPrice  float64 `json:"buy_price"`
	SellPrice float64 `json:"sell_price"`
	Stock     int     `json:"stock"`
	Demand    int     `json:"demand"`
}

// GetStationMarket returns market prices at a station via get_station_market.
func GetStationMarket(ctx context.Context, b *bot.Bot, stationID string) ([]MarketPrice, error) {
	res, err := b.ExecCommand(ctx, "get_station_market", map[string]interface{}{
		"station_id": stationID,
	})
	if err != nil || res == nil {
		return nil, err
	}
	var data struct {
		Prices []MarketPrice `json:"prices"`
		Result []MarketPrice `json:"result"`
		Market []MarketPrice `json:"market"`
	}
	if err := jsonUnmarshal(res, &data); err != nil {
		return nil, err
	}
	if len(data.Prices) > 0 {
		return data.Prices, nil
	}
	if len(data.Result) > 0 {
		return data.Result, nil
	}
	return data.Market, nil
}

// TradeOp represents a potential trade operation.
type TradeOp struct {
	ItemID     string  `json:"item_id"`
	ItemName   string  `json:"item_name"`
	BuyStation string  `json:"buy_station"`
	BuySystem  string  `json:"buy_system"`
	BuyPrice   float64 `json:"buy_price"`
	SellStation string `json:"sell_station"`
	SellSystem  string  `json:"sell_system"`
	SellPrice  float64 `json:"sell_price"`
	Profit     float64 `json:"profit"`
	ProfitPct  float64 `json:"profit_pct"`
	Quantity   int     `json:"quantity"`
}

// FindProfitableTrade scans markets for profitable trade opportunities.
func FindProfitableTrade(ctx context.Context, b *bot.Bot, settings TraderSettings) (*TradeOp, error) {
	// Get current system's stations
	pois, err := GetSystemPOIs(ctx, b)
	if err != nil || len(pois) == 0 {
		return nil, err
	}
	stations := []map[string]interface{}{}
	for _, p := range pois {
		if IsStationPoi(p) {
			stations = append(stations, p)
		}
	}

	snap := b.Snapshot()
	bestOp := &TradeOp{}
	found := false

	itemsToCheck := settings.TradeItems
	if len(itemsToCheck) == 0 {
		itemsToCheck = []string{"fuel_cell", "mining_laser", "shield_generator", "armor_plate"}
	}

	for _, itemID := range itemsToCheck {
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
				// Look for items cheap to buy
				if price.BuyPrice > 0 && price.Stock > 0 {
					// Simple heuristic: buy low, we'll find sell later
					if !found || price.BuyPrice < bestOp.BuyPrice {
						bestOp = &TradeOp{
							ItemID:     price.ItemID,
							ItemName:   price.Name,
							BuyStation: stationID,
							BuySystem:  snap.System,
							BuyPrice:   price.BuyPrice,
							Quantity:   price.Stock,
						}
						found = true
					}
				}
			}
		}
	}

	if !found {
		return nil, nil
	}
	return bestOp, nil
}

// MinerSettings holds per-bot mining configuration read from settings.json.
type MinerSettings struct {
	HomeSystem             string  `json:"homeSystem"`
	HomeStation            string  `json:"homeStation"`
	CargoThreshold         float64 `json:"cargoThreshold"`
	RefuelThreshold        float64 `json:"refuelThreshold"`
	RepairThreshold        float64 `json:"repairThreshold"`
	EnableCloak            bool    `json:"enableCloak"`
	CloakIgnoreBlacklist   bool    `json:"cloakIgnoreBlacklist"`
	EnableCoordination     bool    `json:"enableCoordination"`
	DepositMode            string  `json:"depositMode"`
	DepositBot             string  `json:"depositBot"`
	ReturnHomeFuelCells    int     `json:"returnHomeOnFuelCellsRemaining"`
	EnableMissions         bool    `json:"enableMissions"`
	FlockEnabled           bool    `json:"flockEnabled"`
	FlockName              string  `json:"flockName"`
}

// getMinerSettings reads miner settings for a bot from settings.json.
func getMinerSettings(username string) MinerSettings {
	settings := readSettings()
	miner := map[string]interface{}{}
	if v, ok := settings["miner"].(map[string]interface{}); ok {
		miner = v
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
	s := MinerSettings{
		HomeSystem:          getStr(miner, "homeSystem", ""),
		HomeStation:         getStr(miner, "homeStation", ""),
		CargoThreshold:      getFloat(miner, "cargoThreshold", 85),
		RefuelThreshold:     getFloat(miner, "refuelThreshold", 25),
		RepairThreshold:     getFloat(miner, "repairThreshold", 40),
		EnableCloak:         getBool(miner, "enableCloak", true),
		CloakIgnoreBlacklist:getBool(miner, "cloakIgnoreBlacklist", false),
		EnableCoordination:  getBool(miner, "enableCoordination", false),
		DepositMode:         getStr(miner, "depositMode", "storage"),
		DepositBot:          getStr(miner, "depositBot", ""),
		ReturnHomeFuelCells: getInt(miner, "returnHomeOnFuelCellsRemaining", 2),
		EnableMissions:      getBool(miner, "enableMissions", false),
		FlockEnabled:        getBool(miner, "flockEnabled", false),
		FlockName:           getStr(miner, "flockName", ""),
	}
	// Per-bot override
	if username != "" {
		if bots, ok := settings[username].(map[string]interface{}); ok {
			if b, ok := bots["miner"].(map[string]interface{}); ok {
				s.HomeSystem = getStr(b, "homeSystem", s.HomeSystem)
				s.HomeStation = getStr(b, "homeStation", s.HomeStation)
				s.CargoThreshold = getFloat(b, "cargoThreshold", s.CargoThreshold)
				s.RefuelThreshold = getFloat(b, "refuelThreshold", s.RefuelThreshold)
				s.RepairThreshold = getFloat(b, "repairThreshold", s.RepairThreshold)
				s.EnableCloak = getBool(b, "enableCloak", s.EnableCloak)
				s.DepositMode = getStr(b, "depositMode", s.DepositMode)
				s.DepositBot = getStr(b, "depositBot", s.DepositBot)
			}
		}
	}
	return s
}

// CargoItem represents a single cargo stack.
type CargoItem struct {
	ItemID   string `json:"item_id"`
	Quantity int    `json:"quantity"`
	Name     string `json:"name"`
}

// GetCargo returns the bot's current cargo via get_cargo.
func GetCargo(ctx context.Context, b *bot.Bot) ([]CargoItem, error) {
	res, err := b.ExecCommand(ctx, "get_cargo", nil)
	if err != nil || res == nil {
		return nil, err
	}
	var data struct {
		Cargo []CargoItem `json:"cargo"`
		Items []CargoItem `json:"items"`
	}
	if err := json.Unmarshal(res, &data); err != nil {
		return nil, err
	}
	if len(data.Cargo) > 0 {
		return data.Cargo, nil
	}
	return data.Items, nil
}

// GetShip returns ship info including modules and cargo capacity.
func GetShip(ctx context.Context, b *bot.Bot) (map[string]interface{}, error) {
	res, err := b.ExecCommand(ctx, "get_ship", nil)
	if err != nil || res == nil {
		return nil, err
	}
	var data struct {
		Ship map[string]interface{} `json:"ship"`
		Result map[string]interface{} `json:"result"`
	}
	if err := json.Unmarshal(res, &data); err != nil {
		return nil, err
	}
	if len(data.Ship) > 0 {
		return data.Ship, nil
	}
	return data.Result, nil
}

// GetNearby returns POIs near the bot via get_nearby.
func GetNearby(ctx context.Context, b *bot.Bot) ([]map[string]interface{}, error) {
	res, err := b.ExecCommand(ctx, "get_nearby", nil)
	if err != nil || res == nil {
		return nil, err
	}
	var data struct {
		POIs []map[string]interface{} `json:"pois"`
		Result []map[string]interface{} `json:"result"`
		Nearby []map[string]interface{} `json:"nearby"`
	}
	if err := json.Unmarshal(res, &data); err != nil {
		return nil, err
	}
	if len(data.POIs) > 0 {
		return data.POIs, nil
	}
	if len(data.Result) > 0 {
		return data.Result, nil
	}
	return data.Nearby, nil
}

// DepositCargo deposits all non-fuel cargo into station storage.
func DepositCargo(ctx context.Context, b *bot.Bot, settings MinerSettings) error {
	cargo, err := GetCargo(ctx, b)
	if err != nil {
		return err
	}
	for _, item := range cargo {
		if item.Quantity <= 0 {
			continue
		}
	params := map[string]interface{}{
			"item_id":   item.ItemID,
			"quantity":  item.Quantity,
		}
		switch settings.DepositMode {
		case "sell":
			_, err = b.ExecCommand(ctx, "sell", map[string]interface{}{"id": item.ItemID, "quantity": item.Quantity})
		case "faction":
			params["target"] = "faction"
			_, err = b.ExecCommand(ctx, "spacemolt_storage.deposit", params)
		case "gift":
			if settings.DepositBot != "" {
				params["target"] = settings.DepositBot
				_, err = b.ExecCommand(ctx, "spacemolt_storage.withdraw", params)
			} else {
				_, err = b.ExecCommand(ctx, "spacemolt_storage.deposit", params)
			}
		default:
			_, err = b.ExecCommand(ctx, "spacemolt_storage.deposit", params)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// CargoFullness returns cargo used / max capacity, or -1 if unknown.
func CargoFullness(b *bot.Bot) (float64, bool) {
	snap := b.Snapshot()
	if snap.MaxFuel <= 0 && snap.Cargo == 0 {
		return 0, false
	}
	// snap.Cargo is total cargo used (float64).
	return snap.Cargo, true
}

