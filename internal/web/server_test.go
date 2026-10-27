package web

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"spacemolt-lt/internal/bot"
	"spacemolt-lt/internal/store"
)

type mockBridge struct {
	callMethod string
	callParams interface{}
	callResult []byte
	callErr    error
	callCount  int
}

func (m *mockBridge) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	m.callCount++
	m.callMethod = method
	m.callParams = params
	if m.callErr != nil {
		return nil, m.callErr
	}
	return m.callResult, nil
}

type mockManager struct {
	bots    map[string]*bot.Bot
	started []string
	added   []*bot.Bot
}

func newMockManager() *mockManager {
	return &mockManager{bots: make(map[string]*bot.Bot)}
}

func (m *mockManager) ListBots() []*bot.Bot {
	out := make([]*bot.Bot, 0, len(m.bots))
	for _, b := range m.bots {
		out = append(out, b)
	}
	return out
}

func (m *mockManager) GetBot(id string) *bot.Bot {
	return m.bots[id]
}

func (m *mockManager) GetBotByAccount(accountID string) *bot.Bot {
	for _, b := range m.bots {
		if b.AccountID == accountID {
			return b
		}
	}
	return nil
}

func (m *mockManager) AddBot(b *bot.Bot) {
	m.added = append(m.added, b)
	m.bots[b.ID] = b
}

func (m *mockManager) RemoveBot(id string) {
	delete(m.bots, id)
}

func (m *mockManager) StartBot(ctx context.Context, b *bot.Bot, delay time.Duration) {
	m.started = append(m.started, b.ID)
}

func testServer(t *testing.T, settings map[string]interface{}) (*Server, *mockManager, *mockBridge, *store.FileStore) {
	t.Helper()
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	ss := store.New(settingsPath)
	if settings != nil {
		if err := ss.Write(settings); err != nil {
			t.Fatal(err)
		}
	}
	mgr := newMockManager()
	br := &mockBridge{}
	s := &Server{
		Manager:  mgr,
		Bridge:   br,
		Settings: ss,
		Hub:      NewHub(),
	}
	return s, mgr, br, ss
}

func TestAddClerkBotsNoBridge(t *testing.T) {
	s, _, _, _ := testServer(t, nil)
	s.Bridge = nil
	resp := s.dispatch(wsMessage{Type: "addClerkBots", Data: map[string]interface{}{"ids": []interface{}{"LT14"}}})
	if resp["ok"] != false {
		t.Fatalf("expected ok=false, got %v", resp)
	}
	if _, ok := resp["error"]; !ok {
		t.Fatal("expected error key")
	}
}

func TestAddClerkBotsNoManager(t *testing.T) {
	s, _, br, _ := testServer(t, nil)
	s.Manager = nil
	_ = br
	resp := s.dispatch(wsMessage{Type: "addClerkBots", Data: map[string]interface{}{"ids": []interface{}{"LT14"}}})
	if resp["ok"] != false {
		t.Fatalf("expected ok=false, got %v", resp)
	}
}

func TestAddClerkBotsEmptyIds(t *testing.T) {
	s, mgr, br, _ := testServer(t, nil)
	resp := s.dispatch(wsMessage{Type: "addClerkBots", Data: map[string]interface{}{"ids": []interface{}{}}})
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	if data == nil {
		t.Fatal("expected data key")
	}
	if data["added"] != float64(0) {
		t.Fatalf("expected added=0, got %v", data["added"])
	}
	if br.callCount != 0 {
		t.Fatalf("expected no bridge calls, got %d", br.callCount)
	}
	if len(mgr.added) != 0 {
		t.Fatalf("expected no bots added, got %d", len(mgr.added))
	}
}

func TestAddClerkBotsCallsInitClients(t *testing.T) {
	settings := map[string]interface{}{
		"clerk": map[string]interface{}{
			"apiKey": "test-key",
		},
	}
	s, mgr, br, _ := testServer(t, settings)
	br.callResult = []byte(`[{"id":"acct1","username":"LT14"}]`)

	resp := s.dispatch(wsMessage{Type: "addClerkBots", Data: map[string]interface{}{"ids": []interface{}{"LT14"}}})
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp)
	}

	if br.callCount < 2 {
		t.Fatalf("expected at least 2 bridge calls (initClients + connectOwned), got %d", br.callCount)
	}
	if br.callMethod != "connectOwned" {
		t.Fatalf("expected last call to be connectOwned, got %s", br.callMethod)
	}
	if len(mgr.added) != 1 {
		t.Fatalf("expected 1 bot added, got %d", len(mgr.added))
	}
	if mgr.added[0].Username != "LT14" {
		t.Fatalf("expected bot username LT14, got %s", mgr.added[0].Username)
	}
	if mgr.added[0].AccountID != "acct1" {
		t.Fatalf("expected bot accountID acct1, got %s", mgr.added[0].AccountID)
	}
}

func TestAddClerkBotsPersistsToSettings(t *testing.T) {
	settings := map[string]interface{}{
		"clerk": map[string]interface{}{
			"apiKey": "test-key",
		},
	}
	s, _, br, ss := testServer(t, settings)
	br.callResult = []byte(`[{"id":"acct1","username":"LT14"}]`)

	resp := s.dispatch(wsMessage{Type: "addClerkBots", Data: map[string]interface{}{"ids": []interface{}{"LT14"}}})
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp)
	}

	var out map[string]interface{}
	if err := ss.Read(&out); err != nil {
		t.Fatal(err)
	}
	clerk, ok := out["clerk"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected clerk object, got %v", out["clerk"])
	}
	botsRaw, ok := clerk["bots"].([]interface{})
	if !ok {
		t.Fatalf("expected clerk.bots array, got %v", clerk["bots"])
	}
	found := false
	for _, b := range botsRaw {
		if s, ok := b.(string); ok && s == "LT14" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected clerk.bots to contain LT14, got %v", botsRaw)
	}

	data, _ := resp["data"].(map[string]interface{})
	if data["added"] != float64(1) {
		t.Fatalf("expected added=1, got %v", data["added"])
	}
}

func TestAddClerkBotsExistingBotNotRecreated(t *testing.T) {
	settings := map[string]interface{}{
		"clerk": map[string]interface{}{
			"apiKey": "test-key",
		},
	}
	s, mgr, br, _ := testServer(t, settings)
	br.callResult = []byte(`[{"id":"acct1","username":"LT14"}]`)

	existing := bot.New("acct1", "LT14", "acct1", nil)
	mgr.AddBot(existing)
	// Clear the added tracker so we only count additions from this dispatch.
	mgr.added = nil

	resp := s.dispatch(wsMessage{Type: "addClerkBots", Data: map[string]interface{}{"ids": []interface{}{"LT14"}}})
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp)
	}

	if len(mgr.added) != 0 {
		t.Fatalf("expected no new bots added (already existed), got %d", len(mgr.added))
	}

	var out map[string]interface{}
	_ = s.Settings.Read(&out)
	clerk, _ := out["clerk"].(map[string]interface{})
	botsRaw, _ := clerk["bots"].([]interface{})
	found := false
	for _, b := range botsRaw {
		if s, ok := b.(string); ok && s == "LT14" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected clerk.bots to contain LT14 even when bot already existed, got %v", botsRaw)
	}
}

func TestListClerkPlayersMarksSelected(t *testing.T) {
	settings := map[string]interface{}{
		"clerk": map[string]interface{}{
			"apiKey":  "test-key",
			"apiKey2": "",
			"bots":    []interface{}{"LT14"},
		},
	}
	s, _, br, _ := testServer(t, settings)
	br.callResult = []byte(`[{"keyIndex":0,"keyLabel":"k1","players":[{"id":"acct1","username":"LT14","empire":"sol","hidden":false}]}]`)

	resp := s.dispatch(wsMessage{Type: "listClerkPlayers"})
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	groups, _ := data["groups"].([]map[string]interface{})
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	group := groups[0]
	players, _ := group["players"].([]interface{})
	if len(players) != 1 {
		t.Fatalf("expected 1 player, got %d", len(players))
	}
	player, _ := players[0].(map[string]interface{})
	if player["selected"] != true {
		t.Fatalf("expected player selected=true, got %v", player["selected"])
	}
}

func TestListClerkPlayersMatchesById(t *testing.T) {
	settings := map[string]interface{}{
		"clerk": map[string]interface{}{
			"apiKey": "test-key",
			"bots":   []interface{}{"acct1"},
		},
	}
	s, _, br, _ := testServer(t, settings)
	br.callResult = []byte(`[{"keyIndex":0,"keyLabel":"k1","players":[{"id":"acct1","username":"LT14","empire":"sol","hidden":false}]}]`)

	resp := s.dispatch(wsMessage{Type: "listClerkPlayers"})
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	groups, _ := data["groups"].([]map[string]interface{})
	group := groups[0]
	players, _ := group["players"].([]interface{})
	player, _ := players[0].(map[string]interface{})
	if player["selected"] != true {
		t.Fatalf("expected player selected=true when matching by account id, got %v", player["selected"])
	}
}

func TestHandleBotsReturnsCamelCase(t *testing.T) {
	s, mgr, _, _ := testServer(t, nil)
	mgr.AddBot(bot.New("acct1", "LT14", "acct1", nil))

	resp := handleBots(s)
	body, _ := json.Marshal(resp)
	var out map[string]interface{}
	_ = json.Unmarshal(body, &out)
	bots, _ := out["bots"].([]interface{})
	if len(bots) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(bots))
	}
	b, _ := bots[0].(map[string]interface{})
	if b["username"] != "LT14" {
		t.Fatalf("expected username=LT14, got %v", b["username"])
	}
	if b["id"] != "acct1" {
		t.Fatalf("expected id=acct1, got %v", b["id"])
	}
	if b["name"] != "LT14" {
		t.Fatalf("expected name=LT14, got %v", b["name"])
	}
}

func TestInitReturnsBotObjects(t *testing.T) {
	s, mgr, _, _ := testServer(t, nil)
	mgr.AddBot(bot.New("acct1", "LT14", "acct1", nil))

	resp := s.dispatch(wsMessage{Type: "init"})
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp)
	}
	bots, _ := resp["bots"].([]map[string]interface{})
	if len(bots) != 1 {
		t.Fatalf("expected 1 bot, got %d", len(bots))
	}
	if bots[0]["username"] != "LT14" {
		t.Fatalf("expected username=LT14, got %v", bots[0]["username"])
	}
	if bots[0]["state"] != "running" {
		t.Fatalf("expected state=running, got %v", bots[0]["state"])
	}
}

func handleBots(s *Server) map[string]interface{} {
	botObjs := make([]map[string]interface{}, 0, len(s.Manager.ListBots()))
	for _, b := range s.Manager.ListBots() {
		botObjs = append(botObjs, map[string]interface{}{
			"id":        b.ID,
			"name":      b.Username,
			"username":  b.Username,
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
			"isCloaked": b.IsCloaked,
			"cargo":     b.Cargo,
		})
	}
	return map[string]interface{}{"bots": botObjs}
}

func TestAddClerkBotsConnectOwnedParams(t *testing.T) {
	settings := map[string]interface{}{
		"clerk": map[string]interface{}{
			"apiKey": "test-key",
		},
	}
	s, _, br, _ := testServer(t, settings)
	br.callResult = []byte(`[{"id":"acct1","username":"LT14"}]`)

	_ = s.dispatch(wsMessage{Type: "addClerkBots", Data: map[string]interface{}{"ids": []interface{}{"LT14"}}})

	if br.callMethod != "connectOwned" {
		t.Fatalf("expected connectOwned, got %s", br.callMethod)
	}
	params, ok := br.callParams.(map[string]interface{})
	if !ok {
		t.Fatalf("expected params map, got %T", br.callParams)
	}
	var ids []interface{}
	switch v := params["ids"].(type) {
	case []interface{}:
		ids = v
	case []string:
		for _, s := range v {
			ids = append(ids, s)
		}
	default:
		t.Fatalf("expected ids array, got %v (%T)", params["ids"], params["ids"])
	}
	if len(ids) != 1 {
		t.Fatalf("expected ids=[LT14], got %v", ids)
	}
	idStr, ok := ids[0].(string)
	if !ok || idStr != "LT14" {
		t.Fatalf("expected ids[0]=LT14, got %v (%T)", ids[0], ids[0])
	}
}

func TestAddClerkBotsUsesUsernameNotId(t *testing.T) {
	settings := map[string]interface{}{
		"clerk": map[string]interface{}{
			"apiKey": "test-key",
		},
	}
	s, _, br, ss := testServer(t, settings)
	br.callResult = []byte(`[{"id":"player_id_123","username":"LT14"}]`)

	resp := s.dispatch(wsMessage{Type: "addClerkBots", Data: map[string]interface{}{"ids": []interface{}{"LT14"}}})
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp)
	}

	var out map[string]interface{}
	_ = ss.Read(&out)
	clerk, _ := out["clerk"].(map[string]interface{})
	botsRaw, _ := clerk["bots"].([]interface{})
	foundUsername := false
	foundPlayerID := false
	for _, b := range botsRaw {
		if s, ok := b.(string); ok {
			if s == "LT14" {
				foundUsername = true
			}
			if s == "player_id_123" {
				foundPlayerID = true
			}
		}
	}
	if !foundUsername {
		t.Fatalf("expected clerk.bots to contain username LT14, got %v", botsRaw)
	}
	if foundPlayerID {
		t.Fatalf("expected clerk.bots NOT to contain player_id player_id_123, got %v", botsRaw)
	}
}

func TestAddClerkBotsConnectOwnedError(t *testing.T) {
	settings := map[string]interface{}{
		"clerk": map[string]interface{}{
			"apiKey": "test-key",
		},
	}
	s, _, _, _ := testServer(t, settings)
	s.Bridge = &mockBridge{callErr: errors.New("bridge down")}
	resp := s.dispatch(wsMessage{Type: "addClerkBots", Data: map[string]interface{}{"ids": []interface{}{"LT14"}}})
	if resp["ok"] != false {
		t.Fatalf("expected ok=false on connectOwned error, got %v", resp)
	}
}
