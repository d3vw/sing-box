package option

import "github.com/sagernet/sing/common/byteformats"

type VMessInboundOptions struct {
	ListenOptions
	Users []VMessUser `json:"users,omitempty"`
	InboundTLSOptionsContainer
	Multiplex *InboundMultiplexOptions `json:"multiplex,omitempty"`
	Transport *V2RayTransportOptions   `json:"transport,omitempty"`
}

type VMessUser struct {
	Name           string                   `json:"name"`
	UUID           string                   `json:"uuid"`
	AlterId        int                      `json:"alterId,omitempty"`
	QuotaBytes     *byteformats.MemoryBytes `json:"quota_bytes,omitempty"`
	Admin          bool                     `json:"admin,omitempty"`
	RateLimitRead  *RateLimit               `json:"rate_limit_read,omitempty"`
	RateLimitWrite *RateLimit               `json:"rate_limit_write,omitempty"`
}

type VMessOutboundOptions struct {
	DialerOptions
	ServerOptions
	UUID                string      `json:"uuid"`
	Security            string      `json:"security"`
	AlterId             int         `json:"alter_id,omitempty"`
	GlobalPadding       bool        `json:"global_padding,omitempty"`
	AuthenticatedLength bool        `json:"authenticated_length,omitempty"`
	Network             NetworkList `json:"network,omitempty"`
	OutboundTLSOptionsContainer
	PacketEncoding string                    `json:"packet_encoding,omitempty"`
	Multiplex      *OutboundMultiplexOptions `json:"multiplex,omitempty"`
	Transport      *V2RayTransportOptions    `json:"transport,omitempty"`
}
