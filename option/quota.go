package option

type QuotaServiceOptions struct {
	ListenOptions
	CachePath string `json:"cache_path,omitempty"`
	InboundTLSOptionsContainer
}

type QuotaPortalOutboundOptions struct {
	MemberOutboundConfigDirectory string   `json:"member_outbound_config_directory,omitempty"`
	PortalAddresses               []string `json:"portal_addresses,omitempty"`
}
