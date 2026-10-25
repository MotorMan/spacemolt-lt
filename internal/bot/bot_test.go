package bot

import (
	"testing"
)

func TestBotStateTransitions(t *testing.T) {
	b := New("bot1", "Bot One", "acct1", nil)
	if b.State != StateRunning {
		t.Fatalf("expected running, got %s", b.State)
	}
	if !b.IsRunning() {
		t.Fatal("expected IsRunning to be true")
	}

	b.Cancel()
	if b.State != StateStopped {
		t.Fatalf("expected stopped, got %s", b.State)
	}
	if b.IsRunning() {
		t.Fatal("expected IsRunning to be false after cancel")
	}
}

func TestBotSnapshot(t *testing.T) {
	b := New("bot1", "Bot One", "acct1", nil)
	b.System = "sol"
	b.POI = "earth"
	b.Docked = true
	b.IsCloaked = false
	b.Fuel = 100
	b.MaxFuel = 200
	b.Hull = 80
	b.MaxHull = 100
	b.Credits = 5000

	snap := b.Snapshot()
	if snap.System != "sol" {
		t.Fatalf("expected sol, got %s", snap.System)
	}
	if snap.POI != "earth" {
		t.Fatalf("expected earth, got %s", snap.POI)
	}
	if !snap.Docked {
		t.Fatal("expected docked")
	}
	if snap.IsCloaked {
		t.Fatal("expected not cloaked")
	}
	if snap.Fuel != 100 {
		t.Fatalf("expected 100 fuel, got %v", snap.Fuel)
	}
	if snap.Credits != 5000 {
		t.Fatalf("expected 5000 credits, got %v", snap.Credits)
	}
}

func TestBotLocationGetSet(t *testing.T) {
	b := New("bot1", "Bot One", "acct1", nil)
	b.SetLocation("mars", "mars_station")
	snap := b.Snapshot()
	if snap.System != "mars" || snap.POI != "mars_station" {
		t.Fatalf("expected mars/mars_station, got %s/%s", snap.System, snap.POI)
	}
}
