#!/bin/bash
# SpaceMolt LT Watchdog
# Restarts the client if it exits with code 100 (mass disconnect restart request)
# Normal shutdown (exit code 0) will not trigger restart

set -euo pipefail

RESTART_DELAY=30
MANUAL_RESTART_DELAY=5
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BINARY_NAME="spacemolt-lt"

echo "========================================"
echo "SpaceMolt LT Watchdog"
echo "========================================"
echo "" 
echo "Configuration:"
echo "  - Restart delay: $RESTART_DELAY seconds (mass disconnect)"
echo "  - Manual restart delay: $MANUAL_RESTART_DELAY seconds (user-initiated)"
echo "  - Working directory: $SCRIPT_DIR"
echo "  - Binary: $BINARY_NAME"
echo "  - Git pull on start: enabled"
echo ""
echo "Exit codes:"
echo "  - 0: Normal shutdown (no restart)"
echo "  - 100: Restart requested (mass disconnect detected)"
echo "  - 101: Restart requested (user-initiated, e.g. to apply updates)"
echo "  - Other: Unexpected exit (no restart)"
echo ""
echo "Press Ctrl+C to stop the watchdog."
echo "========================================"
echo ""

while true; do
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Starting SpaceMolt LT..."
    echo ""

    set +e
    cd "$SCRIPT_DIR"

    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Building Go binary..."
    go build -o "$BINARY_NAME" ./cmd/spacemolt-lt
    BUILD_EXIT=$?
    if [ "$BUILD_EXIT" -ne 0 ]; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] Build failed with exit code $BUILD_EXIT"
        break
    fi
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Build complete."
    echo ""

    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Running git pull..."
    git pull || echo "Warning: git pull failed or not a git repository"
    echo ""

    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Installing bridge dependencies..."
    bun install --cwd bridge || echo "Warning: bun install failed"
    echo ""

    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Launching $BINARY_NAME..."
    ./"$BINARY_NAME"
    EXIT_CODE=$?
    set -e

    echo ""
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] spacemolt-lt exited with code $EXIT_CODE"

    if [ "$EXIT_CODE" -eq 0 ]; then
        echo ""
        echo "=== Normal shutdown - no restart ==="
        echo ""
        break
    elif [ "$EXIT_CODE" -eq 100 ]; then
        echo ""
        echo "=== Restart requested (mass disconnect) ==="
        echo ""
        echo "Waiting $RESTART_DELAY seconds before restart..."
        sleep "$RESTART_DELAY"
        echo ""
        echo "=== Restarting spacemolt-lt ==="
        echo ""
    elif [ "$EXIT_CODE" -eq 101 ]; then
        echo ""
        echo "=== Restart requested (user-initiated) ==="
        echo ""
        echo "Waiting $MANUAL_RESTART_DELAY seconds before restart..."
        sleep "$MANUAL_RESTART_DELAY"
        echo ""
        echo "=== Restarting spacemolt-lt ==="
        echo ""
    else
        echo ""
        echo "=== Unexpected exit code $EXIT_CODE - no restart ==="
        echo ""
        break
    fi
done

echo "Watchdog stopped."
