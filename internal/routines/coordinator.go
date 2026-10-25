package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

// Coordinator is a placeholder routine.
func Coordinator(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("coordinator")
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
	Registry["coordinator"] = Coordinator
}

