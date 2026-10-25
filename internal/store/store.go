package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const writeDebounce = 200 * time.Millisecond

type FileStore struct {
	path string
	mu   sync.Mutex

	pending     interface{}
	hasPending  bool
	flushTimer  *time.Timer
	flushCh     chan struct{}
	stopCh      chan struct{}
	doneCh      chan struct{}
}

func New(path string) *FileStore {
	s := &FileStore{
		path:    path,
		flushCh: make(chan struct{}, 1),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
	go s.flushLoop()
	return s
}

func (s *FileStore) Path() string { return s.path }

func (s *FileStore) Read(v interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		backup := s.path + ".corrupt.bak"
		_ = os.WriteFile(backup, data, 0644)
		return fmt.Errorf("corrupt json %s: %w", s.path, err)
	}
	return nil
}

func (s *FileStore) Write(v interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// WriteBuffered buffers the write and flushes after a debounce period,
// coalescing multiple rapid writes into a single disk write.
func (s *FileStore) WriteBuffered(v interface{}) error {
	s.mu.Lock()
	s.pending = v
	s.hasPending = true
	if s.flushTimer == nil {
		s.flushTimer = time.AfterFunc(writeDebounce, func() {
			select {
			case s.flushCh <- struct{}{}:
			default:
			}
		})
	} else {
		s.flushTimer.Reset(writeDebounce)
	}
	s.mu.Unlock()
	return nil
}

// Flush writes any pending buffered data immediately.
func (s *FileStore) Flush() error {
	s.mu.Lock()
	if !s.hasPending {
		s.mu.Unlock()
		return nil
	}
	v := s.pending
	s.hasPending = false
	if s.flushTimer != nil {
		s.flushTimer.Stop()
		s.flushTimer = nil
	}
	s.mu.Unlock()

	return s.Write(v)
}

func (s *FileStore) flushLoop() {
	defer close(s.doneCh)
	for {
		select {
		case <-s.stopCh:
			return
		case <-s.flushCh:
			s.Flush()
		}
	}
}

// Stop halts the flush loop and writes any pending data.
func (s *FileStore) Stop() error {
	close(s.stopCh)
	<-s.doneCh
	return s.Flush()
}

func (s *FileStore) Exists() bool {
	_, err := os.Stat(s.path)
	return err == nil
}
