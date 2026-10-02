package quota

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
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
	"github.com/skip2/go-qrcode"
)

func RegisterPortalOutbound(registry *boxOutbound.Registry) {
	boxOutbound.Register[option.QuotaPortalOutboundOptions](registry, C.TypeQuotaPortal, newPortalOutbound)
}

type memberOutboundConnectivityTester func(ctx context.Context, inboundTag, server string, serverPort int, method, password string, mux, padding bool) (uint16, error)

type memberSSConfig struct {
	InboundTag string `json:"inbound_tag"`
	UserName   string `json:"user_name"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Method     string `json:"method"`
	Password   string `json:"password"`
	Mux        bool   `json:"mux,omitempty"`
	Padding    bool   `json:"padding,omitempty"`
}

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
	return &portalOutbound{
		Adapter:                       boxOutbound.NewAdapter(C.TypeQuotaPortal, tag, []string{N.NetworkTCP, N.NetworkUDP}, nil),
		ctx:                           ctx,
		router:                        router,
		logger:                        logger,
		manager:                       manager,
		memberOutboundConfigDirectory: options.MemberOutboundConfigDirectory,
		connectivityTestURL:           options.ConnectivityTestURL,
		portalAddrs:                   addrs,
	}, nil
}

func (h *portalOutbound) PostStart() error {
	if h.memberOutboundConfigDirectory == "" {
		return nil
	}
	entries, err := os.ReadDir(h.memberOutboundConfigDirectory)
	if err != nil {
		h.logger.Info("quota-portal: PostStart: ReadDir error: ", err)
		return nil
	}
	// Migrate old zz- prefixed fragment files to 00- so they sort before config.json
	// and their route rules take priority over catch-all rules in config.json.
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "zz-quota-member-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		oldPath := filepath.Join(h.memberOutboundConfigDirectory, entry.Name())
		newName := "00-quota-member-" + entry.Name()[len("zz-quota-member-"):]
		newPath := filepath.Join(h.memberOutboundConfigDirectory, newName)
		if err := os.Rename(oldPath, newPath); err != nil {
			h.logger.Info("quota-portal: PostStart: failed to migrate ", entry.Name(), ": ", err)
		} else {
			h.logger.Info("quota-portal: PostStart: migrated ", entry.Name(), " -> ", newName)
		}
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".quota") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(h.memberOutboundConfigDirectory, entry.Name()))
		if err != nil {
			h.logger.Info("quota-portal: PostStart: ReadFile error for ", entry.Name(), ": ", err)
			continue
		}
		var cfg memberSSConfig
		if err := json.Unmarshal(data, &cfg); err != nil || cfg.InboundTag == "" || cfg.Server == "" {
			h.logger.Info("quota-portal: PostStart: parse error for ", entry.Name(), ": ", err)
			continue
		}
		h.logger.Info("quota-portal: PostStart: loading outbound for ", cfg.InboundTag, "/", cfg.UserName, " -> ", cfg.Server, ":", cfg.ServerPort)
		ob, err := h.createLiveSSOutbound(cfg.InboundTag, cfg.UserName, cfg.Server, cfg.ServerPort, cfg.Method, cfg.Password, cfg.Mux, cfg.Padding)
		if err != nil {
			h.logger.Error("quota-portal: PostStart: failed to load live outbound for ", cfg.InboundTag, "/", cfg.UserName, ": ", err)
			continue
		}
		h.liveOutbounds.Store(liveOutboundKey(cfg.InboundTag, cfg.UserName), ob)
		h.logger.Info("quota-portal: PostStart: loaded live outbound for ", cfg.InboundTag, "/", cfg.UserName)
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
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	userState := h.manager.userState(inboundTag, userName)
	isAdmin := inboundTag == "" || (userState != nil && userState.Admin)
	if req.Method == http.MethodPost && req.URL.Path == "/quota/reset" {
		h.handleReset(conn, req, isAdmin)
		return
	}
	if req.Method == http.MethodPost && req.URL.Path == "/outbound/shadowsocks" {
		h.handleSaveShadowsocksOutbound(conn, req, inboundTag, userName, isAdmin)
		return
	}
	if req.Method == http.MethodPost && req.URL.Path == "/outbound/shadowsocks/test" {
		h.handleTestShadowsocksOutbound(conn, req, inboundTag, userName, isAdmin)
		return
	}
	if req.Method == http.MethodPost && req.URL.Path == "/outbound/delete" {
		h.handleDeleteMemberOutbound(conn, inboundTag, userName, isAdmin)
		return
	}

	if inboundTag == "" {
		h.writeHTML(conn, http.StatusForbidden, buildPage("Forbidden", `<div class="card" data-i18n="Forbidden">Forbidden</div>`), nil)
		return
	}
	var body string
	if isAdmin {
		body = h.renderAll(inboundTag, userName)
	} else if snapshot, ok := h.manager.Snapshot(inboundTag, userName); ok {
		var formData *shadowsocksOutboundForm
		if h.memberOutboundConfigDirectory != "" {
			existing := readMemberShadowsocksOutbound(h.memberOutboundConfigDirectory, inboundTag, userName)
			if existing != nil {
				formData = existing
			} else {
				formData = &shadowsocksOutboundForm{}
			}
		}
		subscriptionURI := ""
		if userState != nil {
			subscriptionURI, _ = userState.SubscriptionURI()
		}
		body = buildPage(inboundTag+" / "+userName, renderSnapshotWithHistory(snapshot, false, formData, subscriptionURI, h.renderWeeklyHistory(snapshot)))
	} else {
		body = buildPage(inboundTag+" / "+userName, `<div class="card" data-i18n="No quota configured.">No quota configured.</div>`)
	}
	h.writeHTML(conn, http.StatusOK, body, nil)
}

func (h *portalOutbound) handleReset(conn net.Conn, req *http.Request, isAdmin bool) {
	if !isAdmin {
		h.writeHTML(conn, http.StatusForbidden, buildPage("Forbidden", `<div class="card" data-i18n="Forbidden">Forbidden</div>`), nil)
		return
	}
	if err := req.ParseForm(); err != nil {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", `<div class="card" data-i18n="Bad request">Bad request</div>`), nil)
		return
	}
	tag := req.Form.Get("tag")
	name := req.Form.Get("name")
	if tag == "" || name == "" {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", `<div class="card" data-i18n="Missing inbound tag or user name">Missing inbound tag or user name</div>`), nil)
		return
	}
	if !h.manager.Reset(tag, name) {
		h.writeHTML(conn, http.StatusNotFound, buildPage("Not found", `<div class="card" data-i18n="User not found">User not found</div>`), nil)
		return
	}
	h.writeHTML(conn, http.StatusSeeOther, "", map[string]string{"Location": "/"})
}

type shadowsocksOutboundForm struct {
	Server     string
	ServerPort int
	Method     string
	Password   string
	Mux        bool
	Padding    bool
	Saved      bool
}

func (h *portalOutbound) parseShadowsocksOutboundForm(conn net.Conn, req *http.Request) (shadowsocksOutboundForm, bool) {
	if err := req.ParseForm(); err != nil {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", `<div class="card" data-i18n="Bad request">Bad request</div>`), nil)
		return shadowsocksOutboundForm{}, false
	}
	server := strings.TrimSpace(req.Form.Get("server"))
	method := strings.TrimSpace(req.Form.Get("method"))
	password := req.Form.Get("password")
	port, err := strconv.Atoi(strings.TrimSpace(req.Form.Get("server_port")))
	if err != nil || server == "" || method == "" || password == "" || port < 1 || port > 65535 {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", `<div class="card" data-i18n="Invalid Shadowsocks outbound">Invalid Shadowsocks outbound</div>`), nil)
		return shadowsocksOutboundForm{}, false
	}
	mux := req.Form.Get("mux") == "true" || req.Form.Get("mux") == "on"
	padding := req.Form.Get("padding") == "true" || req.Form.Get("padding") == "on"
	return shadowsocksOutboundForm{Server: server, ServerPort: port, Method: method, Password: password, Mux: mux, Padding: padding}, true
}

func (h *portalOutbound) handleSaveShadowsocksOutbound(conn net.Conn, req *http.Request, inboundTag, userName string, isAdmin bool) {
	if h.memberOutboundConfigDirectory == "" {
		h.writeHTML(conn, http.StatusForbidden, buildPage("Forbidden", `<div class="card" data-i18n="Forbidden">Forbidden</div>`), nil)
		return
	}
	form, ok := h.parseShadowsocksOutboundForm(conn, req)
	if !ok {
		return
	}

	// Validate SS params by creating the live outbound
	newOutbound, err := h.createLiveSSOutbound(inboundTag, userName, form.Server, form.ServerPort, form.Method, form.Password, form.Mux, form.Padding)
	if err != nil {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", fmt.Sprintf(`<div class="card"><span data-i18n="Invalid Shadowsocks outbound">Invalid Shadowsocks outbound</span>: %s</div>`, html.EscapeString(err.Error()))), nil)
		return
	}

	// Run connectivity test. A failure here is advisory only: the outbound
	// parameters are already validated above, and many working landing servers
	// (mainland-China ones especially) cannot reach the default probe URL. Save
	// anyway and surface the failure as a warning so the user can decide.
	var connectivityWarning string
	if _, testErr := h.testMemberShadowsocksOutbound(inboundTag, form.Server, form.ServerPort, form.Method, form.Password, form.Mux, form.Padding); testErr != nil {
		connectivityWarning = testErr.Error()
		h.logger.Warn("quota-portal: save for ", inboundTag, "/", userName, ": connectivity check failed (saving anyway): ", testErr)
	}

	// Detect first-time: live outbound not yet in-memory means route rule not yet pointing to portal
	_, alreadyLive := h.liveOutbounds.Load(liveOutboundKey(inboundTag, userName))
	h.logger.Info("quota-portal: save for ", inboundTag, " alreadyLive=", alreadyLive)

	// Write .quota file for persistence across restarts
	if err := writeMemberSSConfig(h.memberOutboundConfigDirectory, memberSSConfig{
		InboundTag: inboundTag,
		UserName:   userName,
		Server:     form.Server,
		ServerPort: form.ServerPort,
		Method:     form.Method,
		Password:   form.Password,
		Mux:        form.Mux,
		Padding:    form.Padding,
	}); err != nil {
		if closer, ok := newOutbound.(io.Closer); ok {
			_ = closer.Close()
		}
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", fmt.Sprintf(`<div class="card"><span data-i18n="Failed to save">Failed to save</span>: %s</div>`, html.EscapeString(err.Error()))), nil)
		return
	}

	// First-time: write route fragment so sing-box routes this inbound through the portal
	if !alreadyLive {
		// Clean up old per-inbound fragment (migration to per-user)
		_ = os.Remove(filepath.Join(h.memberOutboundConfigDirectory, "00-quota-member-"+safeConfigName(inboundTag)+".json"))
		if content, ferr := buildMemberRouteFragment(inboundTag, userName, h.Tag()); ferr == nil {
			fragPath := filepath.Join(h.memberOutboundConfigDirectory, memberOutboundFragmentName(inboundTag, userName))
			_ = os.MkdirAll(h.memberOutboundConfigDirectory, 0o755)
			tmpPath := fragPath + ".tmp"
			if werr := os.WriteFile(tmpPath, content, 0o600); werr == nil {
				_ = os.Rename(tmpPath, fragPath)
			}
		}
	}

	// Atomically swap live outbound; close old one if present
	if old, loaded := h.liveOutbounds.Swap(liveOutboundKey(inboundTag, userName), newOutbound); loaded {
		if closer, ok := old.(io.Closer); ok {
			_ = closer.Close()
		}
	}

	if connectivityWarning != "" {
		h.writeHTML(conn, http.StatusOK, buildPage("Saved with warning", fmt.Sprintf(
			`<div class="card"><span data-i18n="savedWarning">Outbound saved and activated, but the connectivity check failed:</span><br><br>%s<br><br><span data-i18n="warningHelp">If the server works for you, ignore this. Otherwise use Test to re-check.</span> <a href="/" style="color:#93c5fd" data-i18n="Back">Back</a></div>`,
			html.EscapeString(connectivityWarning))), nil)
	} else {
		h.writeHTML(conn, http.StatusSeeOther, "", map[string]string{"Location": "/"})
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		h.manager.CloseUserConnections(inboundTag, userName)
		if !alreadyLive {
			time.Sleep(100 * time.Millisecond)
			_ = syscall.Kill(os.Getpid(), syscall.SIGHUP)
		}
	}()
}

func (h *portalOutbound) handleTestShadowsocksOutbound(conn net.Conn, req *http.Request, inboundTag, userName string, isAdmin bool) {
	writeJSON := func(status int, body string) {
		resp := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
			status, http.StatusText(status), len(body), body)
		conn.Write([]byte(resp))
	}
	if h.memberOutboundConfigDirectory == "" {
		writeJSON(http.StatusForbidden, `{"error":"forbidden"}`)
		return
	}
	form, ok := h.parseShadowsocksOutboundForm(conn, req)
	if !ok {
		return
	}
	delay, err := h.testMemberShadowsocksOutbound(inboundTag, form.Server, form.ServerPort, form.Method, form.Password, form.Mux, form.Padding)
	if err != nil {
		writeJSON(http.StatusBadRequest, fmt.Sprintf(`{"error":%s}`, jsonString(err.Error())))
		return
	}
	writeJSON(http.StatusOK, fmt.Sprintf(`{"delay":%d}`, delay))
}

func (h *portalOutbound) handleDeleteMemberOutbound(conn net.Conn, inboundTag, userName string, isAdmin bool) {
	if h.memberOutboundConfigDirectory == "" {
		h.writeHTML(conn, http.StatusForbidden, buildPage("Forbidden", `<div class="card" data-i18n="Forbidden">Forbidden</div>`), nil)
		return
	}
	// Remove live outbound immediately
	if val, loaded := h.liveOutbounds.LoadAndDelete(liveOutboundKey(inboundTag, userName)); loaded {
		if closer, ok := val.(io.Closer); ok {
			_ = closer.Close()
		}
	}

	// Delete .quota file
	_ = os.Remove(filepath.Join(h.memberOutboundConfigDirectory, memberSSConfigName(inboundTag, userName)))
	// Delete route fragment (also try old zz- name for migration cleanup)
	_ = os.Remove(filepath.Join(h.memberOutboundConfigDirectory, "zz-quota-member-"+safeConfigName(inboundTag)+".json"))
	// Also clean up old per-inbound fragment (pre-per-user migration)
	_ = os.Remove(filepath.Join(h.memberOutboundConfigDirectory, "00-quota-member-"+safeConfigName(inboundTag)+".json"))
	fragPath := filepath.Join(h.memberOutboundConfigDirectory, memberOutboundFragmentName(inboundTag, userName))
	if err := os.Remove(fragPath); err != nil && !os.IsNotExist(err) {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", fmt.Sprintf(`<div class="card"><span data-i18n="Delete custom outbound failed">Delete custom outbound failed</span>: %s</div>`, html.EscapeString(err.Error()))), nil)
		return
	}
	h.writeHTML(conn, http.StatusSeeOther, "", map[string]string{"Location": "/"})
	go func() {
		time.Sleep(100 * time.Millisecond)
		h.manager.CloseUserConnections(inboundTag, userName)
		time.Sleep(100 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGHUP)
	}()
}

func (h *portalOutbound) createLiveSSOutbound(inboundTag, userName, server string, serverPort int, method, password string, mux, padding bool) (adapter.Outbound, error) {
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	var multiplexOption *option.OutboundMultiplexOptions
	if mux || padding {
		multiplexOption = &option.OutboundMultiplexOptions{
			Enabled: mux,
			Padding: padding,
		}
	}
	return ssoutbound.NewOutbound(ctx, h.router, h.logger, memberOutboundTag(inboundTag, userName), option.ShadowsocksOutboundOptions{
		ServerOptions: option.ServerOptions{
			Server:     server,
			ServerPort: uint16(serverPort),
		},
		Method:    method,
		Password:  password,
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

func writeMemberSSConfig(directory string, cfg memberSSConfig) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(directory, memberSSConfigName(cfg.InboundTag, cfg.UserName))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readMemberShadowsocksOutbound(directory, inboundTag, userName string) *shadowsocksOutboundForm {
	// Try per-user .quota file first
	quotaPath := filepath.Join(directory, memberSSConfigName(inboundTag, userName))
	if data, err := os.ReadFile(quotaPath); err == nil {
		var cfg memberSSConfig
		if err := json.Unmarshal(data, &cfg); err == nil && cfg.Server != "" {
			return &shadowsocksOutboundForm{
				Server:     cfg.Server,
				ServerPort: cfg.ServerPort,
				Method:     cfg.Method,
				Password:   cfg.Password,
				Mux:        cfg.Mux,
				Padding:    cfg.Padding,
				Saved:      true,
			}
		}
	}
	// Fall back to fragment file (old format, for migration)
	path := filepath.Join(directory, "00-quota-member-"+safeConfigName(inboundTag)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var fragment struct {
		Outbounds []struct {
			Server     string `json:"server"`
			ServerPort int    `json:"server_port"`
			Method     string `json:"method"`
			Password   string `json:"password"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(data, &fragment); err != nil || len(fragment.Outbounds) == 0 {
		return nil
	}
	ob := fragment.Outbounds[0]
	if ob.Server == "" {
		return nil
	}
	return &shadowsocksOutboundForm{Server: ob.Server, ServerPort: ob.ServerPort, Method: ob.Method, Password: ob.Password, Saved: true}
}

func buildMemberRouteFragment(inboundTag, userName, portalTag string) ([]byte, error) {
	fragment := struct {
		Route struct {
			Rules []struct {
				Inbound  []string `json:"inbound"`
				AuthUser []string `json:"auth_user,omitempty"`
				Outbound string   `json:"outbound"`
			} `json:"rules"`
		} `json:"route"`
	}{}
	fragment.Route.Rules = append(fragment.Route.Rules, struct {
		Inbound  []string `json:"inbound"`
		AuthUser []string `json:"auth_user,omitempty"`
		Outbound string   `json:"outbound"`
	}{
		Inbound:  []string{inboundTag},
		AuthUser: []string{userName},
		Outbound: portalTag,
	})
	content, err := json.MarshalIndent(fragment, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func memberSSConfigName(inboundTag, userName string) string {
	return "quota-member-" + safeConfigName(inboundTag) + "-" + safeConfigName(userName) + ".quota"
}

func memberOutboundFragmentName(inboundTag, userName string) string {
	return "00-quota-member-" + safeConfigName(inboundTag) + "-" + safeConfigName(userName) + ".json"
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

func (h *portalOutbound) renderAll(selfInboundTag, selfUserName string) string {
	snapshots := h.manager.Snapshots()
	var own, others strings.Builder
	for _, s := range snapshots {
		isSelf := s.InboundTag == selfInboundTag && s.UserName == selfUserName
		var formData *shadowsocksOutboundForm
		subscriptionURI := ""
		if isSelf {
			if h.memberOutboundConfigDirectory != "" {
				existing := readMemberShadowsocksOutbound(h.memberOutboundConfigDirectory, s.InboundTag, s.UserName)
				if existing != nil {
					formData = existing
				} else {
					formData = &shadowsocksOutboundForm{}
				}
			}
			if state := h.manager.userState(s.InboundTag, s.UserName); state != nil {
				subscriptionURI, _ = state.SubscriptionURI()
			}
		}
		card := renderSnapshotWithHistory(s, true, formData, subscriptionURI, h.renderWeeklyHistory(s))
		if isSelf {
			own.WriteString(card)
		} else {
			others.WriteString(card)
		}
	}
	if own.Len() == 0 && others.Len() == 0 {
		return buildPage("No quota data available.", "")
	}
	var sections strings.Builder
	if own.Len() > 0 {
		sections.WriteString(`<section class="quota-group" aria-labelledby="own-quota-heading"><h2 id="own-quota-heading" class="quota-group-title" data-i18n="Your node">Your node</h2>` + own.String() + `</section>`)
	}
	if others.Len() > 0 {
		sections.WriteString(`<section class="quota-group other-users" aria-labelledby="other-quota-heading"><h2 id="other-quota-heading" class="quota-group-title" data-i18n="Other users">Other users</h2><div class="other-user-cards">` + others.String() + `</div></section>`)
	}
	return buildPage("Traffic Quota", sections.String())
}

func (h *portalOutbound) renderWeeklyHistory(snapshot UserSnapshot) string {
	if h.manager.history == nil {
		return ""
	}
	now := time.Now().UTC()
	end := now.Truncate(24 * time.Hour).Add(24 * time.Hour)
	start := end.AddDate(0, 0, -7)
	points, err := h.manager.history.Query(h.ctx, HistoryQuery{
		From: start, To: end, Granularity: "day",
		InboundTag: snapshot.InboundTag, UserName: snapshot.UserName,
	})
	if err != nil {
		h.logger.Error("query weekly quota history: ", err)
		return ""
	}
	byDay := make(map[int64]HistoryPoint, len(points))
	var maximum int64
	var weeklyTotal int64
	for _, point := range points {
		byDay[point.BucketStart] = point
		if point.UsedBytes > maximum {
			maximum = point.UsedBytes
		}
		weeklyTotal += point.UsedBytes
	}
	var bars strings.Builder
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		point := byDay[day.Unix()]
		uplinkHeight, downlinkHeight := 0.0, 0.0
		if maximum > 0 {
			uplinkHeight = float64(point.UplinkBytes) / float64(maximum) * 100
			downlinkHeight = float64(point.DownlinkBytes) / float64(maximum) * 100
		}
		bars.WriteString(fmt.Sprintf(`<div class="history-column" title="%s · Upload %s · Download %s">
  <div class="history-value">%s</div>
  <div class="history-bar"><span class="history-down" style="height:%.2f%%"></span><span class="history-up" style="height:%.2f%%"></span></div>
  <div class="history-day">%s</div>
</div>`, day.Format("Mon 02"), formatBytes(point.UplinkBytes), formatBytes(point.DownlinkBytes), compactBytes(point.UsedBytes), downlinkHeight, uplinkHeight, day.Format("Mon")))
	}
	return fmt.Sprintf(`<div class="history-section">
  <div class="history-header"><div><div class="form-section-title" data-i18n="Last 7 days">Last 7 days</div><div class="history-total">%s</div></div><div class="history-legend"><span class="legend-dot download"></span><span data-i18n="Down">Down</span> <span class="legend-dot upload"></span><span data-i18n="Up">Up</span></div></div>
  <div class="history-chart" role="img" aria-label="Daily traffic usage for the last seven days">%s</div>
  <div class="history-note" data-i18n="Daily totals · UTC · updates every minute">Daily totals · UTC · updates every minute</div>
</div>`, formatBytes(weeklyTotal), bars.String())
}

func renderSnapshot(s UserSnapshot, admin bool, existing *shadowsocksOutboundForm, subscriptionURI string) string {
	return renderSnapshotWithHistory(s, admin, existing, subscriptionURI, "")
}

func renderSnapshotWithHistory(s UserSnapshot, admin bool, existing *shadowsocksOutboundForm, subscriptionURI, history string) string {
	usedPct := 0.0
	if s.QuotaBytes > 0 {
		usedPct = float64(s.UsedBytes) / float64(s.QuotaBytes) * 100
	}
	if usedPct > 100 {
		usedPct = 100
	}
	accentColor := "#3b82f6"
	badgeBg := "#1e3a5f"
	badgeText := "ACTIVE"
	if s.Blocked {
		accentColor = "#ef4444"
		badgeBg = "#3f1f1f"
		badgeText = "BLOCKED"
	}
	resetControl := ""
	if admin {
		resetControl = fmt.Sprintf(`<form method="post" action="/quota/reset">
    <input type="hidden" name="tag" value="%s">
    <input type="hidden" name="name" value="%s">
    <button type="submit" class="reset-button" data-i18n="Reset quota">Reset quota</button>
  </form>`, html.EscapeString(s.InboundTag), html.EscapeString(s.UserName))
	}
	extraControls := ""
	if subscriptionURI != "" {
		extraControls += renderSubscription(subscriptionURI)
	}
	if existing != nil {
		extraControls += renderShadowsocksOutboundForm(existing)
	}
	if extraControls != "" {
		extraControls = `<aside class="card-controls" aria-label="Connection settings">` + extraControls + `</aside>`
	}
	displayName := html.EscapeString(s.InboundTag + " / " + s.UserName)
	return fmt.Sprintf(`<div class="card">
  <section class="quota-overview" aria-label="Traffic usage">
  <div class="card-header">
    <div class="tag-row">
      <span class="tag-name">%s</span>
      <span class="badge" style="background:%s;color:%s">%s</span>
    </div>
    <div class="usage-label">%s <span class="muted" data-i18n="of">of</span> %s</div>
  </div>
  <div class="bar-track"><div class="bar-fill" style="width:%.1f%%;background:%s"></div></div>
  <div class="pct-label" style="color:%s">%.1f%% <span data-i18n="used">used</span></div>
  <div class="stats">
    <div class="stat-item">
      <div class="stat-label" data-i18n="Remaining">Remaining</div>
      <div class="stat-value">%s</div>
    </div>
    <div class="stat-divider"></div>
    <div class="stat-item">
      <div class="stat-label" data-i18n="Upload">Upload</div>
      <div class="stat-value">%s</div>
    </div>
    <div class="stat-divider"></div>
    <div class="stat-item">
      <div class="stat-label" data-i18n="Download">Download</div>
      <div class="stat-value">%s</div>
    </div>
  </div>
  %s
  %s
  </section>
  %s
</div>`,
		displayName,
		badgeBg, accentColor, badgeText,
		formatBytes(s.UsedBytes), formatBytes(s.QuotaBytes),
		usedPct, accentColor,
		accentColor, usedPct,
		formatBytes(s.RemainingBytes),
		formatBytes(s.UplinkBytes),
		formatBytes(s.DownlinkBytes),
		history,
		resetControl,
		extraControls,
	)
}

func renderSubscription(uri string) string {
	png, err := qrcode.Encode(uri, qrcode.Medium, 320)
	qrImg := ""
	if err == nil {
		qrImg = fmt.Sprintf(`<img class="qr-code" alt="ss:// QR code" src="data:image/png;base64,%s">`, base64.StdEncoding.EncodeToString(png))
	}
	return fmt.Sprintf(`<div class="form-section">
    <div class="form-section-title" data-i18n="Subscription">Subscription</div>
    <div class="subscription-section">
      %s
      <div class="subscription-hint" data-i18n="Scan with your client, or copy the link">Scan with your client, or copy the link</div>
      <button type="button" class="copy-button" data-uri="%s" onclick="copySubscription(this)" data-i18n="Copy link">Copy link</button>
    </div>
  </div>
<script>
function copySubscription(btn){
  var textarea=document.createElement('textarea');
  textarea.value=btn.getAttribute('data-uri');
  textarea.style.position='fixed';
  textarea.style.opacity='0';
  document.body.appendChild(textarea);
  textarea.select();
  textarea.setSelectionRange(0,99999);
  try{document.execCommand('copy');}catch(e){}
  document.body.removeChild(textarea);
  setQuotaText(btn,'Copied');
  btn.classList.add('copied');
  setTimeout(function(){setQuotaText(btn,'Copy link');btn.classList.remove('copied');},1500);
}
</script>`, qrImg, html.EscapeString(uri))
}

func renderShadowsocksOutboundForm(existing *shadowsocksOutboundForm) string {
	server, port, method, password := "", "", "", ""
	muxChecked, paddingChecked := "", ""
	if existing != nil {
		server = html.EscapeString(existing.Server)
		if existing.ServerPort > 0 {
			port = strconv.Itoa(existing.ServerPort)
		}
		method = html.EscapeString(existing.Method)
		password = html.EscapeString(existing.Password)
		if existing.Mux {
			muxChecked = "checked"
		}
		if existing.Padding {
			paddingChecked = "checked"
		}
	}
	deleteButton := ""
	if existing != nil && existing.Saved {
		deleteButton = `<form method="post" action="/outbound/delete" style="display:inline">
      <button type="submit" class="delete-outbound-button" data-i18n="Remove">Remove</button>
    </form>`
	}
	return fmt.Sprintf(`<div class="form-section">
    <div class="form-section-header">
      <div class="form-section-title" data-i18n="Custom Outbound">Custom Outbound</div>
      %s
    </div>
    <form method="post" action="/outbound/shadowsocks">
      <div class="field-row">
        <div class="field field-grow">
          <label class="field-label" data-i18n="Server">Server</label>
          <input class="field-input" name="server" placeholder="example.com" autocomplete="off" value="%s" required>
        </div>
        <div class="field field-port">
          <label class="field-label" data-i18n="Port">Port</label>
          <input class="field-input" name="server_port" placeholder="443" inputmode="numeric" value="%s" required>
        </div>
      </div>
      <div class="field-row">
        <div class="field field-grow">
          <label class="field-label" data-i18n="Method">Method</label>
          <input class="field-input" name="method" placeholder="aes-256-gcm" autocomplete="off" value="%s" required>
        </div>
      </div>
      <div class="field-row">
        <div class="field field-grow">
          <label class="field-label" data-i18n="Password">Password</label>
          <input class="field-input" name="password" placeholder="••••••••" type="password" value="%s" required>
        </div>
      </div>
      <div class="field-row checkbox-row">
        <label class="checkbox-container">
          <input type="checkbox" name="mux" value="true" %s>
          <span class="checkmark"></span>
          <span class="checkbox-label" data-i18n="Mux">Mux</span>
        </label>
        <label class="checkbox-container">
          <input type="checkbox" name="padding" value="true" %s>
          <span class="checkmark"></span>
          <span class="checkbox-label" data-i18n="Padding">Padding</span>
        </label>
      </div>
      <div class="form-actions">
        <button type="button" onclick="testOutbound(this)" class="action-button action-button-secondary" data-i18n="Test">Test</button>
        <button type="submit" class="action-button action-button-primary" data-i18n="Save">Save</button>
      </div>
      <div id="test-result" class="test-result" style="display:none"></div>
    </form>
  </div>
<script>
function testOutbound(btn){
  var form=btn.closest('form');
  var data=new FormData(form);
  var params=new URLSearchParams(data);
  btn.disabled=true;
  setQuotaText(btn,'Testing…');
  var r=form.querySelector('.test-result');
  r.removeAttribute('data-i18n');r.className='test-result';r.style.display='none';
  fetch('/outbound/shadowsocks/test',{method:'POST',body:params,headers:{'Content-Type':'application/x-www-form-urlencoded'}})
    .then(function(res){return res.json().then(function(j){return{ok:res.ok,j:j}})})
    .then(function(d){
      r.style.display='block';
      if(d.ok&&d.j.delay!=null){r.className='test-result test-ok';r.textContent=d.j.delay+' ms';}
      else{r.className='test-result test-err';if(d.j.error){r.removeAttribute('data-i18n');r.textContent=d.j.error;}else{setQuotaText(r,'Unknown error');}}
    })
    .catch(function(e){r.style.display='block';r.className='test-result test-err';setQuotaText(r,'Request failed');})
    .finally(function(){btn.disabled=false;setQuotaText(btn,'Test');});
}
</script>`, deleteButton, server, port, method, password, muxChecked, paddingChecked)
}

func buildPage(title, content string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>%s — Quota</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
%s
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{
  font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;
  font-size:14px;
  background:#0a0a0a;
  color:#e2e2e2;
  min-height:100vh;
  display:flex;
  flex-direction:column;
  align-items:center;
  justify-content:flex-start;
  padding:clamp(20px,4vw,64px) 24px;
}
.page-title{
  font-size:11px;
  letter-spacing:0.12em;
  text-transform:uppercase;
  color:#9ba3b0;
  margin-bottom:24px;
  width:100%%;
  max-width:1120px;
}
.cards{display:flex;flex-direction:column;gap:16px;width:100%%;max-width:1120px}
.card{
  background:#141414;
  border:1px solid #222;
  border-radius:16px;
  padding:28px;
}
.card-header{margin-bottom:16px}
.tag-row{display:flex;align-items:center;justify-content:space-between;margin-bottom:6px}
.tag-name{font-size:16px;font-weight:600;color:#f0f0f0;letter-spacing:-0.01em}
.badge{
  font-size:10px;
  font-weight:700;
  letter-spacing:0.08em;
  text-transform:uppercase;
  padding:3px 8px;
  border-radius:6px;
}
.usage-label{font-size:22px;font-weight:700;color:#f0f0f0;letter-spacing:-0.02em}
.usage-label .muted{font-size:14px;font-weight:400;color:#555}
.bar-track{background:#1e1e1e;border-radius:99px;height:6px;margin:16px 0 6px}
.bar-fill{height:6px;border-radius:99px;transition:width .4s ease}
.pct-label{font-size:11px;font-weight:600;letter-spacing:0.04em;margin-bottom:20px}
.stats{display:flex;align-items:center;gap:0;border-top:1px solid #1e1e1e;padding-top:16px}
.stat-item{flex:1;text-align:center}
.stat-divider{width:1px;height:32px;background:#1e1e1e}
.stat-label{font-size:10px;letter-spacing:0.08em;text-transform:uppercase;color:#555;margin-bottom:4px}
.stat-value{font-size:14px;font-weight:600;color:#d0d0d0}
.history-section{border-top:1px solid #1e1e1e;margin-top:20px;padding-top:18px}
.history-header{display:flex;align-items:flex-start;justify-content:space-between;margin-bottom:14px}
.history-total{font-size:18px;font-weight:700;color:#f0f0f0;margin-top:4px;letter-spacing:-.02em}
.history-legend{display:flex;align-items:center;gap:5px;color:#666;font-size:9px;letter-spacing:.06em;text-transform:uppercase;padding-top:2px}
.legend-dot{display:inline-block;width:7px;height:7px;border-radius:2px;margin-left:4px}.legend-dot.download{background:#3b82f6}.legend-dot.upload{background:#263f68}
.history-chart{display:grid;grid-template-columns:repeat(7,1fr);gap:7px;height:132px;align-items:end}
.history-column{height:100%%;min-width:0;display:grid;grid-template-rows:18px 1fr 16px;gap:4px;text-align:center}
.history-value{font-size:8px;color:#606060;white-space:nowrap;overflow:hidden;text-overflow:clip;font-variant-numeric:tabular-nums}
.history-bar{height:90px;width:100%%;max-width:30px;margin:auto;display:flex;flex-direction:column-reverse;justify-content:flex-start;overflow:hidden;border-radius:5px 5px 3px 3px;background:#1b1b1b}
.history-bar span{display:block;width:100%%;min-height:0;transition:height .35s ease}.history-down{background:#3b82f6}.history-up{background:#263f68}
.history-day{font-size:9px;color:#666;text-transform:uppercase;letter-spacing:.04em}
.history-note{font-size:9px;color:#444;text-align:right;margin-top:7px}
.reset-button{width:100%%;margin-top:18px;border:1px solid #2a2a2a;border-radius:10px;background:#1a1a1a;color:#e5e5e5;font-size:12px;font-weight:700;letter-spacing:.06em;text-transform:uppercase;padding:10px 12px;cursor:pointer}
.reset-button:hover{background:#232323;border-color:#3a3a3a}
.form-section{border-top:1px solid #1e1e1e;margin-top:20px;padding-top:20px}
.form-section-header{display:flex;align-items:center;justify-content:space-between;margin-bottom:14px}
.form-section-title{font-size:10px;letter-spacing:0.08em;text-transform:uppercase;color:#555}
.subscription-section{display:flex;flex-direction:column;align-items:center;gap:12px}
.qr-code{width:160px;height:160px;border-radius:12px;background:#fff;padding:10px}
.subscription-hint{font-size:11px;color:#555;text-align:center}
.copy-button{width:100%%;border-radius:10px;border:1px solid #2a2a2a;background:#1a1a1a;color:#e5e5e5;font-size:12px;font-weight:700;letter-spacing:.06em;text-transform:uppercase;padding:10px 12px;cursor:pointer}
.copy-button:hover{background:#232323;border-color:#3a3a3a}
.copy-button.copied{background:#0f2a1a;border-color:#1a4a2a;color:#4ade80}
.delete-outbound-button{background:none;border:none;font-size:10px;font-weight:700;letter-spacing:0.06em;text-transform:uppercase;color:#555;cursor:pointer;padding:0}
.delete-outbound-button:hover{color:#ef4444}
.field-row{display:flex;gap:10px;margin-bottom:10px}
.field{display:flex;flex-direction:column;gap:5px}
.field-grow{flex:1}
.field-port{width:90px}
.field-label{font-size:10px;letter-spacing:0.08em;text-transform:uppercase;color:#555}
.field-input{background:#0f0f0f;border:1px solid #2a2a2a;border-radius:8px;color:#e2e2e2;font-size:13px;padding:8px 10px;width:100%%;outline:none;font-family:inherit}
.field-input:focus{border-color:#3b82f6}
.field-input::placeholder{color:#444}
.form-actions{display:flex;gap:8px;margin-top:4px}
.action-button{flex:1;border-radius:10px;font-size:12px;font-weight:700;letter-spacing:.06em;text-transform:uppercase;padding:10px 12px;cursor:pointer;border:1px solid transparent}
.action-button-primary{background:#1d3461;border-color:#2a4a8a;color:#93c5fd}
.action-button-primary:hover{background:#243d75;border-color:#3b5fa0}
.action-button-secondary{background:#1a1a1a;border-color:#2a2a2a;color:#e5e5e5}
.action-button-secondary:hover{background:#232323;border-color:#3a3a3a}
.action-button-danger{background:#1a1a1a;border-color:#3f1f1f;color:#ef4444;font-size:12px;font-weight:700;letter-spacing:.06em;text-transform:uppercase;padding:10px 12px;cursor:pointer;border-radius:10px}
.action-button-danger:hover{background:#2a1010;border-color:#5a2a2a}
.test-result{margin-top:10px;padding:8px 12px;border-radius:8px;font-size:12px;font-weight:600;letter-spacing:.04em}
.test-ok{background:#0f2a1a;border:1px solid #1a4a2a;color:#4ade80}
.test-err{background:#2a0f0f;border:1px solid #4a1a1a;color:#f87171}
.checkbox-row{display:flex;gap:20px;margin:5px 0 10px;align-items:center}
.checkbox-container{display:flex;align-items:center;position:relative;cursor:pointer;font-size:12px;user-select:none;color:#d0d0d0}
.checkbox-container input{position:absolute;opacity:0;cursor:pointer;height:0;width:0}
.checkmark{height:16px;width:16px;background:#0f0f0f;border:1px solid #2a2a2a;border-radius:4px;margin-right:8px;position:relative;transition:all 0.2s ease}
.checkbox-container:hover input ~ .checkmark{border-color:#3b82f6;background:#151515}
.checkbox-container input:checked ~ .checkmark{background:#1d3461;border-color:#3b82f6}
.checkmark:after{content:"";position:absolute;display:none}
.checkbox-container input:checked ~ .checkmark:after{display:block}
.checkbox-container .checkmark:after{left:5px;top:2px;width:4px;height:8px;border:solid #93c5fd;border-width:0 2px 2px 0;transform:rotate(45deg)}
.checkbox-label{font-size:11px;font-weight:600;letter-spacing:0.04em;text-transform:uppercase;color:#d0d0d0}

.quota-overview,.card-controls,.field{min-width:0}
.tag-row{gap:16px}
.tag-name{overflow-wrap:anywhere}
.badge{flex-shrink:0}
.usage-label,.stat-value,.history-total{font-variant-numeric:tabular-nums}
.stat-label,.form-section-title,.field-label,.subscription-hint,.delete-outbound-button{color:#9299a5}
.history-legend,.history-day,.history-value,.history-note,.usage-label .muted{color:#9299a5}
.field-input::placeholder{color:#7e8590}
.field-input{min-height:42px}
button:focus-visible,.field-input:focus-visible{outline:2px solid #78aaff;outline-offset:3px}
.checkbox-container input:focus-visible ~ .checkmark{outline:2px solid #78aaff;outline-offset:3px}
.card-controls{margin-top:24px}
.card-controls>.form-section:first-child{margin-top:0}
.subscription-section{margin-top:16px}
.history-chart{height:180px}
.history-bar{height:138px;max-width:42px}
.history-value,.history-day,.history-note{font-size:10px}
@media(min-width:860px){
  .card:has(.card-controls){display:grid;grid-template-columns:minmax(0,1.35fr) minmax(0,1fr);padding:0}
  .card:has(.card-controls) .quota-overview{padding:36px}
  .card-controls{margin:0;padding:32px;border-left:1px solid #282828;background:#171717;border-radius:0 16px 16px 0}
  .card-controls>.form-section:first-child{border-top:0;padding-top:0}
  .tag-name{font-size:18px}
  .usage-label{font-size:clamp(26px,2.6vw,36px);margin-top:22px}
  .stat-value{font-size:18px}
  .stats{padding-top:24px}
  .history-section{margin-top:32px;padding-top:24px}
  .history-total{font-size:26px}
  .history-chart{height:240px;margin-top:24px}
  .history-bar{height:198px}
  .subscription-section{display:grid;grid-template-columns:120px minmax(0,1fr);gap:12px 20px}
  .qr-code{width:120px;height:120px;grid-row:1 / 3}
  .subscription-hint{text-align:left;align-self:end;line-height:1.6}
  .copy-button{align-self:start}
  .form-section{margin-top:28px;padding-top:24px}
  .reset-button{width:auto;min-width:140px;margin-top:26px}
}
@media(max-width:480px){
  body{padding:20px 12px}
  .card{padding:20px}
  .usage-label{font-size:clamp(18px,5.5vw,24px)}
  .stat-value{font-size:13px}
  .field-port{width:76px;flex-shrink:0}
  .history-chart{gap:4px}
}
@media(prefers-reduced-motion:reduce){
  *,*::before,*::after{transition:none!important}
}
.page-header{display:flex;align-items:center;justify-content:space-between;gap:16px;width:100%%;max-width:1120px;margin-bottom:24px}
.page-header .page-title{width:auto;margin:0}
.language-switch{display:flex;gap:4px;border:1px solid #2a2a2a;padding:3px;border-radius:9px;flex-shrink:0}
.language-switch button{border:0;background:transparent;color:#9299a5;padding:6px 10px;border-radius:6px;cursor:pointer;font:inherit;font-size:12px}
.language-switch button[aria-pressed="true"]{background:#1d3461;color:#b9d6ff}

:root{color-scheme:dark}
:root[data-theme="light"]{color-scheme:light}
.page-header{flex-wrap:wrap}
.page-preferences{display:flex;gap:12px;flex-wrap:wrap}
.theme-switch{display:flex;gap:4px;border:1px solid #2a2a2a;padding:3px;border-radius:9px}
.theme-switch button{border:0;background:transparent;color:#9299a5;padding:6px 10px;border-radius:6px;cursor:pointer;font:inherit;font-size:12px}
.theme-switch button[aria-pressed="true"]{background:#1d3461;color:#b9d6ff}
@media(max-width:480px){.page-preferences{gap:8px}.theme-switch button,.language-switch button{padding:6px 8px}}
.page-preferences :is(.theme-switch,.language-switch) button{display:inline-flex;align-items:center;justify-content:center;width:36px;height:36px;padding:0}
.page-preferences svg{width:18px;height:18px;pointer-events:none}
.page-preferences button:hover{background:#252d3b;color:#d6e5ff}
.quota-group{min-width:0}
.quota-group-title{font-size:14px;font-weight:600;letter-spacing:.02em;color:#9299a5;margin-bottom:16px}
.quota-group+.quota-group{margin-top:24px;padding-top:28px;border-top:1px solid #282828}
.other-user-cards{display:grid;gap:16px;align-items:start}
@media(min-width:1000px){.other-user-cards{grid-template-columns:repeat(2,minmax(0,1fr))}.other-user-cards .usage-label{font-size:28px}}
:root[data-theme="light"] body{background:#ebe5d8;color:#4d442f}
:root[data-theme="light"] .card{background:#f5f0e6;border-color:#d9d1bf}
:root[data-theme="light"] .card-controls{background:#efe9dc;border-color:#d9d1bf}
:root[data-theme="light"] :is(.tag-name,.usage-label,.history-total){color:#2b2418}
:root[data-theme="light"] :is(.stat-value,.checkbox-container,.checkbox-label){color:#4d442f}
:root[data-theme="light"] :is(.page-title,.stat-label,.form-section-title,.field-label,.subscription-hint,.delete-outbound-button,.history-legend,.history-day,.history-value,.history-note,.usage-label .muted,.quota-group-title){color:#766c55}
:root[data-theme="light"] :is(.stats,.history-section,.form-section){border-color:#d9d1bf}
:root[data-theme="light"] :is(.bar-track,.stat-divider){background:#e0d9c8}
:root[data-theme="light"] .history-bar{background:#e0d9c8}
:root[data-theme="light"] :is(.history-down,.legend-dot.download){background:#a85410}
:root[data-theme="light"] :is(.history-up,.legend-dot.upload){background:#dfb98c}
:root[data-theme="light"] .bar-fill{background:#a85410!important}
:root[data-theme="light"] .quota-overview:has(.badge[data-i18n="BLOCKED"]) .bar-fill{background:#a1302a!important}
:root[data-theme="light"] :is(.field-input,.checkmark){background:#f5f0e6;border-color:#d9d1bf;color:#4d442f}
:root[data-theme="light"] .field-input::placeholder{color:#766c55}
:root[data-theme="light"] .field-input:focus{border-color:#a85410}
:root[data-theme="light"] :is(.reset-button,.copy-button,.action-button-secondary){background:#efe9dc;border-color:#d9d1bf;color:#4d442f}
:root[data-theme="light"] :is(.reset-button,.copy-button,.action-button-secondary):hover{background:#e5e0d3;border-color:#bfb8a8}
:root[data-theme="light"] .action-button-primary{background:#a85410;border-color:#a85410;color:#fff}
:root[data-theme="light"] .action-button-primary:hover{background:#8f470e}
:root[data-theme="light"] :is(.language-switch,.theme-switch){border-color:#d9d1bf;background:#f5f0e6}
:root[data-theme="light"] :is(.language-switch,.theme-switch) button{color:#766c55}
:root[data-theme="light"] :is(.language-switch,.theme-switch) button[aria-pressed="true"]{background:#f2dfc6;color:#86420c}
:root[data-theme="light"] .page-preferences button:hover{background:#e8d6be;color:#86420c}
:root[data-theme="light"] .badge[data-i18n="ACTIVE"]{background:#f2dfc6!important;color:#86420c!important}
:root[data-theme="light"] .badge[data-i18n="BLOCKED"]{background:#f1d9d3!important;color:#a1302a!important}
:root[data-theme="light"] .pct-label{color:#86420c!important}
:root[data-theme="light"] .quota-overview:has(.badge[data-i18n="BLOCKED"]) .pct-label{color:#a1302a!important}
:root[data-theme="light"] :is(.test-ok,.copy-button.copied){background:#dfebd5;border-color:#aec3a1;color:#3a6529}
:root[data-theme="light"] :is(.test-err,.action-button-danger){background:#f1d9d3;border-color:#d9a6a0;color:#a1302a}
:root[data-theme="light"] .delete-outbound-button:hover{color:#a1302a}
:root[data-theme="light"] .checkbox-container:hover input ~ .checkmark{background:#e8d6be;border-color:#a85410}
:root[data-theme="light"] .checkbox-container input:checked ~ .checkmark{background:#a85410;border-color:#a85410}
:root[data-theme="light"] .quota-group+.quota-group{border-color:#d9d1bf}
</style>
</head>
<body>
<header class="page-header"><div class="page-title" data-i18n="Traffic Quota">Traffic Quota</div><div class="page-preferences"><div class="theme-switch" role="group" aria-label="Appearance"><button type="button" data-theme-mode="auto" aria-label="Auto" title="Auto" onclick="setQuotaTheme('auto')"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8m-4-4v4"/></svg></button><button type="button" data-theme-mode="light" aria-label="Light" title="Light" onclick="setQuotaTheme('light')"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5"/></svg></button><button type="button" data-theme-mode="dark" aria-label="Dark" title="Dark" onclick="setQuotaTheme('dark')"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d="M20.5 13.2A8.5 8.5 0 0 1 10.8 3.5 8.5 8.5 0 1 0 20.5 13.2Z"/></svg></button></div><div class="language-switch"><button type="button" class="language-toggle" aria-label="Switch to Chinese" title="Switch to Chinese" onclick="setQuotaLanguage(quotaLanguage==='en'?'zh':'en')"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d="M3 5h12M9 3v2m4 0c-1 5-4 8-9 10m2-7c1 3 4 5 7 6m1 7 4-10 4 10m-6.5-4h5"/></svg></button></div></div></header>
<main class="cards">%s</main>
%s
</body>
</html>`, html.EscapeString(title), portalThemeScript, content, portalLanguageScript)
}

func compactBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%dB", b)
	}
	units := []string{"K", "M", "G", "T", "P"}
	value := float64(b)
	unit := "B"
	for _, next := range units {
		value /= 1024
		unit = next
		if value < 1024 {
			break
		}
	}
	if value >= 100 {
		return fmt.Sprintf("%.0f%s", value, unit)
	}
	if value >= 10 {
		return fmt.Sprintf("%.1f%s", value, unit)
	}
	return fmt.Sprintf("%.2f%s", value, unit)
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

const portalLanguageScript = `
<script>
var quotaTranslations={"Traffic Quota": "流量配额", "Remaining": "剩余流量", "Upload": "上传", "Download": "下载", "Last 7 days": "最近 7 天", "Down": "下载", "Up": "上传", "Daily totals · UTC · updates every minute": "每日汇总 · UTC · 每分钟更新", "Reset quota": "重置配额", "Subscription": "订阅", "Scan with your client, or copy the link": "使用客户端扫码，或复制订阅链接", "Copy link": "复制链接", "Copied": "已复制", "Custom Outbound": "自定义出站", "Remove": "移除", "Server": "服务器", "Port": "端口", "Method": "加密方式", "Password": "密码", "Mux": "多路复用", "Padding": "填充", "Test": "测试", "Save": "保存", "Testing…": "测试中…", "Request failed": "请求失败", "Unknown error": "未知错误", "ACTIVE": "正常", "BLOCKED": "已停用", "of": "/", "used": "已使用", "Mon": "周一", "Tue": "周二", "Wed": "周三", "Thu": "周四", "Fri": "周五", "Sat": "周六", "Sun": "周日", "Forbidden": "无权访问", "Bad request": "请求无效", "Not found": "未找到", "User not found": "未找到用户", "Missing inbound tag or user name": "缺少入站标签或用户名", "No quota configured.": "尚未配置配额。", "No quota data available.": "暂无配额数据。", "Invalid Shadowsocks outbound": "Shadowsocks 出站配置无效", "Failed to save": "保存失败", "Delete custom outbound failed": "移除自定义出站失败", "Connection settings": "连接设置", "Traffic usage": "流量使用情况", "Daily traffic usage for the last seven days": "最近七天的每日流量使用情况", "ss:// QR code": "ss:// 订阅二维码", "Saved with warning": "已保存，但有警告", "Quota": "配额"};
quotaTranslations.savedWarning='出站已保存并启用，但连接测试失败：';
quotaTranslations.warningHelp='如果服务器可以正常使用，可忽略此提示；否则请使用“测试”重新检查。';
quotaTranslations.Back='返回';
quotaTranslations['Your node']='我的节点';quotaTranslations['Other users']='其他用户';
quotaTranslations.Auto='自动';quotaTranslations.Light='白天';quotaTranslations.Dark='黑夜';quotaTranslations.Appearance='外观';
var quotaLanguage='en';
var quotaEnglish={savedWarning:'Outbound saved and activated, but the connectivity check failed:',warningHelp:'If the server works for you, ignore this. Otherwise use Test to re-check.'};
function quotaText(key){return quotaLanguage==='zh'?(quotaTranslations[key]||key):(quotaEnglish[key]||key);}
function setQuotaText(element,key){element.setAttribute('data-i18n',key);element.textContent=quotaText(key);}
var quotaOriginalTitle=document.title;
document.querySelectorAll('.badge,.history-day').forEach(function(el){el.setAttribute('data-i18n',el.textContent.trim());});
document.querySelectorAll('[aria-label],[alt],[title]').forEach(function(el){
  ['aria-label','alt','title'].forEach(function(attr){var key=el.getAttribute(attr);if(quotaTranslations[key])el.setAttribute('data-i18n-'+attr,key);});
});
document.querySelectorAll('.history-column[title]').forEach(function(el){el.dataset.originalTitle=el.title;});
function setQuotaLanguage(language){
  quotaLanguage=language==='zh'?'zh':'en';
  document.documentElement.lang=quotaLanguage==='zh'?'zh-CN':'en';
  document.querySelectorAll('[data-i18n]').forEach(function(el){el.textContent=quotaText(el.getAttribute('data-i18n'));});
  ['aria-label','alt','title'].forEach(function(attr){
    document.querySelectorAll('[data-i18n-'+attr+']').forEach(function(el){el.setAttribute(attr,quotaText(el.getAttribute('data-i18n-'+attr)));});
  });
  document.querySelectorAll('.history-column[title]').forEach(function(el){el.title=el.dataset.originalTitle.replace(/Mon|Tue|Wed|Thu|Fri|Sat|Sun|Upload|Download/g,quotaText);});
  var languageButton=document.querySelector('.language-toggle');
  var languageHint=quotaLanguage==='zh'?'切换至英文 / English':'Switch to Chinese / 中文';
  languageButton.setAttribute('aria-label',languageHint);
  languageButton.title=languageHint;
  var title=quotaOriginalTitle.slice(0,-8);
  document.title=quotaText(title)+' — '+quotaText('Quota');
  try{localStorage.setItem('quota-language',quotaLanguage);}catch(e){}
}
var initialLanguage;
try{initialLanguage=localStorage.getItem('quota-language');}catch(e){}
setQuotaLanguage(initialLanguage==='en'||initialLanguage==='zh'?initialLanguage:((navigator.language||'').toLowerCase().startsWith('zh')?'zh':'en'));
</script>
`

const portalThemeScript = `
<script>
var quotaSystemTheme=window.matchMedia('(prefers-color-scheme: dark)');
var quotaThemeMode='auto';
try{var savedTheme=localStorage.getItem('quota-theme');if(savedTheme==='light'||savedTheme==='dark')quotaThemeMode=savedTheme;}catch(e){}
function applyQuotaTheme(){
  document.documentElement.dataset.theme=quotaThemeMode==='auto'?(quotaSystemTheme.matches?'dark':'light'):quotaThemeMode;
  document.querySelectorAll('[data-theme-mode]').forEach(function(btn){btn.setAttribute('aria-pressed',String(btn.dataset.themeMode===quotaThemeMode));});
}
function setQuotaTheme(mode){
  quotaThemeMode=mode==='light'||mode==='dark'?mode:'auto';
  try{localStorage.setItem('quota-theme',quotaThemeMode);}catch(e){}
  applyQuotaTheme();
}
applyQuotaTheme();
quotaSystemTheme.addEventListener('change',function(){if(quotaThemeMode==='auto')applyQuotaTheme();});
document.addEventListener('DOMContentLoaded',applyQuotaTheme);
</script>
`
