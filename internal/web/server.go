package web

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"spacemolt-lt/embed"
	"spacemolt-lt/internal/botmanager"
	"spacemolt-lt/internal/bridge"
	"spacemolt-lt/internal/store"
	ssync "spacemolt-lt/internal/sync"
)

type Hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]bool
}

func NewHub() *Hub { return &Hub{clients: make(map[*websocket.Conn]bool)} }

type Server struct {
	Manager  *botmanager.Manager
	Bridge   *bridge.Client
	Settings *store.FileStore
	Sync     *ssync.Client
	Hub      *Hub
	up       websocket.Upgrader

	cancel    context.CancelFunc
	shutdownCh chan struct{}
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
			var action wsMessage
			_ = json.Unmarshal(msg, &action)
			resp := s.dispatch(action)
			b, _ := json.Marshal(resp)
			write(websocket.TextMessage, b)
		}
	}()

	// 250ms coalesced push
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		payload := map[string]interface{}{
			"type": "status",
			"bots": len(s.Manager.ListBots()),
		}
		b, _ := json.Marshal(payload)
		write(websocket.TextMessage, b)
	}
}

func (s *Server) dispatch(action wsMessage) map[string]interface{} {
	switch action.Type {
	case "ping":
		return map[string]interface{}{"ok": true, "pong": true}
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
			if routine != "" {
				cur[routine] = action.Data["settings"]
			}
			_ = s.Settings.Write(cur)
		}
		return map[string]interface{}{"ok": true}
	case "setClerkKey":
		// store keys in settings
		if s.Settings != nil {
			var cur map[string]interface{}
			_ = s.Settings.Read(&cur)
			if cur == nil {
				cur = map[string]interface{}{}
			}
			if v, ok := action.Data["clerkApiKey"]; ok {
				cur["clerkApiKey"] = v
			}
			if v, ok := action.Data["clerkApiKey2"]; ok {
				cur["clerkApiKey2"] = v
			}
			_ = s.Settings.Write(cur)
		}
		return map[string]interface{}{"ok": true}
	case "listClerkPlayers":
		if s.Bridge == nil {
			return map[string]interface{}{"ok": false, "error": "no bridge"}
		}
		res, err := s.Bridge.Call(context.Background(), "listOwnedPlayers", nil)
		if err != nil {
			return map[string]interface{}{"ok": false, "error": err.Error()}
		}
		return map[string]interface{}{"ok": true, "data": json.RawMessage(res)}
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

