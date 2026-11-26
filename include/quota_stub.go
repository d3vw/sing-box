//go:build !with_quota

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

func registerQuotaService(registry *service.Registry) {
	service.Register[option.QuotaServiceOptions](registry, C.TypeQuota, func(ctx context.Context, logger log.ContextLogger, tag string, options option.QuotaServiceOptions) (adapter.Service, error) {
		return nil, E.New(`quota is not included in this build, rebuild with -tags with_quota`)
	})
}

func registerQuotaOutbound(registry *outbound.Registry) {
	outbound.Register[option.QuotaPortalOutboundOptions](registry, C.TypeQuotaPortal, func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.QuotaPortalOutboundOptions) (adapter.Outbound, error) {
		return nil, E.New(`quota is not included in this build, rebuild with -tags with_quota`)
	})
}
