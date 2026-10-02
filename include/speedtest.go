//go:build with_speedtest

package include

import (
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/service/speedtest"
)

func registerSpeedtestService(registry *service.Registry) {
	speedtest.RegisterService(registry)
}

func registerSpeedtestOutbound(registry *outbound.Registry) {
	speedtest.RegisterPortalOutbound(registry)
}
