//go:build with_quota

package include

import (
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/service/quota"
)

func registerQuotaService(registry *service.Registry) {
	quota.RegisterService(registry)
}

func registerQuotaOutbound(registry *outbound.Registry) {
	quota.RegisterPortalOutbound(registry)
}
