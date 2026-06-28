package dashboard_ui

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"sync"

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
	boxOutbound.Register[option.DashboardUIPortalOutboundOptions](registry, C.TypeDashboardUIPortal, newPortalOutbound)
}

type portalOutbound struct {
	boxOutbound.Adapter
	ctx        context.Context
	svc        *Service
	listener   *chanListener
	httpServer *http.Server
}

func newPortalOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.DashboardUIPortalOutboundOptions) (adapter.Outbound, error) {
	svc := service.FromContext[*Service](ctx)
	if svc == nil {
		return nil, fmt.Errorf("dashboard-ui-portal outbound requires a dashboard-ui service to be configured")
	}
	return &portalOutbound{
		Adapter: boxOutbound.NewAdapter(C.TypeDashboardUIPortal, tag, []string{N.NetworkTCP}, nil),
		ctx:     ctx,
		svc:     svc,
	}, nil
}

func (h *portalOutbound) PostStart() error {
	h.listener = newChanListener()
	h.httpServer = &http.Server{Handler: h.buildHandler()}
	go h.httpServer.Serve(h.listener) //nolint:errcheck
	return nil
}

func (h *portalOutbound) buildHandler() http.Handler {
	static := h.svc.StaticHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if apiHandler := h.svc.APIHandler(); apiHandler != nil {
				apiHandler.ServeHTTP(w, r)
				return
			}
			http.Error(w, "API service unavailable", http.StatusBadGateway)
			return
		}
		static.ServeHTTP(w, r)
	})
}

func (h *portalOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	client, server := net.Pipe()
	h.listener.push(server)
	return client, nil
}

func (h *portalOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, fmt.Errorf("dashboard-ui-portal does not support UDP")
}

func (h *portalOutbound) Close() error {
	if h.httpServer != nil {
		h.httpServer.Close()
	}
	if h.listener != nil {
		h.listener.Close()
	}
	return nil
}

// chanListener is a net.Listener that accepts connections pushed via push().
type chanListener struct {
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newChanListener() *chanListener {
	return &chanListener{
		ch:   make(chan net.Conn, 32),
		done: make(chan struct{}),
	}
}

func (l *chanListener) push(conn net.Conn) {
	select {
	case l.ch <- conn:
	case <-l.done:
		conn.Close()
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.ch:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *chanListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "dashboard-ui-portal", Net: "unix"}
}

// spaHandler serves files from root, falling back to index.html for unknown paths.
type spaHandler struct {
	root http.FileSystem
	fs   http.Handler
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	urlPath := path.Clean("/" + r.URL.Path)
	f, err := h.root.Open(urlPath)
	if err == nil {
		f.Close()
		h.fs.ServeHTTP(w, r)
		return
	}
	r2 := new(http.Request)
	*r2 = *r
	r2.URL = new(url.URL)
	*r2.URL = *r.URL
	r2.URL.Path = "/"
	h.fs.ServeHTTP(w, r2)
}
