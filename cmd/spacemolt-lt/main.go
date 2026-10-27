package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"spacemolt-lt/embed"
	"spacemolt-lt/internal/bot"
	"spacemolt-lt/internal/botmanager"
	"spacemolt-lt/internal/bridge"
	"spacemolt-lt/internal/store"
	"spacemolt-lt/internal/sync"
	"spacemolt-lt/internal/web"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ensure data dirs
	_ = os.MkdirAll("data", 0755)
	_ = os.MkdirAll("sessions", 0755)

	port := 8787
	if v := os.Getenv("BRIDGE_PORT"); v != "" {
		_, _ = fmt.Sscan(v, &port)
	}

	bm := bridge.NewManager("bridge", port)
	if err := bm.Start(ctx); err != nil {
		log.Printf("bridge start: %v", err)
	}

	client := bridge.NewClient(bm.URL())
	// retry connect
	for i := 0; i < 30; i++ {
		if err := client.Connect(ctx); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	mgr := botmanager.New(client)
	mgr.OnMassDisconnect = func() {
		log.Println("mass disconnect detected, exiting 100")
		os.Exit(100)
	}
	go func() {
		for ev := range client.Events() {
			switch ev.Method {
			case "account.disconnected":
				var p struct {
					ID string `json:"id"`
				}
				_ = json.Unmarshal(ev.Params, &p)
				if p.ID != "" {
					mgr.RecordDisconnect(p.ID)
				}
			case "account.connected":
				var p struct {
					ID       string `json:"id"`
					Username string `json:"username"`
				}
				_ = json.Unmarshal(ev.Params, &p)
				if p.ID != "" {
					username := p.Username
					if username == "" {
						username = p.ID
					}
					b := mgr.GetBotByAccount(p.ID)
					if b == nil {
						log.Printf("account.connected: creating bot for %s (%s)", p.ID, username)
						b = bot.New(p.ID, username, p.ID, client)
						mgr.AddBot(b)
					}
					if !b.IsRunning() {
						log.Printf("account.connected: starting bot %s (%s)", p.ID, username)
						mgr.StartBot(ctx, b, 0)
					}
				}
			case "account.status":
				var p struct {
					ID       string  `json:"id"`
					System   string  `json:"system"`
					POI      string  `json:"poi"`
					Ship     string  `json:"ship"`
					Credits  float64 `json:"credits"`
					Fuel     float64 `json:"fuel"`
					MaxFuel  float64 `json:"maxFuel"`
					Hull     float64 `json:"hull"`
					MaxHull  float64 `json:"maxHull"`
				}
				_ = json.Unmarshal(ev.Params, &p)
				if b := mgr.GetBotByAccount(p.ID); b != nil {
					b.SetStatus(bot.Status{
						System:   p.System,
						POI:      p.POI,
						Ship:     p.Ship,
						Credits:  p.Credits,
						Fuel:     p.Fuel,
						MaxFuel:  p.MaxFuel,
						Hull:     p.Hull,
						MaxHull:  p.MaxHull,
						Cargo:    map[string]int{},
					})
				}
			}
		}
	}()

	_ = mgr.DiscoverSessions("sessions")
	for i, b := range mgr.ListBots() {
		mgr.StartBot(ctx, b, time.Duration(i)*2*time.Second)
	}
		// Restore bots from persisted clerk.bots so they survive client restarts.
		// Create them but do NOT start yet — they'll be started when the
		// account.connected event fires from startup connectOwned below.
		settingsStore := store.New("data/settings.json")
		var settings map[string]interface{}
		_ = settingsStore.Read(&settings)
		if settings != nil {
			if clerkObj, ok := settings["clerk"].(map[string]interface{}); ok {
				if botsRaw, ok := clerkObj["bots"].([]interface{}); ok {
					for _, bRaw := range botsRaw {
						id, _ := bRaw.(string)
						if id == "" || mgr.GetBotByAccount(id) != nil {
							continue
						}
						log.Printf("restoring clerk bot %s from settings", id)
						b := bot.New(id, id, id, client)
						mgr.AddBot(b)
						// Don't StartBot here — wait for account.connected from
						// connectOwned below so the bridge connection is live first.
					}
				}
			}
		}

	// Read sync settings and create sync client/master.
	syncSettings := sync.DefaultSettings()
	if settings != nil {
		if s, ok := settings["sync"].(map[string]interface{}); ok {
			if v, ok := s["enabled"].(bool); ok {
				syncSettings.Enabled = v
			}
			if v, ok := s["mode"].(string); ok {
				syncSettings.Mode = sync.Mode(v)
			}
			if v, ok := s["masterUrl"].(string); ok && v != "" {
				syncSettings.MasterURL = v
			}
			if v, ok := s["apiKey"].(string); ok {
				syncSettings.APIKey = v
			}
			if v, ok := s["label"].(string); ok && v != "" {
				syncSettings.Label = v
			}
			if v, ok := s["pollIntervalSec"].(float64); ok {
				syncSettings.PollIntervalSec = int(v)
			}
		}
	}

	var syncClient *sync.Client
	if syncSettings.Enabled {
		syncClient = sync.NewClient(syncSettings.Mode, syncSettings)
		if err := syncClient.Start(context.Background()); err != nil {
			log.Printf("sync start: %v", err)
		}
		if syncSettings.Mode == sync.ModeMaster && syncClient.GetMaster() != nil {
			m := syncClient.GetMaster()
			m.RegisterClient(syncClient.ClientID(), syncSettings.Label, syncSettings.Password, syncSettings.APIKey, "", false, "127.0.0.1")
		}
	}

	// init bridge clients from settings
	if settings != nil {
		keys := []interface{}{}
		if clerkObj, ok := settings["clerk"].(map[string]interface{}); ok {
			if v, ok := clerkObj["apiKey"]; ok {
				keys = append(keys, v)
			}
			if v, ok := clerkObj["apiKey2"]; ok {
				keys = append(keys, v)
			}
		}
		strKeys := make([]string, 0, len(keys))
		for _, k := range keys {
			if s, ok := k.(string); ok && s != "" {
				strKeys = append(strKeys, s)
			}
		}
		if len(strKeys) > 0 {
			_, _ = client.Call(context.Background(), "initClients", map[string]interface{}{"keys": strKeys})
		}
		// Connect any bots persisted in clerk.bots so they come back online
		// after a restart, not just sit there disconnected.
		if settings != nil {
			if clerkObj, ok := settings["clerk"].(map[string]interface{}); ok {
				if botsRaw, ok := clerkObj["bots"].([]interface{}); ok {
					var botIds []string
					for _, b := range botsRaw {
						if id, ok := b.(string); ok && id != "" {
							botIds = append(botIds, id)
						}
					}
					if len(botIds) > 0 {
						if _, err := client.Call(context.Background(), "connectOwned", map[string]interface{}{"ids": botIds}); err != nil {
							log.Printf("startup connectOwned error: %v", err)
						} else {
							log.Printf("startup: connecting %d clerk bot(s): %v", len(botIds), botIds)
						}
					}
				}
			}
		}
	}

	ws := web.New(mgr, client, settingsStore, syncClient, cancel)
	ws.RegisterRoutes()

	addr := ":3000"
	if v := os.Getenv("PORT"); v != "" {
		addr = ":" + v
	}
	log.Printf("dashboard listening on %s", addr)

	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Fatal(err)
		}
	}()

	// keep embed referenced
	_ = embed.FS

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	bm.Stop()
	client.Close()
	settingsStore.Stop()
	cancel()
}

