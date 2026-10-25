package routines

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"spacemolt-lt/internal/ai"
	"spacemolt-lt/internal/bot"
	"spacemolt-lt/internal/store"
)

const (
	aiCycleInterval = 10 * time.Second
	aiMemoryFile    = "data/ai_memory.json"
)

// AISettings holds per-bot AI configuration.
type AISettings struct {
	Model               string  `json:"model"`
	BaseURL             string  `json:"baseUrl"`
	APIKey              string  `json:"apiKey"`
	CycleIntervalSec    int     `json:"cycleIntervalSec"`
	MaxToolCallsPerCycle int    `json:"maxToolCallsPerCycle"`
	CaptainsLogEveryN   int     `json:"captainsLogEveryN"`
}

func getAISettings() AISettings {
	settings := readSettings()
	ai := map[string]interface{}{}
	if v, ok := settings["ai"].(map[string]interface{}); ok {
		ai = v
	}
	getStr := func(m map[string]interface{}, key, def string) string {
		if v, ok := m[key].(string); ok && v != "" {
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
	return AISettings{
		Model:               getStr(ai, "model", "llama3.2"),
		BaseURL:             getStr(ai, "baseUrl", "http://localhost:11434/v1"),
		APIKey:              getStr(ai, "apiKey", "ollama"),
		CycleIntervalSec:    getInt(ai, "cycleIntervalSec", 10),
		MaxToolCallsPerCycle: getInt(ai, "maxToolCallsPerCycle", 40),
		CaptainsLogEveryN:   getInt(ai, "captainsLogEveryN", 5),
	}
}

// AIMemory is the persistent AI memory.
type AIMemory struct {
	Version     int       `json:"version"`
	LastCycle   string    `json:"lastCycle"`
	CycleCount  int       `json:"cycleCount"`
	Goals       []string  `json:"goals"`
	Insights    []string  `json:"insights"`
	Decisions   []AIDecision `json:"decisions"`
}

type AIDecision struct {
	Timestamp string `json:"timestamp"`
	Action    string `json:"action"`
	Reason    string `json:"reason"`
}

func loadAIMemory() AIMemory {
	s := store.New(aiMemoryFile)
	var m AIMemory
	if err := s.Read(&m); err != nil || m.Version == 0 {
		return AIMemory{
			Version:    1,
			Goals:      []string{"Play SpaceMolt effectively", "Earn credits and develop skills"},
			Insights:   []string{},
			Decisions:  []AIDecision{},
		}
	}
	return m
}

func saveAIMemory(m AIMemory) error {
	s := store.New(aiMemoryFile)
	return s.Write(m)
}

// AI is the LLM-driven bot routine.
func AI(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("ai")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)

	settings := getAISettings()
	if settings.Model == "" {
		settings.Model = "llama3.2"
	}

	client := ai.NewClient(settings.BaseURL, settings.Model, settings.APIKey)
	memory := loadAIMemory()
	cycleCount := memory.CycleCount

	systemPrompt := `You are an autonomous SpaceMolt bot pilot. You control a spaceship in a multiplayer space MMO.

Available game commands (executed via exec tool):
- get_status: Get current bot status (location, ship, fuel, hull, credits, cargo)
- get_cargo: Get cargo contents
- get_ship: Get ship info including modules
- get_system_info: Get POIs in current system
- get_nearby: Get nearby entities
- travel: Travel to a POI ({target_poi: string})
- jump: Jump to a system ({target_system: string})
- dock: Dock at current station
- undock: Undock from station
- refuel: Refuel ship
- repair: Repair ship
- mine: Mine at current POI
- buy: Buy items ({id: string, quantity: number})
- sell: Sell items ({id: string, quantity: number})
- cloak: Enable/disable cloak ({enable: boolean})
- flee: Flee from battle

Game loop: undock → travel to mining belt → mine → travel to station → dock → sell → refuel → repeat.

You must observe the bot's current state, decide on the next action, and execute it. Be concise.`

	tools := []ai.ToolDefinition{
		{
			Type: "function",
			Function: ai.FunctionDefinition{
				Name:        "exec",
				Description: "Execute a game command",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{"type": "string", "description": "Command name (get_status, mine, travel, dock, etc.)"},
						"params": map[string]interface{}{"type": "object", "description": "Command parameters"},
					},
					"required": []interface{}{"command"},
				},
			},
		},
	}

	ticker := time.NewTicker(time.Duration(settings.CycleIntervalSec) * time.Second)
	defer ticker.Stop()

	// Initial status refresh.
	_ = b.RefreshStatus(ctx)

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
			case <-time.After(aiCycleInterval):
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

		inBattle, _ := CheckAndFleeFromBattle(ctx, b, "ai")
		if inBattle {
			select {
			case <-ctx.Done():
				return
			case <-time.After(aiCycleInterval):
			}
			continue
		}

		// Build conversation messages.
		stateDesc := fmt.Sprintf("System: %s, POI: %s, Docked: %v, Cloaked: %v, Fuel: %.0f/%.0f, Hull: %.0f/%.0f, Credits: %.0f",
			snap.System, snap.POI, snap.Docked, snap.IsCloaked, snap.Fuel, snap.MaxFuel, snap.Hull, snap.MaxHull, snap.Credits)

		messages := []ai.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: fmt.Sprintf("Current state: %s\nWhat should I do next? Pick one command.", stateDesc)},
		}

		resp, err := client.Chat(ctx, messages, tools)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(aiCycleInterval):
			}
			continue
		}

		// Handle tool calls.
		if len(resp.ToolCalls) > 0 {
			for _, tc := range resp.ToolCalls {
				if tc.Function.Name != "exec" {
					continue
				}
				var params map[string]interface{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &params)
				command, _ := params["command"].(string)
				cp, _ := params["params"].(map[string]interface{})
				if command == "" {
					continue
				}
				_, err := b.ExecCommand(ctx, command, cp)
				if err != nil {
					_, _ = b.ExecCommand(ctx, "log", map[string]interface{}{
						"level": "warn",
						"message": "AI: " + command + " failed: " + err.Error(),
					})
				}
				cycleCount++
				memory.Decisions = append(memory.Decisions, AIDecision{
					Timestamp: fmt.Sprintf("%d", time.Now().Unix()),
					Action:    command,
					Reason:    "LLM decision",
				})
				if len(memory.Decisions) > 100 {
					memory.Decisions = memory.Decisions[len(memory.Decisions)-100:]
				}
				_ = saveAIMemory(memory)
			}
		}

		cycleCount++
		memory.CycleCount = cycleCount
		memory.LastCycle = fmt.Sprintf("%d", time.Now().Unix())
		_ = saveAIMemory(memory)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func init() {
	Registry["ai"] = AI
}

