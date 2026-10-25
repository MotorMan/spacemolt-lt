package bridge

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type Manager struct {
	Dir     string
	Port    int
	BunPath string

	mu      sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	url     string
	running bool
}

func NewManager(dir string, port int) *Manager {
	return &Manager{Dir: dir, Port: port, BunPath: "bun", url: fmt.Sprintf("ws://127.0.0.1:%d", port)}
}

func (m *Manager) URL() string { return m.url }

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return nil
	}
	childCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
		cmd := exec.CommandContext(childCtx, m.BunPath, "run", "index.ts")
	cmd.Dir = m.Dir
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", m.Port))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	m.cmd = cmd
	m.running = true
	go m.watch(childCtx, cmd)
	// wait for port readiness
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", m.Port), 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-childCtx.Done():
			return childCtx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("bridge did not become ready")
}

func (m *Manager) watch(ctx context.Context, cmd *exec.Cmd) {
	_ = cmd.Wait()
	m.mu.Lock()
	m.running = false
	m.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	// restart with backoff
	time.Sleep(2 * time.Second)
	_ = m.Start(context.Background())
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
	}
	m.running = false
}

func BridgeDirDefault() string {
	if p, err := os.Getwd(); err == nil {
		return filepath.Join(p, "bridge")
	}
	return "bridge"
}
