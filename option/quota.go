package option

type QuotaServiceOptions struct {
	ListenOptions
	CachePath string `json:"cache_path,omitempty"`
	InboundTLSOptionsContainer

	// SubscriptionServer/Port are the public host/port clients should connect
	// to for each user's ss:// subscription link. Inbounds typically listen
	// on "::" and carry no public address, so the portal needs one
	// configured here to render subscription links/QR codes. SubscriptionPort
	// is optional and overrides every user's own inbound listen_port, for
	// setups where the externally reachable port differs (e.g. NAT/port
	// forwarding); omit it to use each user's inbound listen_port as-is.
	SubscriptionServer string `json:"subscription_server,omitempty"`
	SubscriptionPort   uint16 `json:"subscription_port,omitempty"`
}

type QuotaPortalOutboundOptions struct {
	MemberOutboundConfigDirectory string   `json:"member_outbound_config_directory,omitempty"`
	PortalAddresses               []string `json:"portal_addresses,omitempty"`

	// ConnectivityTestURL is the URL probed through a member's custom
	// Shadowsocks outbound when it is saved or tested. Defaults to
	// https://www.gstatic.com/generate_204, which is unreachable from many
	// mainland-China landing servers; override it with a target the intended
	// landing servers can actually reach (e.g. http://cp.cloudflare.com).
	ConnectivityTestURL string `json:"connectivity_test_url,omitempty"`
}
