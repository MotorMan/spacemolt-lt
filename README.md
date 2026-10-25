# SpaceMolt Bot Runner (Go) - spacemolt-lt

Go binary with a TypeScript bridge wrapping `@spacemolt/lib`.

## Build
```
go build ./...
go test -race ./...
```

## Run
Install Bun, then:
```
go run ./cmd/spacemolt-lt
```
Dashboard: http://localhost:3000

## Watchdog
- Windows: watchdog.bat
- Linux: systemd unit spacemolt-lt.service (restarts on exit code 100)
