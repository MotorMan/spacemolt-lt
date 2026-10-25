package botmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"spacemolt-lt/internal/bot"
	"spacemolt-lt/internal/bridge"
	"spacemolt-lt/internal/routines"
)

type Manager struct {
	mu    sync.RWMutex
	bots  map[string]*bot.Bot
	bridge *bridge.Client

	massMu   sync.Mutex
	disconnects []time.Time

	OnMassDisconnect func()
}

func New(bc *bridge.Client) *Manager {
	return &Manager{bots: make(map[string]*bot.Bot), bridge: bc}
}

func (m *Manager) Bridge() *bridge.Client { return m.bridge }

func (m *Manager) AddBot(b *bot.Bot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bots[b.ID] = b
}

func (m *Manager) RemoveBot(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.bots[id]; ok {
		b.Cancel()
		delete(m.bots, id)
	}
}

func (m *Manager) GetBot(id string) *bot.Bot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.bots[id]
}

func (m *Manager) GetBotByAccount(accountID string) *bot.Bot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, b := range m.bots {
		if b.AccountID == accountID {
			return b
		}
	}
	return nil
}
func (m *Manager) ListBots() []*bot.Bot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*bot.Bot, 0, len(m.bots))
	for _, b := range m.bots {
		out = append(out, b)
	}
	return out
}

func (m *Manager) RecordDisconnect(id string) {
	m.massMu.Lock()
	defer m.massMu.Unlock()
	now := time.Now()
	m.disconnects = append(m.disconnects, now)
	// prune older than 5s
	cutoff := now.Add(-5 * time.Second)
	kept := m.disconnects[:0]
	for _, t := range m.disconnects {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	m.disconnects = kept
	if len(m.disconnects) >= 5 {
		if m.OnMassDisconnect != nil {
			go m.OnMassDisconnect()
		} else {
			os.Exit(100)
		}
	}
}

func (m *Manager) DiscoverSessions(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessionPath := filepath.Join(dir, e.Name(), "session.json")
		data, err := os.ReadFile(sessionPath)
		if err != nil {
			continue
		}
		var s struct {
			Username string `json:"username"`
			ID       string `json:"id"`
		}
		_ = json.Unmarshal(data, &s)
		id := s.ID
		if id == "" {
			id = e.Name()
		}
		name := s.Username
		if name == "" {
			name = e.Name()
		}
		b := bot.New(id, name, id, m.bridge)
		m.AddBot(b)
	}
	return nil
}

func (m *Manager) StartBot(ctx context.Context, b *bot.Bot, delay time.Duration) {
	go func() {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
		b.SetRoutine("idle")
		routines.Run(ctx, b, "idle")
	}()
}

