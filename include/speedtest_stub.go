//go:build !with_speedtest

package include

import (
	"context"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/service"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
)

func registerSpeedtestService(registry *service.Registry) {
	service.Register[option.SpeedtestServiceOptions](registry, C.TypeSpeedtest, func(ctx context.Context, logger log.ContextLogger, tag string, options option.SpeedtestServiceOptions) (adapter.Service, error) {
		return nil, E.New(`speedtest is not included in this build, rebuild with -tags with_speedtest`)
	})
}

func registerSpeedtestOutbound(registry *outbound.Registry) {
	outbound.Register[option.SpeedtestPortalOutboundOptions](registry, C.TypeSpeedtestPortal, func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.SpeedtestPortalOutboundOptions) (adapter.Outbound, error) {
		return nil, E.New(`speedtest is not included in this build, rebuild with -tags with_speedtest`)
	})
}
