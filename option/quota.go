package option

import (
	"github.com/sagernet/sing/common/byteformats"
	"github.com/sagernet/sing/common/json/badjson"
)

type QuotaServiceOptions struct {
	ListenOptions
	DefaultQuotaBytes *byteformats.MemoryBytes                       `json:"default_quota_bytes,omitempty"`
	Inbounds          *badjson.TypedMap[string, QuotaInboundOptions] `json:"inbounds"`
	CachePath         string                                         `json:"cache_path,omitempty"`
	InboundTLSOptionsContainer
}

type QuotaInboundOptions struct {
	QuotaBytes *byteformats.MemoryBytes `json:"quota_bytes,omitempty"`
}

type QuotaPortalOutboundOptions struct {
	MemberOutboundConfigDirectory string   `json:"member_outbound_config_directory,omitempty"`
	PortalAddresses               []string `json:"portal_addresses,omitempty"`
}
