package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

type Func func(ctx context.Context, b *bot.Bot)

var Registry = map[string]Func{
	"idle":             Idle,
	"return_home":      ReturnHome,
	"command_receiver": CommandReceiver,
	"cleanup":          Cleanup,
	"coordinator":      Coordinator,
}

const idleInterval = 30 * time.Second

func Idle(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("idle")
	b.SetState(bot.StateRunning)

	if b.Bridge != nil {
		if err := b.SubscribeToObservation(ctx, true); err != nil {
			_, _ = b.Bridge.Call(ctx, "exec", map[string]interface{}{
				"id":      b.AccountID,
				"command": "log",
				"params":  map[string]interface{}{"level": "warn", "message": "idle: failed to subscribe to observation: " + err.Error()},
			})
		}
	}

	lastSystem := b.System
	lastPOI := b.POI

	ticker := time.NewTicker(idleInterval)
	defer ticker.Stop()
	defer b.ClearObservationState()

	for {
		select {
		case <-ctx.Done():
			b.SetState(bot.StateStopped)
			return
		case <-ticker.C:
		}

		if !b.IsRunning() {
			b.SetState(bot.StateStopped)
			return
		}

		if b.Bridge != nil {
			_, _ = b.ExecCommand(ctx, "get_nearby", nil)
			_ = b.RefreshCargo(ctx)
			_ = b.RefreshLocation(ctx)
		}

		sys, poi := b.GetLocation()
		if sys != lastSystem || poi != lastPOI {
			if b.Bridge != nil {
				_ = b.SubscribeToObservation(ctx, true)
			}
			lastSystem = sys
			lastPOI = poi
		}
	}
}

func Run(ctx context.Context, b *bot.Bot, name string) {
	fn, ok := Registry[name]
	if !ok {
		fn = Idle
	}
	fn(ctx, b)
}

