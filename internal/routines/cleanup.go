package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

// Cleanup is a placeholder routine.
func Cleanup(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("cleanup")
	b.SetState(bot.StateRunning)
	defer b.SetState(bot.StateStopped)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func init() {
	Registry["cleanup"] = Cleanup
}

