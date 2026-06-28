package dashboard_ui

import (
	"context"
	"io/fs"
	"net/http"

	"github.com/sagernet/sing-box/adapter"
	boxService "github.com/sagernet/sing-box/adapter/service"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"
)

var _ adapter.Service = (*Service)(nil)

func RegisterService(registry *boxService.Registry) {
	boxService.Register[option.DashboardUIServiceOptions](registry, C.TypeDashboardUI, NewService)
}

type Service struct {
	boxService.Adapter
	ctx        context.Context
	fileServer http.Handler
	httpFS     http.FileSystem
}

func NewService(ctx context.Context, logger log.ContextLogger, tag string, options option.DashboardUIServiceOptions) (adapter.Service, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	httpFS := http.FS(sub)
	s := &Service{
		Adapter:    boxService.NewAdapter(C.TypeDashboardUI, tag),
		ctx:        ctx,
		httpFS:     httpFS,
		fileServer: http.FileServer(httpFS),
	}
	service.MustRegister[*Service](ctx, s)
	return s, nil
}

func (s *Service) Start(_ adapter.StartStage) error {
	return nil
}

func (s *Service) Close() error {
	return nil
}

// APIHandler looks up the API service handler on every call so that startup
// ordering between services does not matter.
func (s *Service) APIHandler() http.Handler {
	serviceManager := service.FromContext[adapter.ServiceManager](s.ctx)
	if serviceManager == nil {
		return nil
	}
	for _, svc := range serviceManager.Services() {
		if svc.Type() == C.TypeAPI {
			if p, ok := svc.(interface{ Handler() http.Handler }); ok {
				return p.Handler()
			}
		}
	}
	return nil
}

func (s *Service) StaticHandler() http.Handler {
	return &spaHandler{root: s.httpFS, fs: s.fileServer}
}
