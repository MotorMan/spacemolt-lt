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

func (c *Client) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
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
	data, _ := json.Marshal(req)
	err := conn.WriteJSON(req)
	c.mu.Unlock()
	_ = data

	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
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
