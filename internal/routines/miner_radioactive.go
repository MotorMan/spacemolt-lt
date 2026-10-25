package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

func MinerRadioactive(ctx context.Context, b *bot.Bot) {
	b.SetRoutine("miner_radioactive")
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if b.Bridge != nil {
			_, _ = b.Bridge.Call(ctx, "exec", map[string]interface{}{"id": b.AccountID, "command": "get_status", "params": map[string]interface{}{}})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func init() {
	Registry["miner_radioactive"] = MinerRadioactive
}

