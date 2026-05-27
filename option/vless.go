package option

import "github.com/sagernet/sing/common/byteformats"

type VLESSInboundOptions struct {
	ListenOptions
	Users []VLESSUser `json:"users,omitempty"`
	InboundTLSOptionsContainer
	Multiplex *InboundMultiplexOptions `json:"multiplex,omitempty"`
	Transport *V2RayTransportOptions   `json:"transport,omitempty"`
}

type VLESSUser struct {
	Name           string                   `json:"name"`
	UUID           string                   `json:"uuid"`
	Flow           string                   `json:"flow,omitempty"`
	QuotaBytes     *byteformats.MemoryBytes `json:"quota_bytes,omitempty"`
	Admin          bool                     `json:"admin,omitempty"`
	RateLimitRead  *RateLimit               `json:"rate_limit_read,omitempty"`
	RateLimitWrite *RateLimit               `json:"rate_limit_write,omitempty"`
}

type VLESSOutboundOptions struct {
	DialerOptions
	ServerOptions
	UUID    string      `json:"uuid"`
	Flow    string      `json:"flow,omitempty"`
	Network NetworkList `json:"network,omitempty"`
	OutboundTLSOptionsContainer
	Multiplex      *OutboundMultiplexOptions `json:"multiplex,omitempty"`
	Transport      *V2RayTransportOptions    `json:"transport,omitempty"`
	PacketEncoding *string                   `json:"packet_encoding,omitempty"`
}
