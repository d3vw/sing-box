package speedtest

import (
	"context"

	"github.com/sagernet/sing-box/adapter"
	boxService "github.com/sagernet/sing-box/adapter/service"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"
)

var _ adapter.Service = (*Service)(nil)

func RegisterService(registry *boxService.Registry) {
	boxService.Register[option.SpeedtestServiceOptions](registry, C.TypeSpeedtest, NewService)
}

type Service struct {
	boxService.Adapter
	backend string
}

func NewService(ctx context.Context, logger log.ContextLogger, tag string, options option.SpeedtestServiceOptions) (adapter.Service, error) {
	s := &Service{
		Adapter: boxService.NewAdapter(C.TypeSpeedtest, tag),
		backend: options.Backend,
	}
	service.MustRegister[*Service](ctx, s)
	return s, nil
}

func (s *Service) Start(stage adapter.StartStage) error {
	return nil
}

func (s *Service) Close() error {
	return nil
}

func (s *Service) Backend() string {
	return s.backend
}
