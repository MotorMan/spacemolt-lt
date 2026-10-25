package sync

import (
	"encoding/json"
	"net/http"
	"sync"
)

type Master struct {
	mu        sync.RWMutex
	settings  SyncSettings
	clients   map[string]*RegisteredClient
	botStatus map[string]BotStatusPush // username -> status

	catalogVersion     string
	catalogLastFetched string
	catalogDownloader  string // clientId designated to download
	catalogData        map[string]interface{}

	version string
}

func NewMaster(settings SyncSettings) *Master {
	return &Master{
		settings:   settings,
		clients:    make(map[string]*RegisteredClient),
		botStatus:  make(map[string]BotStatusPush),
		catalogData: make(map[string]interface{}),
		version:    "1.0.0",
	}
}

func (m *Master) Settings() SyncSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings
}

func (m *Master) SetSettings(s SyncSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = s
}

func (m *Master) RegisterClient(clientID, label, password, apiKey, selfURL string, light bool, ip string) *RegisteredClient {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := nowUnix()
	rc := &RegisteredClient{
		ClientID:   clientID,
		Label:      label,
		Password:   password,
		APIKey:     apiKey,
		ConnectedAt: now,
		LastSeen:   now,
		IP:         ip,
		SelfURL:    selfURL,
		Light:      light,
	}
	m.clients[clientID] = rc
	return rc
}

func (m *Master) Heartbeat(clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clients[clientID]; ok {
		c.LastSeen = nowUnix()
	}
}

func (m *Master) SetClientMarketData(clientID string, has bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clients[clientID]; ok {
		c.HasMarketData = has
	}
}

func (m *Master) SetClientBotCount(clientID string, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.clients[clientID]; ok {
		c.BotCount = count
	}
}

func (m *Master) ListClients() []*RegisteredClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*RegisteredClient, 0, len(m.clients))
	for _, c := range m.clients {
		out = append(out, c)
	}
	return out
}

func (m *Master) GetClient(clientID string) *RegisteredClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clients[clientID]
}

func (m *Master) PushBotStatus(username, state string, status BotStatusPush) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status.Username = username
	status.State = state
	m.botStatus[username] = status
}

func (m *Master) GetBotStatus(username string) (BotStatusPush, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.botStatus[username]
	return s, ok
}

func (m *Master) ListBotStatuses() []BotStatusPush {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]BotStatusPush, 0, len(m.botStatus))
	for _, s := range m.botStatus {
		out = append(out, s)
	}
	return out
}

// CatalogVersionReport handles a client reporting its catalog version.
// Returns the master's verdict on what action the client should take.
func (m *Master) CatalogVersionReport(report CatalogVersionReport) CatalogVersionResponse {
	m.mu.Lock()
	defer m.mu.Unlock()

	resp := CatalogVersionResponse{
		OK:               true,
		Action:           "none",
		GameServerVersion: m.catalogVersion,
	}

	if report.Version != nil {
		resp.Version = report.Version
	}

	// If we have no catalog version yet, ask client to upload.
	if m.catalogVersion == "" {
		resp.Action = "upload"
		return resp
	}

	// If client version matches ours, no action needed.
	if report.Version != nil && *report.Version == m.catalogVersion {
		resp.Action = "none"
		return resp
	}

	// If client has no version or older version, designate as downloader or ask to accept.
	if m.catalogDownloader == "" {
		m.catalogDownloader = "unknown"
		// We don't have clientID here in this signature. Let's just return accept_catalog if we have data.
		if len(m.catalogData) > 0 {
			resp.Action = "accept_catalog"
			resp.Catalog = m.catalogData
		} else {
			resp.Action = "download_and_upload"
		}
		return resp
	}

	resp.Action = "accept_catalog"
	if len(m.catalogData) > 0 {
		resp.Catalog = m.catalogData
	}
	return resp
}

func (m *Master) SetCatalogData(version, lastFetched, fromClientID string, data map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.catalogVersion = version
	m.catalogLastFetched = lastFetched
	m.catalogDownloader = fromClientID
	m.catalogData = data
}

func (m *Master) GetCatalogSyncState() CatalogSyncState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	clients := make([]CatalogSyncStateClient, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, CatalogSyncStateClient{
			ClientID:    c.ClientID,
			Version:     c.ClientID, // placeholder - we don't track version per client in this simple impl
			LastFetched: "",
		})
	}
	return CatalogSyncState{
		GameServerVersion:   m.catalogVersion,
		LatestCatalogVersion: m.catalogVersion,
		LatestCatalogFrom:   m.catalogDownloader,
		Downloader:          m.catalogDownloader,
		Clients:             clients,
	}
}

func (m *Master) HandleMarketQuery(req MarketQueryRequest) MarketQueryResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := MarketQueryResult{
		OK:    true,
		Source: "local",
	}

	// Check if any client has market data.
	for _, c := range m.clients {
		if c.HasMarketData {
			result.Source = "remote"
			break
		}
	}

	return result
}

// HTTP handlers

func (m *Master) HelloHandler(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	resp := HelloResponse{
		OK:               true,
		Version:          m.version,
		ClientID:         "",
		ConnectedClients: m.ListClients(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (m *Master) CatalogSyncStateHandler(w http.ResponseWriter, r *http.Request) {
	state := m.GetCatalogSyncState()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

func (m *Master) MarketQueryHandler(w http.ResponseWriter, r *http.Request) {
	var req MarketQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	result := m.HandleMarketQuery(req)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
