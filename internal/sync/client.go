package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	mode   Mode
	master *Master // non-nil only when mode is master
	settings SyncSettings

	httpClient *http.Client
	clientID   string
	label      string
	apiKey     string
	password   string
	masterURL  string
}

func NewClient(mode Mode, settings SyncSettings) *Client {
	return &Client{
		mode:       mode,
		settings:   settings,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		clientID:   generateClientID(),
		label:      settings.Label,
		apiKey:     settings.APIKey,
		password:   settings.Password,
		masterURL:  settings.MasterURL,
	}
}

func (c *Client) Mode() Mode { return c.mode }

func (c *Client) IsMaster() bool { return c.mode == ModeMaster }

func (c *Client) GetMaster() *Master { return c.master }

func (c *Client) SetMaster(m *Master) { c.master = m }

func (c *Client) ClientID() string { return c.clientID }

func generateClientID() string {
	return fmt.Sprintf("client_%d_%d", time.Now().UnixNano()%1000000, time.Now().Unix())
}

// Start begins the sync client. For master mode, it initializes the internal master.
func (c *Client) Start(ctx context.Context) error {
	if c.mode == ModeMaster {
		if c.master == nil {
			c.master = NewMaster(c.settings)
		}
		return nil
	}
	if c.mode == ModeDisabled {
		return nil
	}
	// Slave/light/market: register with master asynchronously.
	go c.runClientLoop(ctx)
	return nil
}

func (c *Client) runClientLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(c.settings.PollIntervalSec) * time.Second)
	defer ticker.Stop()

	// Initial hello/register.
	c.hello(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.heartbeat(ctx)
			c.reportCatalog(ctx)
			c.pushStatus(ctx)
		}
	}
}

func (c *Client) hello(ctx context.Context) {
	if c.masterURL == "" {
		return
	}
	req := map[string]interface{}{
		"clientId": c.clientID,
		"label":    c.label,
		"apiKey":   c.apiKey,
		"light":    c.mode == ModeLight,
	}
	data, _ := json.Marshal(req)
	resp, err := c.httpClient.Post(c.masterURL+"/api/client-sync/hello", "application/json", bytes.NewReader(data))
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func (c *Client) heartbeat(ctx context.Context) {
	if c.masterURL == "" {
		return
	}
	req := map[string]interface{}{"clientId": c.clientID}
	data, _ := json.Marshal(req)
	resp, err := c.httpClient.Post(c.masterURL+"/api/client-sync/heartbeat", "application/json", bytes.NewReader(data))
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func (c *Client) reportCatalog(ctx context.Context) {
	if c.masterURL == "" || !c.settings.SyncCatalog {
		return
	}
	report := CatalogVersionReport{
		Version:    nil,
		LastFetched: nil,
	}
	data, _ := json.Marshal(report)
	resp, err := c.httpClient.Post(c.masterURL+"/api/client-sync/catalog-version", "application/json", bytes.NewReader(data))
	if err != nil {
		return
	}
	_ = resp.Body.Close()

	// Handle accept_catalog action.
	var cresp CatalogVersionResponse
	if err := json.NewDecoder(resp.Body).Decode(&cresp); err != nil {
		return
	}
	if cresp.Action == "accept_catalog" && cresp.Catalog != nil {
		// Client would store the catalog locally here.
		_ = cresp.Catalog
	}
}

func (c *Client) pushStatus(ctx context.Context) {
	if c.masterURL == "" || !c.settings.SyncStats {
		return
	}
	status := BotStatusPush{
		Username: c.label,
		State:    "running",
	}
	data, _ := json.Marshal(status)
	resp, err := c.httpClient.Post(c.masterURL+"/api/client-sync/bot-status", "application/json", bytes.NewReader(data))
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func (c *Client) QueryMarket(req MarketQueryRequest) (*MarketQueryResult, error) {
	if c.masterURL == "" {
		return nil, fmt.Errorf("no master URL")
	}
	data, _ := json.Marshal(req)
	resp, err := c.httpClient.Post(c.masterURL+"/api/client-sync/market-query", "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result MarketQueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}
