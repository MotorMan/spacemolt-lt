package web

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"spacemolt-lt/embed"
	"spacemolt-lt/internal/bot"
	"spacemolt-lt/internal/botmanager"
	"spacemolt-lt/internal/bridge"
	"spacemolt-lt/internal/routines"
	"spacemolt-lt/internal/store"
	ssync "spacemolt-lt/internal/sync"
)

type Hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]bool
}

func NewHub() *Hub { return &Hub{clients: make(map[*websocket.Conn]bool)} }

type Server struct {
	Manager  botMgr
	Bridge   bridge.Caller
	Settings *store.FileStore
	Sync     *ssync.Client
	Hub      *Hub
	up       websocket.Upgrader

	cancel    context.CancelFunc
	shutdownCh chan struct{}
}

// botMgr is the subset of botmanager.Manager used by the web server, extracted
// as an interface so the dispatch handlers can be unit-tested with mocks.
type botMgr interface {
	ListBots() []*bot.Bot
	GetBot(id string) *bot.Bot
	GetBotByAccount(accountID string) *bot.Bot
	AddBot(b *bot.Bot)
	RemoveBot(id string)
	StartBot(ctx context.Context, b *bot.Bot, delay time.Duration)
}

func New(m *botmanager.Manager, bc *bridge.Client, settings *store.FileStore, sc *ssync.Client, cancel context.CancelFunc) *Server {
	return &Server{
		Manager:     m,
		Bridge:      bc,
		Settings:    settings,
		Sync:        sc,
		Hub:         NewHub(),
		up:          websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
		cancel:      cancel,
		shutdownCh:  make(chan struct{}, 1),
	}
}

func (s *Server) RegisterRoutes() {
	http.HandleFunc("/ws", s.handleWS)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "bots": len(s.Manager.ListBots())})
	})
	// Sync API endpoints
	http.HandleFunc("/api/client-sync/hello", s.handleSyncHello)
	http.HandleFunc("/api/client-sync/heartbeat", s.handleSyncHeartbeat)
	http.HandleFunc("/api/client-sync/catalog-version", s.handleSyncCatalogVersion)
	http.HandleFunc("/api/client-sync/catalog-state", s.handleSyncCatalogState)
	http.HandleFunc("/api/client-sync/market-query", s.handleSyncMarketQuery)
	http.HandleFunc("/api/client-sync/bot-status", s.handleSyncBotStatus)
	// Shutdown / restart endpoint
	http.HandleFunc("/api/shutdown", s.handleShutdown)
	// Settings & data endpoints
	http.HandleFunc("/api/settings", s.handleSettings)
	http.HandleFunc("/api/bots", s.handleBots)
	http.HandleFunc("/api/bots/discovered", s.handleBotsDiscovered)
	http.HandleFunc("/api/routines", s.handleRoutines)
	http.HandleFunc("/api/flock/settings", s.handleFlockSettings)
	http.HandleFunc("/api/stats", s.handleStats)
	http.HandleFunc("/api/catalog", s.handleCatalog)
	http.HandleFunc("/api/logs/main", s.handleLogsMain)
	http.HandleFunc("/api/faction-storage", s.handleFactionStorage)
	http.HandleFunc("/api/last-used-routines", s.handleLastUsedRoutines)
	http.HandleFunc("/api/taxes", s.handleTaxes)
	http.HandleFunc("/api/faction-tax-estimate", s.handleFactionTaxEstimate)
	http.HandleFunc("/api/map/register-poi", s.handleMapRegisterPOI)
	http.HandleFunc("/api/map/register-wormhole", s.handleMapRegisterWormhole)
	http.HandleFunc("/api/ship/bulk-move", s.handleShipBulkMove)
	http.HandleFunc("/api/ship/apply-preset", s.handleShipApplyPreset)
	http.HandleFunc("/api/crafting-loadouts", s.handleCraftingLoadouts)
	http.HandleFunc("/api/ship-loadouts", s.handleShipLoadouts)
	http.HandleFunc("/api/module-loadouts", s.handleModuleLoadouts)
	// Metrics endpoint
	http.HandleFunc("/metrics", s.handleMetrics)
	// Static assets served from embedded web directory at root
	sub, _ := fs.Sub(embed.FS, "web")
	http.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(sub))))
	http.Handle("/", http.FileServer(http.FS(sub)))
	// pprof auto-registered on DefaultServeMux via blank import
}

type wsMessage struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data,omitempty"`
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.Hub.mu.Lock()
	s.Hub.clients[conn] = true
	s.Hub.mu.Unlock()

	var writeMu sync.Mutex

	// Send init message on connect (matches TS server behavior)
	go func() {
		var settings map[string]interface{}
		if s.Settings != nil {
			_ = s.Settings.Read(&settings)
		}
		if settings == nil {
			settings = map[string]interface{}{}
		}
		bots := make([]string, 0, len(s.Manager.ListBots()))
		for _, b := range s.Manager.ListBots() {
			bots = append(bots, b.ID)
		}
		routineNames := make([]string, 0, len(routines.Registry))
		for name := range routines.Registry {
			routineNames = append(routineNames, name)
		}
		initPayload := map[string]interface{}{
			"type":     "init",
			"bots":     bots,
			"routines": routineNames,
			"settings": settings,
		}
		b, _ := json.Marshal(initPayload)
		writeMu.Lock()
		_ = conn.WriteMessage(websocket.TextMessage, b)
		writeMu.Unlock()
	}()

	write := func(mt int, data []byte) {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.WriteMessage(mt, data)
	}

	defer func() {
		s.Hub.mu.Lock()
		delete(s.Hub.clients, conn)
		s.Hub.mu.Unlock()
		_ = conn.Close()
	}()

	// reader
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			log.Printf("WS raw: %s", string(msg))
			// Frontend sends everything at top level (type + routine + settings + etc.),
			// not wrapped in a "data" field. Capture all non-type fields into Data.
			var raw map[string]interface{}
			_ = json.Unmarshal(msg, &raw)
			actionType, _ := raw["type"].(string)
			data := map[string]interface{}{}
			for k, v := range raw {
				if k != "type" {
					data[k] = v
				}
			}
			action := wsMessage{Type: actionType, Data: data}
			resp := s.dispatch(action)
			// Wrap response as actionResult (matching TS server format)
			wrapped := map[string]interface{}{
				"type":   "actionResult",
				"action": actionType,
			}
			for k, v := range resp {
				wrapped[k] = v
			}
			b, _ := json.Marshal(wrapped)
			write(websocket.TextMessage, b)
		}
	}()

	// 250ms coalesced push
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		botObjs := make([]map[string]interface{}, 0, len(s.Manager.ListBots()))
		for _, b := range s.Manager.ListBots() {
			botObjs = append(botObjs, botToObj(b))
		}
		// Sort by username so the dashboard table is stable across pushes
		// (Go map iteration order is randomized).
		sort.Slice(botObjs, func(i, j int) bool {
			return botObjs[i]["username"].(string) < botObjs[j]["username"].(string)
		})
		payload := map[string]interface{}{
			"type": "status",
			"bots": botObjs,
		}
		b, _ := json.Marshal(payload)
		write(websocket.TextMessage, b)
	}
}

func (s *Server) dispatch(action wsMessage) map[string]interface{} {
	switch action.Type {
	case "ping":
		return map[string]interface{}{"ok": true, "pong": true}
	case "init":
		var settings map[string]interface{}
		if s.Settings != nil {
			_ = s.Settings.Read(&settings)
		}
		if settings == nil {
			settings = map[string]interface{}{}
		}
		botObjs := make([]map[string]interface{}, 0, len(s.Manager.ListBots()))
		for _, b := range s.Manager.ListBots() {
			botObjs = append(botObjs, botToObj(b))
		}
		// Sort by username so the dashboard table is stable across pushes.
		sort.Slice(botObjs, func(i, j int) bool {
			return botObjs[i]["username"].(string) < botObjs[j]["username"].(string)
		})
		routineNames := make([]string, 0, len(routines.Registry))
		for name := range routines.Registry {
			routineNames = append(routineNames, name)
		}
		return map[string]interface{}{
			"ok":       true,
			"settings": settings,
			"bots":     botObjs,
			"routines": routineNames,
		}
	case "listBots":
		ids := []string{}
		for _, b := range s.Manager.ListBots() {
			ids = append(ids, b.ID)
		}
		return map[string]interface{}{"ok": true, "bots": ids}
	case "start":
		botID, _ := action.Data["bot"].(string)
		routine, _ := action.Data["routine"].(string)
		b := s.Manager.GetBot(botID)
		if b == nil {
			return map[string]interface{}{"ok": false, "error": "bot not found"}
		}
		if routine == "" {
			routine = "idle"
		}
		b.SetRoutine(routine)
		s.Manager.StartBot(context.Background(), b, 0)
		return map[string]interface{}{"ok": true}
	case "stop":
		botID, _ := action.Data["bot"].(string)
		s.Manager.RemoveBot(botID)
		return map[string]interface{}{"ok": true}
	case "exec":
		botID, _ := action.Data["bot"].(string)
		b := s.Manager.GetBot(botID)
		if b == nil {
			return map[string]interface{}{"ok": false, "error": "bot not found"}
		}
		command, _ := action.Data["command"].(string)
		params, _ := action.Data["params"].(map[string]interface{})
		if s.Bridge == nil {
			return map[string]interface{}{"ok": false, "error": "no bridge"}
		}
		res, err := s.Bridge.Call(context.Background(), "exec", map[string]interface{}{"id": b.AccountID, "command": command, "params": params})
		if err != nil {
			return map[string]interface{}{"ok": false, "error": err.Error()}
		}
		return map[string]interface{}{"ok": true, "data": json.RawMessage(res)}
	case "saveSettings":
		if s.Settings != nil {
			var cur map[string]interface{}
			_ = s.Settings.Read(&cur)
			if cur == nil {
				cur = map[string]interface{}{}
			}
			routine, _ := action.Data["routine"].(string)
			settings := action.Data["settings"]
			if routine != "" && settings != nil {
				cur[routine] = settings
				log.Printf("saveSettings: routine=%s settings=%v", routine, settings)
			}
			// Handle clerkSelection (from general settings save)
			if cs, ok := action.Data["clerkSelection"].(map[string]interface{}); ok {
				checked, _ := cs["checked"].([]interface{})
				bots := make([]string, 0, len(checked))
				for _, c := range checked {
					if s, ok := c.(string); ok {
						bots = append(bots, s)
					}
				}
				clerkObj, _ := cur["clerk"].(map[string]interface{})
				if clerkObj == nil {
					clerkObj = map[string]interface{}{}
				}
				clerkObj["bots"] = bots
				cur["clerk"] = clerkObj
			}
			if routine == "" {
				log.Printf("saveSettings: empty routine, data=%v", action.Data)
				// No routine specified — save top-level keys directly into cur
				hasKeys := false
				for k, v := range action.Data {
					if k == "type" || k == "routine" || k == "clerkSelection" {
						continue
					}
					cur[k] = v
					hasKeys = true
				}
				if !hasKeys {
					log.Printf("saveSettings: no data to save, skipping write")
					return map[string]interface{}{"ok": true}
				}
				log.Printf("saveSettings: saved top-level keys: %v", cur)
			}
			if err := s.Settings.Write(cur); err != nil {
				log.Printf("saveSettings: write error: %v", err)
				return map[string]interface{}{"ok": false, "error": err.Error()}
			}
			// Broadcast updated settings to all connected clients so UI tabs refresh.
			updated := map[string]interface{}{}
			_ = s.Settings.Read(&updated)
			if updated == nil {
				updated = map[string]interface{}{}
			}
			s.Broadcast(map[string]interface{}{
				"type":   "settings_updated",
				"settings": updated,
			})
		}
		return map[string]interface{}{"ok": true}
	case "setClerkKey":
		// store keys in settings under clerk object (matching TS convention)
		if s.Settings != nil {
			var cur map[string]interface{}
			_ = s.Settings.Read(&cur)
			if cur == nil {
				cur = map[string]interface{}{}
			}
			clerkObj, _ := cur["clerk"].(map[string]interface{})
			if clerkObj == nil {
				clerkObj = map[string]interface{}{}
			}
			if v, ok := action.Data["clerkApiKey"]; ok {
				if s, ok := v.(string); ok {
					clerkObj["apiKey"] = strings.TrimSpace(s)
				} else {
					clerkObj["apiKey"] = v
				}
			}
			if v, ok := action.Data["clerkApiKey2"]; ok {
				if s, ok := v.(string); ok {
					clerkObj["apiKey2"] = strings.TrimSpace(s)
				} else {
					clerkObj["apiKey2"] = v
				}
			}
			cur["clerk"] = clerkObj
			_ = s.Settings.Write(cur)
			// Initialize bridge clients with the saved key(s) so the Clerk API key
			// is sent to spacemolt/lib immediately, not just on next
			// listClerkPlayers or restart.
			if s.Bridge != nil {
				var keys []interface{}
				if v, ok := clerkObj["apiKey"].(string); ok && v != "" {
					keys = append(keys, v)
				}
				if v, ok := clerkObj["apiKey2"].(string); ok && v != "" {
					keys = append(keys, v)
				}
				if len(keys) > 0 {
					if _, err := s.Bridge.Call(context.Background(), "initClients", map[string]interface{}{"keys": keys}); err != nil {
						log.Printf("setClerkKey: initClients error: %v", err)
					}
				}
			}
		}
		return map[string]interface{}{"ok": true}
	case "listClerkPlayers":
		if s.Bridge == nil {
			return map[string]interface{}{"ok": false, "error": "no bridge"}
		}
		// Initialize bridge clients with clerk API keys from settings
		if s.Settings != nil {
			var cur map[string]interface{}
			_ = s.Settings.Read(&cur)
			if cur != nil {
				var keys []interface{}
				if clerkObj, ok := cur["clerk"].(map[string]interface{}); ok {
					if v, ok := clerkObj["apiKey"].(string); ok && v != "" {
						keys = append(keys, v)
					}
					if v, ok := clerkObj["apiKey2"].(string); ok && v != "" {
						keys = append(keys, v)
					}
				}
				// Fallback: old format (top-level clerkApiKey)
				if len(keys) == 0 {
					if v, ok := cur["clerkApiKey"].(string); ok && v != "" {
						keys = append(keys, v)
					}
					if v, ok := cur["clerkApiKey2"].(string); ok && v != "" {
						keys = append(keys, v)
					}
				}
				log.Printf("listClerkPlayers: found %d keys", len(keys))
				if len(keys) > 0 {
					if _, err := s.Bridge.Call(context.Background(), "initClients", map[string]interface{}{"keys": keys}); err != nil {
						log.Printf("listClerkPlayers: initClients error: %v", err)
					}
				}
			}
		}
		res, err := s.Bridge.Call(context.Background(), "listOwnedPlayersByKey", nil)
		if err != nil {
			log.Printf("listClerkPlayers: bridge error: %v", err)
			return map[string]interface{}{"ok": false, "error": err.Error()}
		}
		// Parse the bridge result and format for frontend
		var groups []map[string]interface{}
		if err := json.Unmarshal(res, &groups); err != nil {
			// Maybe it's already an array
			return map[string]interface{}{"ok": false, "error": "failed to parse groups: " + err.Error()}
		}
		// Mark players as selected if they're in clerk.bots (persisted selection),
		// so the dashboard checkboxes stay ticked across reloads.
		if s.Settings != nil {
			var cur map[string]interface{}
			_ = s.Settings.Read(&cur)
			if cur != nil {
				clerkObj, _ := cur["clerk"].(map[string]interface{})
				var selectedBots []string
				if botsRaw, ok := clerkObj["bots"].([]interface{}); ok {
					for _, b := range botsRaw {
						if s2, ok := b.(string); ok {
							selectedBots = append(selectedBots, s2)
						}
					}
				}
				if len(selectedBots) > 0 {
					selectedSet := make(map[string]bool, len(selectedBots))
					for _, b := range selectedBots {
						selectedSet[b] = true
					}
					for _, g := range groups {
						if players, ok := g["players"].([]interface{}); ok {
							for _, pRaw := range players {
								if p, ok := pRaw.(map[string]interface{}); ok {
									var pUsername, pID string
									if username, ok := p["username"].(string); ok {
										pUsername = username
									}
									if id, ok := p["id"].(string); ok {
										pID = id
									}
									if (pUsername != "" && selectedSet[pUsername]) || (pID != "" && selectedSet[pID]) {
										p["selected"] = true
									}
								}
							}
						}
					}
				}
			}
		}
		count := 0
		for _, g := range groups {
			if players, ok := g["players"].([]interface{}); ok {
				count += len(players)
			}
		}
		return map[string]interface{}{"ok": true, "data": map[string]interface{}{"groups": groups, "count": count}}
	case "addClerkBots":
		if s.Bridge == nil {
			return map[string]interface{}{"ok": false, "error": "no bridge"}
		}
		if s.Manager == nil {
			return map[string]interface{}{"ok": false, "error": "no manager"}
		}
		idsRaw, _ := action.Data["ids"].([]interface{})
		var ids []string
		for _, idRaw := range idsRaw {
			if id, ok := idRaw.(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return map[string]interface{}{"ok": true, "data": map[string]interface{}{"added": float64(0), "ids": []interface{}{}}}
		}
		// Initialize bridge clients with clerk API keys from settings so
		// connectOwned has the Clerk key available (same as listClerkPlayers).
		if s.Settings != nil {
			var cur map[string]interface{}
			_ = s.Settings.Read(&cur)
			if cur != nil {
				clerkObj, _ := cur["clerk"].(map[string]interface{})
				var keys []interface{}
				if v, ok := clerkObj["apiKey"].(string); ok && v != "" {
					keys = append(keys, v)
				}
				if v, ok := clerkObj["apiKey2"].(string); ok && v != "" {
					keys = append(keys, v)
				}
				if len(keys) > 0 {
					if _, err := s.Bridge.Call(context.Background(), "initClients", map[string]interface{}{"keys": keys}); err != nil {
						log.Printf("addClerkBots: initClients error: %v", err)
					}
				}
			}
		}
		log.Printf("addClerkBots: connecting %d account(s): %v", len(ids), ids)
		res, err := s.Bridge.Call(context.Background(), "connectOwned", map[string]interface{}{"ids": ids})
		if err != nil {
			log.Printf("addClerkBots: connectOwned error: %v", err)
			return map[string]interface{}{"ok": false, "error": err.Error()}
		}
		log.Printf("addClerkBots: connectOwned response: %s", string(res))
		var connected []map[string]interface{}
		if err := json.Unmarshal(res, &connected); err != nil {
			return map[string]interface{}{"ok": false, "error": "failed to parse connected accounts: " + err.Error()}
		}
		log.Printf("addClerkBots: got %d connected accounts", len(connected))
		var addedIds []string
		for _, acc := range connected {
			accID, _ := acc["id"].(string)
			accUsername, _ := acc["username"].(string)
			if accID == "" {
				continue
			}
			if accUsername == "" {
				accUsername = accID
			}
			// Create the bot if it doesn't already exist (it may have been
			// created moments ago by the account.connected handler fired inside
			// connectOwnedAccounts). Always track the username so we can
			// persist it to clerk.bots below.
			if s.Manager.GetBotByAccount(accID) == nil {
				b := bot.New(accID, accUsername, accID, s.Bridge)
				s.Manager.AddBot(b)
				s.Manager.StartBot(context.Background(), b, 0)
				// Pull initial state so the dashboard shows real data
				// immediately instead of zeroed/unknown until the next
				// account.status event fires.
				go b.RefreshStatus(context.Background())
			}
			addedIds = append(addedIds, accUsername)
		}
		// Persist newly-added bots to clerk.bots so they survive reloads and
		// show as selected on the next listClerkPlayers. Persist the username
		// so it matches ClerkPlayer.username used by listClerkPlayers when
		// marking selected.
		if s.Settings != nil && len(addedIds) > 0 {
			var cur map[string]interface{}
			_ = s.Settings.Read(&cur)
			if cur == nil {
				cur = map[string]interface{}{}
			}
			clerkObj, _ := cur["clerk"].(map[string]interface{})
			if clerkObj == nil {
				clerkObj = map[string]interface{}{}
			}
			existing := make(map[string]bool)
			if botsRaw, ok := clerkObj["bots"].([]interface{}); ok {
				for _, b := range botsRaw {
					if s2, ok := b.(string); ok {
						existing[s2] = true
					}
				}
			}
			var bots []interface{}
			for b := range existing {
				bots = append(bots, b)
			}
			for _, id := range addedIds {
				if !existing[id] {
					bots = append(bots, id)
				}
			}
			clerkObj["bots"] = bots
			cur["clerk"] = clerkObj
			if err := s.Settings.Write(cur); err != nil {
				log.Printf("addClerkBots: settings write error: %v", err)
				return map[string]interface{}{"ok": false, "error": err.Error()}
			}
			log.Printf("addClerkBots: persisted clerk.bots=%v", bots)
		}
		return map[string]interface{}{"ok": true, "data": map[string]interface{}{"added": float64(len(addedIds)), "ids": addedIds}}
	case "bulkSetHunterMode":
		return map[string]interface{}{"ok": true}
	case "chat":
		return map[string]interface{}{"ok": true}
	case "commission":
		return map[string]interface{}{"ok": true}
	case "discardPendingWrite":
		return map[string]interface{}{"ok": true}
	case "emergencyReturn":
		return map[string]interface{}{"ok": true}
	case "faction":
		return map[string]interface{}{"ok": true}
	case "manual_rescue_request":
		return map[string]interface{}{"ok": true}
	case "outpost":
		return map[string]interface{}{"ok": true}
	case "package_logistics_bay", "package_logistics_center", "package_logistics_hub", "package_logistics_nexus":
		return map[string]interface{}{"ok": true}
	case "personal":
		return map[string]interface{}{"ok": true}
	case "recipes":
		resp := map[string]interface{}{"ok": true}
		resp["recipes"] = []interface{}{}
		return resp
	case "remove":
		return map[string]interface{}{"ok": true}
	case "retryPendingWrites":
		return map[string]interface{}{"ok": true}
	case "ships":
		resp := map[string]interface{}{"ok": true}
		resp["ships"] = []interface{}{}
		return resp
	case "stop_after_cycle":
		return map[string]interface{}{"ok": true}
	default:
		return map[string]interface{}{"ok": false, "error": "unknown action: " + action.Type}
	}
}

func (s *Server) Broadcast(msg interface{}) {
	b, _ := json.Marshal(msg)
	s.Hub.mu.Lock()
	defer s.Hub.mu.Unlock()
	for c := range s.Hub.clients {
		_ = c.WriteMessage(websocket.TextMessage, b)
	}
}

// Sync handlers

func (s *Server) handleSyncHello(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil || !s.Sync.IsMaster() {
		http.Error(w, "sync not available or not master", 400)
		return
	}
	s.Sync.GetMaster().HelloHandler(w, r)
}

func (s *Server) handleSyncHeartbeat(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil || !s.Sync.IsMaster() {
		http.Error(w, "sync not available or not master", 400)
		return
	}
	var req struct {
		ClientID string `json:"clientId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.Sync.GetMaster().Heartbeat(req.ClientID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleSyncCatalogVersion(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil || !s.Sync.IsMaster() {
		http.Error(w, "sync not available or not master", 400)
		return
	}
	var report ssync.CatalogVersionReport
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	resp := s.Sync.GetMaster().CatalogVersionReport(report)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleSyncCatalogState(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil || !s.Sync.IsMaster() {
		http.Error(w, "sync not available or not master", 400)
		return
	}
	state := s.Sync.GetMaster().GetCatalogSyncState()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

func (s *Server) handleSyncMarketQuery(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil || !s.Sync.IsMaster() {
		http.Error(w, "sync not available or not master", 400)
		return
	}
	var req ssync.MarketQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	result := s.Sync.GetMaster().HandleMarketQuery(req)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) handleSyncBotStatus(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil || !s.Sync.IsMaster() {
		http.Error(w, "sync not available or not master", 400)
		return
	}
	var status ssync.BotStatusPush
	if err := json.NewDecoder(r.Body).Decode(&status); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.Sync.GetMaster().PushBotStatus(status.Username, status.State, status)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"goroutines":     runtime.NumGoroutine(),
		"allocMB":        m.Alloc / 1024 / 1024,
		"totalAllocMB":   m.TotalAlloc / 1024 / 1024,
		"sysMB":          m.Sys / 1024 / 1024,
		"numGC":          m.NumGC,
		"bots":           len(s.Manager.ListBots()),
		"cpuCores":       runtime.NumCPU(),
	})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	restart := r.URL.Query().Get("restart") == "true"
	// Respond immediately so the frontend gets a success.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})

	// Shutdown on a goroutine so the HTTP response is sent first.
	go func() {
		time.Sleep(500 * time.Millisecond)
		if restart {
			os.Exit(101) // watchdog restarts on 101
		} else {
			os.Exit(0)   // watchdog stops on 0
		}
	}()
}

// ── Missing HTTP endpoints (stubs) ──
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	var settings map[string]interface{}
	if s.Settings != nil {
		_ = s.Settings.Read(&settings)
	}
	if settings == nil {
		settings = map[string]interface{}{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(settings)
}

// botToObj converts a *bot.Bot into the frontend's expected camelCase object.
// Matches TS BotStatus shape exactly: credits, fuel, maxFuel, cargo (total
// used, number), cargoMax (capacity), hull, maxHull, shield, maxShield, etc.
func botToObj(b *bot.Bot) map[string]interface{} {
	return map[string]interface{}{
		"id":        b.ID,
		"name":      b.Username,
		"username":  b.Username,
		"shipName":  b.ShipName,
		"state":     string(b.State),
		"routine":   b.Routine,
		"system":    b.System,
		"poi":       b.POI,
		"location":  b.System + "/" + b.POI,
		"credits":   b.Credits,
		"fuel":      b.Fuel,
		"maxFuel":   b.MaxFuel,
		"hull":      b.Hull,
		"maxHull":   b.MaxHull,
		"shield":    b.Shield,
		"maxShield": b.MaxShield,
		"isCloaked": b.IsCloaked,
		"cargo":     b.Cargo,
		"cargoMax":  b.CargoMax,
	}
}

func (s *Server) handleBots(w http.ResponseWriter, r *http.Request) {
	if s.Manager == nil {
		http.Error(w, "no manager", 500)
		return
	}
	botObjs := make([]map[string]interface{}, 0, len(s.Manager.ListBots()))
	for _, b := range s.Manager.ListBots() {
		botObjs = append(botObjs, botToObj(b))
	}
	// Sort by username so the dashboard table is stable across pushes
	// (Go map iteration order is randomized).
	sort.Slice(botObjs, func(i, j int) bool {
		return botObjs[i]["username"].(string) < botObjs[j]["username"].(string)
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"bots": botObjs})
}

func (s *Server) handleBotsDiscovered(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{}
	resp["bots"] = []interface{}{}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleRoutines(w http.ResponseWriter, r *http.Request) {
	routineNames := make([]string, 0, len(routines.Registry))
	for name := range routines.Registry {
		routineNames = append(routineNames, name)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"routines": routineNames})
}

func (s *Server) handleFlockSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"bots":     len(s.Manager.ListBots()),
		"goroutine": runtime.NumGoroutine(),
	})
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ships":     map[string]interface{}{},
		"recipes":   map[string]interface{}{},
		"modules":   map[string]interface{}{},
	})
}

func (s *Server) handleLogsMain(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": []interface{}{},
	})
}

func (s *Server) handleFactionStorage(w http.ResponseWriter, r *http.Request) {
	station := r.URL.Query().Get("station")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"station": station,
		"items":   []interface{}{},
	})
}

func (s *Server) handleLastUsedRoutines(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"routines": []interface{}{},
	})
}

func (s *Server) handleTaxes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"taxes": []interface{}{},
	})
}

func (s *Server) handleFactionTaxEstimate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"estimate": 0,
	})
}

func (s *Server) handleMapRegisterPOI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleMapRegisterWormhole(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleShipBulkMove(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleShipApplyPreset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleCraftingLoadouts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{}
	resp["loadouts"] = []interface{}{}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleShipLoadouts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{}
	resp["loadouts"] = []interface{}{}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleModuleLoadouts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{}
	resp["loadouts"] = []interface{}{}
	_ = json.NewEncoder(w).Encode(resp)
}

