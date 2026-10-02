package option

type SpeedtestServiceOptions struct {
	Backend string `json:"backend"` // local speedtest server address, e.g. "127.0.0.1:8080"
}

type SpeedtestPortalOutboundOptions struct{}
