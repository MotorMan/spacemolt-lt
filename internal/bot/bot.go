package bot

import (
	"context"
	"encoding/json"
	"sync"

	"spacemolt-lt/internal/bridge"
)

type Status struct {
	Location   string                 `json:"location"`
	System     string                 `json:"system"`
	POI        string                 `json:"poi"`
	Ship       string                 `json:"ship"`
	Credits    float64                `json:"credits"`
	Fuel       float64                `json:"fuel"`
	MaxFuel    float64                `json:"maxFuel"`
	Hull       float64                `json:"hull"`
	MaxHull    float64                `json:"maxHull"`
	Cargo      map[string]int         `json:"cargo"`
	Extra      map[string]interface{} `json:"-"`
}

type BotState string

const (
	StateRunning  BotState = "running"
	StateStopping BotState = "stopping"
	StateStopped  BotState = "stopped"
)

type Bot struct {
	ID        string
	Name      string
	Username  string
	AccountID string
	Bridge    bridge.Caller
	Routine   string
	State     BotState

	System   string
	POI      string
	Docked   bool
	IsCloaked bool
	InTransit bool
	TransitType string
	Fuel     float64
	MaxFuel  float64
	Hull     float64
	MaxHull  float64
	Credits  float64
	Cargo    map[string]int

	mu     sync.RWMutex
	status Status

	ctx    context.Context
	cancel context.CancelFunc
}

func New(id, name, accountID string, bc bridge.Caller) *Bot {
	ctx, cancel := context.WithCancel(context.Background())
	return &Bot{
		ID:        id,
		Name:      name,
		AccountID: accountID,
		Username:  name,
		Bridge:    bc,
		State:     StateRunning,
		ctx:       ctx,
		cancel:    cancel,
		status:    Status{Cargo: map[string]int{}, Extra: map[string]interface{}{}},
		Cargo:     map[string]int{},
	}
}

func (b *Bot) Context() context.Context { return b.ctx }

func (b *Bot) Cancel() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.State = StateStopped
	if b.cancel != nil {
		b.cancel()
	}
}

func (b *Bot) IsRunning() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.State == StateRunning
}

func (b *Bot) GetLocation() (string, string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.System, b.POI
}

func (b *Bot) SetLocation(system, poi string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.System = system
	b.POI = poi
}

func (b *Bot) SetDocked(docked bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Docked = docked
}

func (b *Bot) SetCloaked(cloaked bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.IsCloaked = cloaked
}

// Snapshot returns an atomic copy of the bot's runtime state.
func (b *Bot) Snapshot() BotStateSnapshot {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return BotStateSnapshot{
		System:     b.System,
		POI:        b.POI,
		Docked:     b.Docked,
		IsCloaked:  b.IsCloaked,
		InTransit:  b.InTransit,
		TransitType: b.TransitType,
		Fuel:       b.Fuel,
		MaxFuel:    b.MaxFuel,
		Hull:       b.Hull,
		MaxHull:    b.MaxHull,
		Credits:    b.Credits,
		Cargo:      b.Cargo,
	}
}

// BotStateSnapshot is an atomic snapshot of Bot runtime state.
type BotStateSnapshot struct {
	System     string
	POI        string
	Docked     bool
	IsCloaked  bool
	InTransit  bool
	TransitType string
	Fuel       float64
	MaxFuel    float64
	Hull       float64
	MaxHull    float64
	Credits    float64
	Cargo      map[string]int
}

func (b *Bot) SetStatus(s Status) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = s
	b.State = StateRunning
	if s.System != "" {
		b.System = s.System
	}
	if s.POI != "" {
		b.POI = s.POI
	}
	if s.Location != "" {
		b.System = s.Location
	}
	b.Fuel = s.Fuel
	b.MaxFuel = s.MaxFuel
	b.Hull = s.Hull
	b.MaxHull = s.MaxHull
	b.Credits = s.Credits
	if s.Cargo != nil {
		b.Cargo = s.Cargo
	}
}

func (b *Bot) GetStatus() Status {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.status
}

func (b *Bot) SetRoutine(r string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Routine = r
}

func (b *Bot) SetState(s BotState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.State = s
}

// ExecCommand sends an exec command through the bridge and returns the raw JSON result.
func (b *Bot) ExecCommand(ctx context.Context, command string, params map[string]interface{}) (json.RawMessage, error) {
	if b.Bridge == nil {
		return nil, nil
	}
	return b.Bridge.Call(ctx, "exec", map[string]interface{}{
		"id":      b.AccountID,
		"command": command,
		"params":  params,
	})
}

// RefreshStatus calls get_status via the bridge and updates local state.
func (b *Bot) RefreshStatus(ctx context.Context) error {
	res, err := b.ExecCommand(ctx, "get_status", nil)
	if err != nil || res == nil {
		return err
	}
	var st struct {
		System   string  `json:"system"`
		POI      string  `json:"poi"`
		Docked   bool    `json:"docked"`
		IsCloaked bool   `json:"isCloaked"`
		InTransit bool   `json:"inTransit"`
		TransitType string `json:"transitType"`
		Fuel     float64 `json:"fuel"`
		MaxFuel  float64 `json:"maxFuel"`
		Hull     float64 `json:"hull"`
		MaxHull  float64 `json:"maxHull"`
		Credits  float64 `json:"credits"`
		Cargo    map[string]int `json:"cargo"`
	}
	if err := json.Unmarshal(res, &st); err != nil {
		return err
	}
	b.mu.Lock()
	b.System = st.System
	b.POI = st.POI
	b.Docked = st.Docked
	b.IsCloaked = st.IsCloaked
	b.InTransit = st.InTransit
	b.TransitType = st.TransitType
	b.Fuel = st.Fuel
	b.MaxFuel = st.MaxFuel
	b.Hull = st.Hull
	b.MaxHull = st.MaxHull
	b.Credits = st.Credits
	if st.Cargo != nil {
		b.Cargo = st.Cargo
	}
	b.mu.Unlock()
	b.SetStatus(Status{
		Location: st.System + "/" + st.POI,
		Ship:     st.TransitType,
		Credits:  st.Credits,
		Fuel:     st.Fuel,
		Cargo:    b.Cargo,
	})
	return nil
}

// RefreshLocation calls get_status and updates location fields only.
func (b *Bot) RefreshLocation(ctx context.Context) error {
	return b.RefreshStatus(ctx)
}

// RefreshShip calls get_status and updates ship/hull/fuel fields only.
func (b *Bot) RefreshShip(ctx context.Context) error {
	return b.RefreshStatus(ctx)
}

// RefreshCargo calls get_status and updates cargo only.
func (b *Bot) RefreshCargo(ctx context.Context) error {
	return b.RefreshStatus(ctx)
}

// SubscribeToObservation subscribes to passive observation for the current location.
func (b *Bot) SubscribeToObservation(ctx context.Context, passive bool) error {
	if b.Bridge == nil {
		return nil
	}
	_, err := b.Bridge.Call(ctx, "exec", map[string]interface{}{
		"id":      b.AccountID,
		"command": "subscribe_observation",
		"params":  map[string]interface{}{"passive": passive, "system": b.System, "poi": b.POI},
	})
	return err
}

// ClearObservationState is a no-op placeholder for observation cleanup.
func (b *Bot) ClearObservationState() {}

