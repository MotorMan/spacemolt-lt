# Migration Guide: TypeScript -> Go

This guide covers migrating from the TypeScript botrunner (`spacemolt_botrunner/`)
to the Go rewrite (`spacemolt-lt/`).

## Overview

The Go rewrite replaces the TypeScript bot runner with a native Go binary that
still wraps `@spacemolt/lib` via a TypeScript bridge for game account connections.
Routines, settings, and sync are reimplemented in Go.

## Build & Run

```sh
# Cross-compile for all platforms
./build-all.sh        # Linux/macOS
.\build-all.ps1       # Windows PowerShell

# Or build for the current platform
go build ./cmd/spacemolt-lt

# Run
go run ./cmd/spacemolt-lt
Dashboard: http://localhost:3000
```

## Routines (31 total)

All TS routines are ported to Go. Register a routine in `internal/routines/routines.go`
via the `Registry` map, then start it via the web dashboard action `start` with
`routine` set to the routine name.

| TS Routine | Go Routine | Status |
|------------|-----------|--------|
| idle | idle | Complete |
| return_home | return_home | Complete |
| command_receiver | command_receiver | Complete |
| miner | miner | Complete |
| trader | trader | Complete |
| trade_buyer | trade_buyer | Complete |
| faction_trader | faction_trader | Complete |
| cargo_mover | cargo_mover | Complete |
| hunter | hunter | Complete |
| fleet_hunter_commander | fleet_hunter_commander | Complete |
| fleet_hunter_subordinate | fleet_hunter_subordinate | Complete |
| salvager | salvager | Complete |
| escort | escort | Complete |
| explorer | explorer | Complete |
| fuel_cell_seller | fuel_cell_seller | Complete |
| fuel_transport | fuel_transport | Complete |
| fuel_service | fuel_service | Complete |
| civilian_transport | civilian_transport | Complete |
| module_seller | module_seller | Complete |
| market_streamer | market_streamer | Complete |
| ai | ai | Complete |
| stealth_grind | stealth_grind | Complete |
| pathfinder_test | pathfinder_test | Complete |
| craft_trade | craft_trade | Complete |
| crafter | crafter | Complete |
| craft_goals | craft_goals | Complete |
| rescue | rescue | Stub |
| fuel_rescue | fuel_rescue | Stub |
| cleanup | cleanup | Stub |
| coordinator | coordinator | Stub |

## Client Sync

The Go sync package supports master/slave/light/market modes. Configure in
`data/settings.json` under the `sync` key:

```json
{
  "sync": {
    "enabled": true,
    "mode": "master",
    "label": "my-master",
    "pollIntervalSec": 30
  }
}
```

HTTP endpoints (master only):
- `GET /api/client-sync/hello` - client info + connected clients
- `POST /api/client-sync/heartbeat` - client heartbeat
- `POST /api/client-sync/catalog-version` - catalog version orchestration
- `GET /api/client-sync/catalog-state` - catalog sync state
- `POST /api/client-sync/market-query` - market deal query
- `POST /api/client-sync/bot-status` - push bot status

## AI Service

The AI routine uses an OpenAI-compatible LLM endpoint (Ollama, etc.).
Configure in `data/settings.json` under the `ai` key:

```json
{
  "ai": {
    "model": "llama3.2",
    "baseUrl": "http://localhost:11434/v1",
    "apiKey": "ollama",
    "cycleIntervalSec": 10
  }
}
```

Or via environment variables: `OPENAI_COMPAT_BASE_URL`, `OPENAI_COMPAT_API_KEY`, `AI_MODEL`.

## Watchdog

- Windows: `watchdog.bat`
- Linux: systemd unit `spacemolt-lt.service` (restarts on exit code 100)

## Metrics

- `GET /health` - basic health + bot count
- `GET /metrics` - goroutine count, memory, GC stats, CPU cores
- pprof: `net/http/pprof` is registered on the default mux

## Settings Compatibility

The Go rewrite reads `data/settings.json` with the same top-level keys as TS:
`clerkApiKey`, `clerkApiKey2`, `sync`, `ai`, `miner`, `trader`, `trade_buyer`,
`faction_trader`, `cargo_mover`, `hunter`, `salvager`, `escort`, `explorer`,
`fuel_cell_seller`, `fuel_transport`, `fuel_service`, `civilian_transport`,
`module_seller`, `market_streamer`, `stealth_grind`, `craft_trade`, `crafter`,
`craft_goals`.

## Known Differences from TS

1. **Bridge**: The Go bridge wraps `@spacemolt/lib` via a TypeScript bridge
   process. Game commands are sent via `exec(id, command, params)`.
2. **FileStore**: Writes are debounced (200ms) via `WriteBuffered()`.
3. **Routines**: All run as goroutines with context cancellation.
4. **No Bun required**: Only Go is needed to build/run. Bun is still needed
   for the bridge TypeScript code.
