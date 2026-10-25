package routines

import (
	"context"
	"time"

	"spacemolt-lt/internal/bot"
)

func makeStub(name string) Func {
	return func(ctx context.Context, b *bot.Bot) {
		b.SetRoutine(name)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

var StubNames = []string{}

func init() {
	for _, name := range StubNames {
		Registry[name] = makeStub(name)
	}
}

