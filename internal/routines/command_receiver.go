package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

// CommandReceiver keeps the bot running and ready to receive fleet commands.
func CommandReceiver(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("command_receiver")
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
	Registry["command_receiver"] = CommandReceiver
}

