package quota

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	boxService "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/common/listener"
	boxTLS "github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
	aTLS "github.com/sagernet/sing/common/tls"
	"github.com/sagernet/sing/service"

	"github.com/go-chi/chi/v5"
	"golang.org/x/net/http2"
)

var (
	_ adapter.Service             = (*Service)(nil)
	_ adapter.ConnectionTracker   = (*Service)(nil)
	_ adapter.ConnectionInspector = (*Service)(nil)
)

func RegisterService(registry *boxService.Registry) {
	boxService.Register[option.QuotaServiceOptions](registry, C.TypeQuota, NewService)
}

type Service struct {
	boxService.Adapter
	ctx            context.Context
	cancel         context.CancelFunc
	logger         log.ContextLogger
	manager        *Manager
	router         adapter.Router
	outbound       adapter.OutboundManager
	listener       *listener.Listener
	tlsConfig      boxTLS.ServerConfig
	httpServer     *http.Server
	cachePath      string
	saveTicker     *time.Ticker
	lastSavedCache []byte
	cacheMutex     sync.Mutex
}

func NewService(ctx context.Context, logger log.ContextLogger, tag string, options option.QuotaServiceOptions) (adapter.Service, error) {
	ctx, cancel := context.WithCancel(ctx)
	manager := NewManager()
	router := service.FromContext[adapter.Router](ctx)
	if router == nil {
		return nil, E.New("missing router")
	}
	outbound := service.FromContext[adapter.OutboundManager](ctx)
	if outbound == nil {
		return nil, E.New("missing outbound manager")
	}
	s := &Service{
		Adapter:   boxService.NewAdapter(C.TypeQuota, tag),
		ctx:       ctx,
		cancel:    cancel,
		logger:    logger,
		manager:   manager,
		router:    router,
		outbound:  outbound,
		cachePath: options.CachePath,
	}
	router.AppendTracker(s)
	_ = service.ContextWith(ctx, manager)
	if options.Listen != nil || options.ListenPort != 0 {
		chiRouter := chi.NewRouter()
		NewAPIServer(logger, manager).Route(chiRouter)
		s.listener = listener.New(listener.Options{
			Context: ctx,
			Logger:  logger,
			Network: []string{N.NetworkTCP},
			Listen:  options.ListenOptions,
		})
		s.httpServer = &http.Server{Handler: chiRouter}
		if options.TLS != nil {
			tlsConfig, tlsErr := boxTLS.NewServer(ctx, logger, common.PtrValueOrDefault(options.TLS))
			if tlsErr != nil {
				return nil, tlsErr
			}
			s.tlsConfig = tlsConfig
		}
	}
	return s, nil
}

func (s *Service) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	// Auto-discover users with quota_bytes from inbounds
	inboundManager := service.FromContext[adapter.InboundManager](s.ctx)
	if inboundManager != nil {
		for _, ib := range inboundManager.Inbounds() {
			if provider, ok := ib.(adapter.QuotaUserProvider); ok {
				for _, u := range provider.QuotaUsers() {
					s.manager.AddUser(ib.Tag(), u.Name, u.QuotaBytes, u.Admin)
					if u.RateLimitRead > 0 || u.RateLimitWrite > 0 {
						s.manager.SetUserRateLimit(ib.Tag(), u.Name, u.RateLimitRead, u.RateLimitWrite)
						s.logger.Info("quota: applied static rate limit for user ", u.Name, " in inbound ", ib.Tag(), ": read=", u.RateLimitRead, " B/s, write=", u.RateLimitWrite, " B/s")
					}
				}
			}
		}
	}
	if err := s.loadCache(); err != nil {
		s.logger.Error(E.Cause(err, "load cache"))
	}
	if s.cachePath != "" {
		s.saveTicker = time.NewTicker(time.Minute)
		go s.loopSaveCache()
	}
	if s.httpServer == nil || s.listener == nil {
		return nil
	}
	if s.tlsConfig != nil {
		if err := s.tlsConfig.Start(); err != nil {
			return E.Cause(err, "create TLS config")
		}
	}
	tcpListener, err := s.listener.ListenTCP()
	if err != nil {
		return err
	}
	if s.tlsConfig != nil {
		if !common.Contains(s.tlsConfig.NextProtos(), http2.NextProtoTLS) {
			s.tlsConfig.SetNextProtos(append([]string{"h2"}, s.tlsConfig.NextProtos()...))
		}
		tcpListener = aTLS.NewListener(tcpListener, s.tlsConfig)
	}
	go func() {
		err = s.httpServer.Serve(tcpListener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("serve error: ", err)
		}
	}()
	return nil
}

func (s *Service) loopSaveCache() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.saveTicker.C:
			if err := s.saveCache(); err != nil {
				s.logger.Error(E.Cause(err, "save cache"))
			}
		}
	}
}

func (s *Service) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.saveTicker != nil {
		s.saveTicker.Stop()
	}
	if err := s.saveCache(); err != nil {
		s.logger.Error(E.Cause(err, "save cache"))
	}
	return common.Close(
		common.PtrOrNil(s.httpServer),
		common.PtrOrNil(s.listener),
		s.tlsConfig,
	)
}

func (s *Service) isPortalDestination(metadata adapter.InboundContext) bool {
	for _, r := range s.router.Rules() {
		if !r.Match(&metadata) {
			continue
		}
		action, ok := r.Action().(*R.RuleActionRoute)
		if !ok {
			break
		}
		ob, loaded := s.outbound.Outbound(action.Outbound)
		if loaded && ob.Type() == C.TypeQuotaPortal {
			return true
		}
		break
	}
	return false
}

func (s *Service) CheckConnection(ctx context.Context, metadata adapter.InboundContext) error {
	if s.isPortalDestination(metadata) {
		return nil
	}
	return s.manager.CheckConnection(ctx, metadata)
}

func (s *Service) CheckPacketConnection(ctx context.Context, metadata adapter.InboundContext) error {
	if s.isPortalDestination(metadata) {
		return nil
	}
	return s.manager.CheckPacketConnection(ctx, metadata)
}

func (s *Service) RoutedConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) net.Conn {
	return s.manager.RoutedConnection(ctx, conn, metadata, matchedRule, matchOutbound)
}

func (s *Service) RoutedPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) N.PacketConn {
	return s.manager.RoutedPacketConnection(ctx, conn, metadata, matchedRule, matchOutbound)
}
