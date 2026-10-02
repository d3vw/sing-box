//go:build !with_dashboard_ui

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

func registerDashboardUIService(registry *service.Registry) {
	service.Register[option.DashboardUIServiceOptions](registry, C.TypeDashboardUI, func(ctx context.Context, logger log.ContextLogger, tag string, options option.DashboardUIServiceOptions) (adapter.Service, error) {
		return nil, E.New(`dashboard-ui is not included in this build, rebuild with -tags with_dashboard_ui`)
	})
}

func registerDashboardUIOutbound(registry *outbound.Registry) {
	outbound.Register[option.DashboardUIPortalOutboundOptions](registry, C.TypeDashboardUIPortal, func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.DashboardUIPortalOutboundOptions) (adapter.Outbound, error) {
		return nil, E.New(`dashboard-ui is not included in this build, rebuild with -tags with_dashboard_ui`)
	})
}
