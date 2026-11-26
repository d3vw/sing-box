package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sagernet/sing-box/adapter"
	boxOutbound "github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	ssoutbound "github.com/sagernet/sing-box/protocol/shadowsocks"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterPortalOutbound(registry *boxOutbound.Registry) {
	boxOutbound.Register[option.QuotaPortalOutboundOptions](registry, C.TypeQuotaPortal, newPortalOutbound)
}

type memberOutboundConnectivityTester func(ctx context.Context, inboundTag, server string, serverPort int, method, password string, mux, padding bool) (uint16, error)

type portalOutbound struct {
	boxOutbound.Adapter
	ctx                              context.Context
	router                           adapter.Router
	logger                           log.ContextLogger
	manager                          *Manager
	memberOutboundConfigDirectory    string
	memberOutboundConnectivityTester memberOutboundConnectivityTester
	connectivityTestURL              string
	portalAddrs                      []netip.Addr
	liveOutbounds                    sync.Map // "inboundTag:userName" -> adapter.Outbound
	// profilesAccess serializes changes to the members' node files.
	profilesAccess sync.Mutex
	// reload reloads the config, by default with SIGHUP; tests replace it.
	reload func()
	// buildOutbound builds a node's outbound; tests replace it.
	buildOutbound func(inboundTag, userName string, profile *outboundProfile) (adapter.Outbound, error)
}

func liveOutboundKey(inboundTag, userName string) string {
	return inboundTag + ":" + userName
}

func newPortalOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.QuotaPortalOutboundOptions) (adapter.Outbound, error) {
	manager := service.FromContext[*Manager](ctx)
	if manager == nil {
		return nil, fmt.Errorf("quota-portal outbound requires a quota service to be configured")
	}
	var addrs []netip.Addr
	for _, s := range options.PortalAddresses {
		if addr, err := netip.ParseAddr(s); err == nil {
			addrs = append(addrs, addr)
		}
	}
	if len(addrs) == 0 {
		addrs = []netip.Addr{
			netip.MustParseAddr("203.0.113.1"),
			netip.MustParseAddr("6.6.6.6"),
		}
	}
	h := &portalOutbound{
		Adapter:                       boxOutbound.NewAdapter(C.TypeQuotaPortal, tag, []string{N.NetworkTCP, N.NetworkUDP}, nil),
		ctx:                           ctx,
		router:                        router,
		logger:                        logger,
		manager:                       manager,
		memberOutboundConfigDirectory: options.MemberOutboundConfigDirectory,
		connectivityTestURL:           options.ConnectivityTestURL,
		portalAddrs:                   addrs,
	}
	manager.SetPortal(addrs, h.liveOutboundTag)
	if overrideRouter, ok := router.(adapter.OutboundOverrideRouter); ok {
		overrideRouter.AppendOutboundOverride(h)
	}
	return h, nil
}

// liveOutboundTag names the custom outbound a member's traffic goes out
// through, if they have one.
func (h *portalOutbound) liveOutboundTag(inboundTag, userName string) (string, bool) {
	val, ok := h.liveOutbounds.Load(liveOutboundKey(inboundTag, userName))
	if !ok {
		return "", false
	}
	return val.(adapter.Outbound).Tag(), true
}

func (h *portalOutbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage == adapter.StartStatePostStart {
		return h.PostStart()
	}
	return nil
}

func (h *portalOutbound) PostStart() error {
	if h.memberOutboundConfigDirectory == "" {
		return nil
	}
	migrated, err := migrateMemberOutbounds(h.memberOutboundConfigDirectory)
	if err != nil {
		h.logger.Error("quota-portal: migrate custom outbounds: ", err)
	}
	for _, member := range migrated {
		h.logger.Info("quota-portal: moved the custom outbound of ", member, " to a saved node")
	}
	if len(migrated) > 0 {
		// The loaded config still routes these members to the portal by
		// the rules just removed; reload once so their default routing is
		// the server's again.
		reload := h.reload
		if reload == nil {
			reload = func() { _ = syscall.Kill(os.Getpid(), syscall.SIGHUP) }
		}
		go func() {
			time.Sleep(time.Second)
			reload()
		}()
	}
	entries, err := os.ReadDir(h.memberOutboundConfigDirectory)
	if err != nil {
		h.logger.Info("quota-portal: PostStart: ReadDir error: ", err)
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "quota-member-") || !strings.HasSuffix(entry.Name(), ".profiles") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(h.memberOutboundConfigDirectory, entry.Name()))
		if err != nil {
			continue
		}
		var profiles memberProfiles
		if err := json.Unmarshal(data, &profiles); err != nil || profiles.InboundTag == "" {
			h.logger.Info("quota-portal: PostStart: parse error for ", entry.Name(), ": ", err)
			continue
		}
		profile := profiles.active()
		if profile == nil {
			continue
		}
		ob, err := h.createProfileOutbound(profiles.InboundTag, profiles.UserName, profile)
		if err != nil {
			h.logger.Error("quota-portal: PostStart: failed to load node ", profile.Name, " for ", profiles.InboundTag, "/", profiles.UserName, ": ", err)
			continue
		}
		h.liveOutbounds.Store(liveOutboundKey(profiles.InboundTag, profiles.UserName), ob)
		h.logger.Info("quota-portal: PostStart: ", profiles.InboundTag, "/", profiles.UserName, " goes out via node ", profile.Name)
	}
	return nil
}

func (h *portalOutbound) Close() error {
	h.liveOutbounds.Range(func(_, value any) bool {
		if ob, ok := value.(io.Closer); ok {
			_ = ob.Close()
		}
		return true
	})
	return nil
}

func (h *portalOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	inboundTag := ""
	userName := ""
	if metadata := adapter.ContextFrom(ctx); metadata != nil {
		inboundTag = metadata.Inbound
		userName = metadata.User
	}

	if !h.isPortalAddr(destination.Addr) {
		if val, ok := h.liveOutbounds.Load(liveOutboundKey(inboundTag, userName)); ok {
			if h.logger != nil {
				h.logger.DebugContext(ctx, "quota-portal: routing ", inboundTag, " -> ", destination, " via live outbound")
			}
			return val.(adapter.Outbound).DialContext(ctx, network, destination)
		}
		if h.logger != nil {
			h.logger.InfoContext(ctx, "quota-portal: no live outbound for inbound=", inboundTag, " destination=", destination, "; serving portal page")
		}
	}

	client, server := net.Pipe()
	go h.serveHTTP(server, inboundTag, userName)
	return client, nil
}

func (h *portalOutbound) isPortalAddr(addr netip.Addr) bool {
	for _, pa := range h.portalAddrs {
		if addr == pa {
			return true
		}
	}
	return false
}

func (h *portalOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	inboundTag := ""
	userName := ""
	if metadata := adapter.ContextFrom(ctx); metadata != nil {
		inboundTag = metadata.Inbound
		userName = metadata.User
	}
	// The portal page is HTTP/TCP only; UDP is meaningful solely when it can be
	// relayed through the member's custom outbound.
	if val, ok := h.liveOutbounds.Load(liveOutboundKey(inboundTag, userName)); ok {
		if h.logger != nil {
			h.logger.DebugContext(ctx, "quota-portal: routing ", inboundTag, " UDP -> ", destination, " via live outbound")
		}
		return val.(adapter.Outbound).ListenPacket(ctx, destination)
	}
	return nil, fmt.Errorf("quota-portal does not support UDP without a member outbound")
}

func (h *portalOutbound) serveHTTP(conn net.Conn, inboundTag, userName string) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		if content, contentType, ok := portalAsset(req.URL.Path); ok {
			h.writeAsset(conn, req, content, contentType)
			return
		}
	}
	if req.Method == http.MethodGet && req.URL.Path == probePath && inboundTag != "" {
		h.handleProbe(conn, reader, req)
		return
	}
	userState := h.manager.userState(inboundTag, userName)
	isAdmin := inboundTag == "" || (userState != nil && userState.Admin)
	// Every user follows their own connections, and only theirs.
	if req.URL.Path == connectionsPath || req.URL.Path == connectionsClosePath {
		if userState == nil {
			h.writeJSON(conn, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		if req.Method == http.MethodGet && req.URL.Path == connectionsPath {
			h.handleConnections(conn, reader, req, inboundTag, userName)
			return
		}
		if req.Method == http.MethodPost && req.URL.Path == connectionsClosePath {
			h.handleCloseConnections(conn, req, inboundTag, userName)
			return
		}
	}
	if req.Method == http.MethodPost && req.URL.Path == "/quota/reset" {
		h.handleReset(conn, req, isAdmin)
		return
	}
	if req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, profilesPath) && inboundTag != "" {
		h.handleProfiles(conn, req, inboundTag, userName)
		return
	}

	if inboundTag == "" {
		h.writeFailure(conn, req, http.StatusForbidden, "Forbidden", "")
		return
	}
	if req.URL.Path == pageAPIPath {
		h.writeJSON(conn, http.StatusOK, h.buildPage(inboundTag, userName, isAdmin))
		return
	}
	h.writeHTML(conn, http.StatusOK, renderPage(h.buildPage(inboundTag, userName, isAdmin)), nil)
}

func (h *portalOutbound) handleReset(conn net.Conn, req *http.Request, isAdmin bool) {
	if !isAdmin {
		h.writeFailure(conn, req, http.StatusForbidden, "Forbidden", "")
		return
	}
	if err := req.ParseForm(); err != nil {
		h.writeFailure(conn, req, http.StatusBadRequest, "Bad request", "")
		return
	}
	tag := req.Form.Get("tag")
	name := req.Form.Get("name")
	if tag == "" || name == "" {
		h.writeFailure(conn, req, http.StatusBadRequest, "Missing inbound tag or user name", "")
		return
	}
	if !h.manager.Reset(tag, name) {
		h.writeFailure(conn, req, http.StatusNotFound, "User not found", "")
		return
	}
	if wantsJSON(req) {
		h.writeJSON(conn, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	h.writeHTML(conn, http.StatusSeeOther, "", map[string]string{"Location": "/"})
}

// wantsJSON reports whether the dashboard sent the request, which expects a
// JSON result instead of a page or redirect.
func wantsJSON(req *http.Request) bool {
	return strings.Contains(req.Header.Get("Accept"), "application/json")
}

func (h *portalOutbound) writeJSON(conn net.Conn, status int, body any) {
	content, _ := json.Marshal(body)
	conn.Write([]byte(fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		status, http.StatusText(status), len(content), content)))
}

// writeFailure reports an error as JSON {"error": message} to the dashboard,
// or as a page with message as the card text (detail appended) otherwise.
func (h *portalOutbound) writeFailure(conn net.Conn, req *http.Request, status int, message, detail string) {
	if wantsJSON(req) {
		if detail != "" {
			message += ": " + detail
		}
		h.writeJSON(conn, status, map[string]string{"error": message})
		return
	}
	h.writeHTML(conn, status, renderMessagePage(message, detail, ""), nil)
}

// createProfileOutbound builds the Shadowsocks outbound for one of a
// member's nodes, tagged with the node's name for logs and the connections
// list.
func (h *portalOutbound) createProfileOutbound(inboundTag, userName string, profile *outboundProfile) (adapter.Outbound, error) {
	if h.buildOutbound != nil {
		return h.buildOutbound(inboundTag, userName, profile)
	}
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	var multiplexOption *option.OutboundMultiplexOptions
	if profile.Mux || profile.Padding {
		multiplexOption = &option.OutboundMultiplexOptions{
			Enabled: profile.Mux,
			Padding: profile.Padding,
		}
	}
	tag := profile.Name
	if tag == "" {
		tag = memberOutboundTag(inboundTag, userName)
	}
	return ssoutbound.NewOutbound(ctx, h.router, h.logger, tag, option.ShadowsocksOutboundOptions{
		ServerOptions: option.ServerOptions{
			Server:     profile.Server,
			ServerPort: uint16(profile.ServerPort),
		},
		Method:    profile.Method,
		Password:  profile.Password,
		Multiplex: multiplexOption,
	})
}

func (h *portalOutbound) testMemberShadowsocksOutbound(inboundTag, server string, serverPort int, method, password string, mux, padding bool) (uint16, error) {
	tester := h.memberOutboundConnectivityTester
	if tester == nil {
		tester = h.testShadowsocksOutboundConnectivity
	}
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	return tester(ctx, inboundTag, server, serverPort, method, password, mux, padding)
}

func (h *portalOutbound) testShadowsocksOutboundConnectivity(ctx context.Context, inboundTag, server string, serverPort int, method, password string, mux, padding bool) (uint16, error) {
	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var multiplexOption *option.OutboundMultiplexOptions
	if mux || padding {
		multiplexOption = &option.OutboundMultiplexOptions{
			Enabled: mux,
			Padding: padding,
		}
	}
	outbound, err := ssoutbound.NewOutbound(testCtx, h.router, h.logger, "quota-test-"+safeConfigName(inboundTag), option.ShadowsocksOutboundOptions{
		ServerOptions: option.ServerOptions{
			Server:     server,
			ServerPort: uint16(serverPort),
		},
		Method:    method,
		Password:  password,
		Multiplex: multiplexOption,
	})
	if err != nil {
		return 0, err
	}
	if closer, ok := outbound.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	return urltest.URLTest(testCtx, h.connectivityTestURL, outbound)
}

func memberOutboundTag(inboundTag, userName string) string {
	return "quota-" + safeConfigName(inboundTag) + "-" + safeConfigName(userName) + "-custom-out"
}

func safeConfigName(value string) string {
	var sb strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('-')
		}
	}
	if sb.Len() == 0 {
		return "member"
	}
	return sb.String()
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// writeAsset serves a file beside the dashboard; they change only with the
// binary, so browsers keep them for a day.
func (h *portalOutbound) writeAsset(conn net.Conn, req *http.Request, content []byte, contentType string) {
	conn.Write([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: %s\r\nContent-Length: %d\r\nCache-Control: public, max-age=86400\r\nConnection: close\r\n\r\n", contentType, len(content))))
	if req.Method != http.MethodHead {
		conn.Write(content)
	}
}

func (h *portalOutbound) writeHTML(conn net.Conn, status int, body string, headers map[string]string) {
	statusText := http.StatusText(status)
	if statusText == "" {
		statusText = "status"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n", status, statusText, len(body)))
	for key, value := range headers {
		sb.WriteString(key)
		sb.WriteString(": ")
		sb.WriteString(value)
		sb.WriteString("\r\n")
	}
	sb.WriteString("\r\n")
	sb.WriteString(body)
	resp := sb.String()
	conn.Write([]byte(resp))
}

// bjtDayStart returns the start of the current BJT day, where a day begins at
// 23:00 (子时), plus the 1h-shifted clock used to derive the date and slot.
func bjtDayStart(now time.Time) (shifted, dayStart time.Time) {
	// Shifting by an hour makes 23:00 the start of the next day's slot 0.
	shifted = now.In(quotaLocation).Add(time.Hour)
	dayStart = time.Date(shifted.Year(), shifted.Month(), shifted.Day(), 0, 0, 0, 0, quotaLocation).Add(-time.Hour)
	return
}
