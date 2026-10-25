package sync

import "time"

type Mode string

const (
	ModeDisabled Mode = "disabled"
	ModeMaster   Mode = "master"
	ModeSlave    Mode = "slave"
	ModeLight    Mode = "light"
	ModeMarket   Mode = "market"
)

type SyncSettings struct {
	Enabled                  bool     `json:"enabled"`
	Mode                     Mode     `json:"mode"`
	MasterURL                string   `json:"masterUrl"`
	APIKey                   string   `json:"apiKey"`
	Password                 string   `json:"password,omitempty"`
	Label                    string   `json:"label"`
	PollIntervalSec          int      `json:"pollIntervalSec"`
	SyncMap                  bool     `json:"syncMap"`
	SyncMarket               bool     `json:"syncMarket"`
	SyncCatalog              bool     `json:"syncCatalog"`
	SyncStats                bool     `json:"syncStats"`
	SyncBotChat              bool     `json:"syncBotChat"`
	SyncPlayerNames          bool     `json:"syncPlayerNames"`
	SyncCoordination         bool     `json:"syncCoordination"`
	SyncCivilianTransport    bool     `json:"syncCivilianTransport"`
	SyncRescue               bool     `json:"syncRescue"`
	SyncWildlife             bool     `json:"syncWildlife"`
	AllowRemoteBotsInDropdowns bool   `json:"allowRemoteBotsInDropdowns"`
	RemoteBotNameStyle       string   `json:"remoteBotNameStyle"`
	PushLocalDiscoveries     bool     `json:"pushLocalDiscoveries"`
	SelfURL                  string   `json:"selfUrl,omitempty"`
	DisabledSyncFiles        []string `json:"disabledSyncFiles,omitempty"`
}

func DefaultSettings() SyncSettings {
	return SyncSettings{
		Enabled:               false,
		Mode:                  ModeDisabled,
		PollIntervalSec:       30,
		SyncMap:               true,
		SyncMarket:            true,
		SyncCatalog:           true,
		SyncStats:             true,
		SyncBotChat:           true,
		SyncPlayerNames:       true,
		SyncCoordination:      true,
		SyncCivilianTransport: true,
		SyncRescue:            true,
		SyncWildlife:          true,
		AllowRemoteBotsInDropdowns: true,
		RemoteBotNameStyle:    "prefix",
		PushLocalDiscoveries:  true,
	}
}

type RegisteredClient struct {
	ClientID             string    `json:"clientId"`
	Label                string    `json:"label"`
	Password             string    `json:"password,omitempty"`
	APIKey               string    `json:"apiKey,omitempty"`
	ConnectedAt          int64     `json:"connectedAt"`
	LastSeen             int64     `json:"lastSeen"`
	IP                   string    `json:"ip,omitempty"`
	SelfURL              string    `json:"selfUrl,omitempty"`
	Light                bool      `json:"light,omitempty"`
	HasMarketData        bool      `json:"hasMarketData,omitempty"`
	BotCount             int       `json:"botCount,omitempty"`
}

type PoiPayload struct {
	SystemID string                 `json:"systemId"`
	POI      map[string]interface{} `json:"poi"`
}

type MarketPayload struct {
	Station string                   `json:"station"`
	Orders  []map[string]interface{} `json:"orders"`
}

type CoordinationPayload struct {
	File  string                 `json:"file"`
	Data  interface{}            `json:"data"`
}

type CreatureClaimPayload struct {
	Claimer   string `json:"claimer"`
	TargetID  string `json:"targetId"`
	TargetName string `json:"targetName"`
	System    string `json:"system"`
	POI       string `json:"poi"`
	ExpiresAt int64  `json:"expiresAt"`
}

type WildlifeDeltaPayload struct {
	System string      `json:"system"`
	Data   interface{} `json:"data"`
}

type PlayerNamePayload struct {
	Name      string `json:"name"`
	Faction   string `json:"faction,omitempty"`
	LastSeen  string `json:"lastSeen,omitempty"`
}

type PassengerPayload struct {
	Manifest map[string]interface{} `json:"manifest"`
}

type BotStatusPush struct {
	Username string                 `json:"username"`
	State    string                 `json:"state"`
	Routine  *string                `json:"routine,omitempty"`
	Credits  *float64               `json:"credits,omitempty"`
	Fuel     *float64               `json:"fuel,omitempty"`
	Stats    map[string]interface{} `json:"stats,omitempty"`
}

type CatalogVersionReport struct {
	Version    *string `json:"version"`
	LastFetched *string `json:"lastFetched"`
}

type CatalogVersionResponse struct {
	OK              bool   `json:"ok"`
	GameServerVersion string `json:"gameServerVersion"`
	Action          string `json:"action"` // none, upload, download_and_upload, master_fetch, accept_catalog
	Catalog         interface{} `json:"catalog,omitempty"`
	Version         *string `json:"version,omitempty"`
}

type CatalogSyncStateClient struct {
	ClientID    string `json:"clientId"`
	Version     string `json:"version,omitempty"`
	LastFetched string `json:"lastFetched,omitempty"`
}

type CatalogSyncState struct {
	GameServerVersion string                   `json:"gameServerVersion"`
	LatestCatalogVersion string               `json:"latestCatalogVersion"`
	LatestCatalogFrom  string               `json:"latestCatalogFrom"`
	Downloader         string               `json:"downloader"`
	Clients            []CatalogSyncStateClient `json:"clients"`
}

type HelloResponse struct {
	OK               bool               `json:"ok"`
	Version          string             `json:"version"`
	ClientID         string             `json:"clientId"`
	ConnectedClients []*RegisteredClient `json:"connectedClients"`
}

type SyncEventType string

const (
	EventPOI            SyncEventType = "poi"
	EventMarket         SyncEventType = "market"
	EventPlayerName     SyncEventType = "playerName"
	EventCoordination   SyncEventType = "coordination"
	EventCivilianTransport SyncEventType = "civilianTransport"
	EventRescue         SyncEventType = "rescue"
	EventWildlife       SyncEventType = "wildlife"
)

type MarketQueryRequest struct {
	ItemID          string  `json:"itemId"`
	MaxPrice        *float64 `json:"maxPrice,omitempty"`
	MinQuantity     *int     `json:"minQuantity,omitempty"`
	RequesterSystemID  string `json:"requesterSystemId,omitempty"`
	RequesterLocation string `json:"requesterLocation,omitempty"`
	TradeType       string  `json:"tradeType,omitempty"`
	StationPoiID    string  `json:"stationPoiId,omitempty"`
	SystemID        string  `json:"systemId,omitempty"`
	BaseStationID   string  `json:"baseStationId,omitempty"`
}

type MarketQueryResponse struct {
	OK          bool    `json:"ok"`
	ItemID      string  `json:"itemId"`
	StationName string  `json:"stationName"`
	SystemID    string  `json:"systemId"`
	StationPoiID string `json:"stationPoiId"`
	Price       float64 `json:"price"`
	Quantity    int     `json:"quantity"`
	Distance    *int    `json:"distance,omitempty"`
	ItemName    string  `json:"itemName,omitempty"`
	LastUpdated string  `json:"lastUpdated,omitempty"`
	Error       string  `json:"error,omitempty"`
}

type MarketQueryResult struct {
	OK      bool                   `json:"ok"`
	Results []MarketQueryResponse `json:"results"`
	Error   string                 `json:"error,omitempty"`
	Source  string                 `json:"source,omitempty"`
}

type WildlifeDeltasResponse struct {
	OK        bool                     `json:"ok"`
	Version   int                      `json:"version"`
	Systems   map[string]interface{}   `json:"systems"`
	NeedsFull bool                     `json:"needsFull"`
}

var nowFunc = time.Now

func nowUnix() int64 {
	return nowFunc().Unix()
}
