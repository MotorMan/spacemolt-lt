package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Caller is the subset of Client used by callers that only need to send
// RPC requests and read responses. Extracted as an interface so other
// packages (web, bot) can depend on it without importing the concrete
// Client type, and so it can be mocked in tests.
type Caller interface {
	Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error)
}

type Client struct {
	url     string
	conn    *websocket.Conn
	mu      sync.Mutex
	pending map[int64]chan *Response
	idSeq   int64
	events  chan Event
	done    chan struct{}
	closed  bool
}

func NewClient(u string) *Client {
	return &Client{
		url:     u,
		pending: make(map[int64]chan *Response),
		events:  make(chan Event, 1024),
		done:    make(chan struct{}),
	}
}

func (c *Client) Connect(ctx context.Context) error {
	u, err := url.Parse(c.url)
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	go c.readLoop(conn)
	return nil
}

func (c *Client) readLoop(conn *websocket.Conn) {
	defer func() { _ = conn.Close() }()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			c.mu.Lock()
			c.conn = nil // mark connection as closed so Callers know
			for id, ch := range c.pending {
				ch <- &Response{ID: id, Error: &RPCError{Code: -1, Message: err.Error()}}
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		var env Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		if env.ID != nil {
			c.mu.Lock()
			ch, ok := c.pending[*env.ID]
			if ok {
				delete(c.pending, *env.ID)
			}
			c.mu.Unlock()
			if ok {
				resp := &Response{JSONRPC: env.JSONRPC, ID: *env.ID, Result: env.Result, Error: env.Error}
				ch <- resp
			}
			continue
		}
		// notification/event
		ev := Event{JSONRPC: env.JSONRPC, Method: env.Method, Params: env.Params}
		select {
		case c.events <- ev:
		default:
		}
	}
}

// reconnect redials the WebSocket and starts a new read loop. Safe to call
// from any goroutine; returns nil if already connected or reconnect succeeds.
func (c *Client) reconnect(ctx context.Context) error {
	c.mu.Lock()
	if c.conn != nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, c.url, nil)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	go c.readLoop(conn)
	return nil
}

func (c *Client) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	// Ensure we have a live connection (reconnect if the read loop died).
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.conn == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("not connected")
	}
	c.idSeq++
	id := c.idSeq
	ch := make(chan *Response, 1)
	c.pending[id] = ch
	conn := c.conn

	req := Request{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	err := conn.WriteJSON(req)
	c.mu.Unlock()

	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		// Connection likely closed — try to reconnect once and retry.
		if rerr := c.reconnect(ctx); rerr != nil {
			return nil, fmt.Errorf("write failed: %w; reconnect failed: %w", err, rerr)
		}
		// Retry the call once on the new connection.
		c.mu.Lock()
		if c.conn == nil {
			c.mu.Unlock()
			return nil, fmt.Errorf("write failed: %w; connection closed", err)
		}
		c.idSeq++
		id = c.idSeq
		ch = make(chan *Response, 1)
		c.pending[id] = ch
		conn = c.conn
		req := Request{JSONRPC: "2.0", ID: id, Method: method, Params: params}
		err = conn.WriteJSON(req)
		c.mu.Unlock()
		if err != nil {
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
			return nil, fmt.Errorf("write failed after reconnect: %w", err)
		}
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// ensureConnected returns nil if the client has a live connection, or
// attempts to reconnect and returns nil on success, or an error if it cannot.
func (c *Client) ensureConnected(ctx context.Context) error {
	c.mu.Lock()
	if c.conn != nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	return c.reconnect(ctx)
}

func (c *Client) Events() <-chan Event { return c.events }

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if c.conn != nil {
		_ = c.conn.Close()
	}
}
