//go:build with_dashboard_ui

package include

import (
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/service"
	dashboard_ui "github.com/sagernet/sing-box/service/dashboard_ui"
)

func registerDashboardUIService(registry *service.Registry) {
	dashboard_ui.RegisterService(registry)
}

func registerDashboardUIOutbound(registry *outbound.Registry) {
	dashboard_ui.RegisterPortalOutbound(registry)
}
