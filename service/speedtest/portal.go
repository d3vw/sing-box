package speedtest

import (
	"context"
	"fmt"
	"net"

	"github.com/sagernet/sing-box/adapter"
	boxOutbound "github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterPortalOutbound(registry *boxOutbound.Registry) {
	boxOutbound.Register[option.SpeedtestPortalOutboundOptions](registry, C.TypeSpeedtestPortal, newPortalOutbound)
}

type portalOutbound struct {
	boxOutbound.Adapter
	svc *Service
}

func newPortalOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.SpeedtestPortalOutboundOptions) (adapter.Outbound, error) {
	svc := service.FromContext[*Service](ctx)
	if svc == nil {
		return nil, fmt.Errorf("speedtest-portal outbound requires a speedtest service to be configured")
	}
	return &portalOutbound{
		Adapter: boxOutbound.NewAdapter(C.TypeSpeedtestPortal, tag, []string{N.NetworkTCP}, nil),
		svc:     svc,
	}, nil
}

func (h *portalOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return net.Dial("tcp", h.svc.Backend())
}

func (h *portalOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, fmt.Errorf("speedtest-portal does not support UDP")
}
