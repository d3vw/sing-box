package quota

import (
	"bufio"
	"context"
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
		Adapter:                       boxOutbound.NewAdapter(C.TypeQuotaPortal, tag, []string{N.NetworkTCP}, nil),
		ctx:                           ctx,
		router:                        router,
		logger:                        logger,
		manager:                       manager,
		memberOutboundConfigDirectory: options.MemberOutboundConfigDirectory,
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
	return nil, fmt.Errorf("quota-portal does not support UDP")
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
	if req.Method == http.MethodPost && req.URL.Path == "/quota/rate-limit" {
		h.handleSaveRateLimit(conn, req, isAdmin)
		return
	}
	if req.Method == http.MethodPost && req.URL.Path == "/outbound/delete" {
		h.handleDeleteMemberOutbound(conn, inboundTag, userName, isAdmin)
		return
	}

	if inboundTag == "" {
		h.writeHTML(conn, http.StatusForbidden, buildPage("Forbidden", `<div class="card">Forbidden</div>`), nil)
		return
	}
	var body string
	if isAdmin {
		body = h.renderAll(true)
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
		body = buildPage(inboundTag+" / "+userName, renderSnapshot(snapshot, false, formData))
	} else {
		body = buildPage(inboundTag+" / "+userName, `<div class="card">No quota configured.</div>`)
	}
	h.writeHTML(conn, http.StatusOK, body, nil)
}

func (h *portalOutbound) handleReset(conn net.Conn, req *http.Request, isAdmin bool) {
	if !isAdmin {
		h.writeHTML(conn, http.StatusForbidden, buildPage("Forbidden", `<div class="card">Forbidden</div>`), nil)
		return
	}
	if err := req.ParseForm(); err != nil {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", `<div class="card">Bad request</div>`), nil)
		return
	}
	tag := req.Form.Get("tag")
	name := req.Form.Get("name")
	if tag == "" || name == "" {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", `<div class="card">Missing inbound tag or user name</div>`), nil)
		return
	}
	if !h.manager.Reset(tag, name) {
		h.writeHTML(conn, http.StatusNotFound, buildPage("Not found", `<div class="card">User not found</div>`), nil)
		return
	}
	h.writeHTML(conn, http.StatusSeeOther, "", map[string]string{"Location": "/"})
}

func (h *portalOutbound) handleSaveRateLimit(conn net.Conn, req *http.Request, isAdmin bool) {
	if !isAdmin {
		h.writeHTML(conn, http.StatusForbidden, buildPage("Forbidden", `<div class="card">Forbidden</div>`), nil)
		return
	}
	if err := req.ParseForm(); err != nil {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", `<div class="card">Bad request</div>`), nil)
		return
	}
	tag := req.Form.Get("tag")
	name := req.Form.Get("name")
	rateLimitReadStr := req.Form.Get("rate_limit_read")
	rateLimitWriteStr := req.Form.Get("rate_limit_write")

	rateLimitRead, err := option.ParseRateLimit(rateLimitReadStr)
	if err != nil {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Invalid rate limit format", `<div class="card">Invalid upload rate limit format</div>`), nil)
		return
	}
	rateLimitWrite, err := option.ParseRateLimit(rateLimitWriteStr)
	if err != nil {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Invalid rate limit format", `<div class="card">Invalid download rate limit format</div>`), nil)
		return
	}

	success := h.manager.SetUserRateLimit(tag, name, rateLimitRead, rateLimitWrite)
	if !success {
		h.writeHTML(conn, http.StatusNotFound, buildPage("Not found", `<div class="card">User not found</div>`), nil)
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
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", `<div class="card">Bad request</div>`), nil)
		return shadowsocksOutboundForm{}, false
	}
	server := strings.TrimSpace(req.Form.Get("server"))
	method := strings.TrimSpace(req.Form.Get("method"))
	password := req.Form.Get("password")
	port, err := strconv.Atoi(strings.TrimSpace(req.Form.Get("server_port")))
	if err != nil || server == "" || method == "" || password == "" || port < 1 || port > 65535 {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", `<div class="card">Invalid Shadowsocks outbound</div>`), nil)
		return shadowsocksOutboundForm{}, false
	}
	mux := req.Form.Get("mux") == "true" || req.Form.Get("mux") == "on"
	padding := req.Form.Get("padding") == "true" || req.Form.Get("padding") == "on"
	return shadowsocksOutboundForm{Server: server, ServerPort: port, Method: method, Password: password, Mux: mux, Padding: padding}, true
}

func (h *portalOutbound) handleSaveShadowsocksOutbound(conn net.Conn, req *http.Request, inboundTag, userName string, isAdmin bool) {
	if isAdmin || h.memberOutboundConfigDirectory == "" {
		h.writeHTML(conn, http.StatusForbidden, buildPage("Forbidden", `<div class="card">Forbidden</div>`), nil)
		return
	}
	form, ok := h.parseShadowsocksOutboundForm(conn, req)
	if !ok {
		return
	}

	// Validate SS params by creating the live outbound
	newOutbound, err := h.createLiveSSOutbound(inboundTag, userName, form.Server, form.ServerPort, form.Method, form.Password, form.Mux, form.Padding)
	if err != nil {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", fmt.Sprintf(`<div class="card">Invalid Shadowsocks outbound: %s</div>`, html.EscapeString(err.Error()))), nil)
		return
	}

	// Run connectivity test
	_, err = h.testMemberShadowsocksOutbound(inboundTag, form.Server, form.ServerPort, form.Method, form.Password, form.Mux, form.Padding)
	if err != nil {
		if closer, ok := newOutbound.(io.Closer); ok {
			_ = closer.Close()
		}
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", fmt.Sprintf(`<div class="card">Invalid Shadowsocks outbound: %s</div>`, html.EscapeString(err.Error()))), nil)
		return
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
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", fmt.Sprintf(`<div class="card">Failed to save: %s</div>`, html.EscapeString(err.Error()))), nil)
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

	h.writeHTML(conn, http.StatusSeeOther, "", map[string]string{"Location": "/"})

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
	if isAdmin || h.memberOutboundConfigDirectory == "" {
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
	if isAdmin || h.memberOutboundConfigDirectory == "" {
		h.writeHTML(conn, http.StatusForbidden, buildPage("Forbidden", `<div class="card">Forbidden</div>`), nil)
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
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", fmt.Sprintf(`<div class="card">Delete custom outbound failed: %s</div>`, html.EscapeString(err.Error()))), nil)
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
	return urltest.URLTest(testCtx, "", outbound)
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

func (h *portalOutbound) renderAll(admin bool) string {
	snapshots := h.manager.Snapshots()
	var sb strings.Builder
	for _, s := range snapshots {
		sb.WriteString(renderSnapshot(s, admin, nil))
	}
	if sb.Len() == 0 {
		return buildPage("No quota data available.", "")
	}
	return buildPage("Traffic Quota", sb.String())
}

func renderSnapshot(s UserSnapshot, admin bool, existing *shadowsocksOutboundForm) string {
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
    <button type="submit" class="reset-button">Reset quota</button>
  </form>
  <div class="form-section">
    <div class="form-section-title" style="margin-bottom:8px">Rate Limiting</div>
    <form method="post" action="/quota/rate-limit">
      <input type="hidden" name="tag" value="%s">
      <input type="hidden" name="name" value="%s">
      <div class="field-row">
        <div class="field field-grow">
          <label class="field-label">Upload Limit (Read)</label>
          <input class="field-input" name="rate_limit_read" placeholder="Unlimited (e.g. 100Mbps)" autocomplete="off" value="%s">
        </div>
        <div class="field field-grow">
          <label class="field-label">Download Limit (Write)</label>
          <input class="field-input" name="rate_limit_write" placeholder="Unlimited (e.g. 10MB/s)" autocomplete="off" value="%s">
        </div>
      </div>
      <button type="submit" class="reset-button" style="margin-top:8px;background:#3b82f6;border-color:#2a4a8a;color:#fff">Save limits</button>
    </form>
  </div>`,
			html.EscapeString(s.InboundTag), html.EscapeString(s.UserName),
			html.EscapeString(s.InboundTag), html.EscapeString(s.UserName),
			html.EscapeString(formatRateLimit(s.RateLimitRead)),
			html.EscapeString(formatRateLimit(s.RateLimitWrite)),
		)
	}
	extraControls := resetControl
	if existing != nil {
		extraControls += renderShadowsocksOutboundForm(existing)
	}
	displayName := html.EscapeString(s.InboundTag + " / " + s.UserName)
	return fmt.Sprintf(`<div class="card">
  <div class="card-header">
    <div class="tag-row">
      <span class="tag-name">%s</span>
      <span class="badge" style="background:%s;color:%s">%s</span>
    </div>
    <div class="usage-label">%s <span class="muted">of</span> %s</div>
  </div>
  <div class="bar-track"><div class="bar-fill" style="width:%.1f%%;background:%s"></div></div>
  <div class="pct-label" style="color:%s">%.1f%% used</div>
  <div class="stats">
    <div class="stat-item">
      <div class="stat-label">Remaining</div>
      <div class="stat-value">%s</div>
    </div>
    <div class="stat-divider"></div>
    <div class="stat-item">
      <div class="stat-label">Upload</div>
      <div class="stat-value">%s</div>
    </div>
    <div class="stat-divider"></div>
    <div class="stat-item">
      <div class="stat-label">Download</div>
      <div class="stat-value">%s</div>
    </div>
  </div>
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
		extraControls,
	)
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
      <button type="submit" class="delete-outbound-button">Remove</button>
    </form>`
	}
	return fmt.Sprintf(`<div class="form-section">
    <div class="form-section-header">
      <div class="form-section-title">Custom Outbound</div>
      %s
    </div>
    <form method="post" action="/outbound/shadowsocks">
      <div class="field-row">
        <div class="field field-grow">
          <label class="field-label">Server</label>
          <input class="field-input" name="server" placeholder="example.com" autocomplete="off" value="%s" required>
        </div>
        <div class="field field-port">
          <label class="field-label">Port</label>
          <input class="field-input" name="server_port" placeholder="443" inputmode="numeric" value="%s" required>
        </div>
      </div>
      <div class="field-row">
        <div class="field field-grow">
          <label class="field-label">Method</label>
          <input class="field-input" name="method" placeholder="aes-256-gcm" autocomplete="off" value="%s" required>
        </div>
      </div>
      <div class="field-row">
        <div class="field field-grow">
          <label class="field-label">Password</label>
          <input class="field-input" name="password" placeholder="••••••••" type="password" value="%s" required>
        </div>
      </div>
      <div class="field-row checkbox-row">
        <label class="checkbox-container">
          <input type="checkbox" name="mux" value="true" %s>
          <span class="checkmark"></span>
          <span class="checkbox-label">Mux</span>
        </label>
        <label class="checkbox-container">
          <input type="checkbox" name="padding" value="true" %s>
          <span class="checkmark"></span>
          <span class="checkbox-label">Padding</span>
        </label>
      </div>
      <div class="form-actions">
        <button type="button" onclick="testOutbound(this)" class="action-button action-button-secondary">Test</button>
        <button type="submit" class="action-button action-button-primary">Save</button>
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
  btn.textContent='Testing…';
  var r=document.getElementById('test-result');
  r.className='test-result';r.style.display='none';
  fetch('/outbound/shadowsocks/test',{method:'POST',body:params,headers:{'Content-Type':'application/x-www-form-urlencoded'}})
    .then(function(res){return res.json().then(function(j){return{ok:res.ok,j:j}})})
    .then(function(d){
      r.style.display='block';
      if(d.ok&&d.j.delay!=null){r.className='test-result test-ok';r.textContent=d.j.delay+' ms';}
      else{r.className='test-result test-err';r.textContent=d.j.error||'Unknown error';}
    })
    .catch(function(e){r.style.display='block';r.className='test-result test-err';r.textContent='Request failed';})
    .finally(function(){btn.disabled=false;btn.textContent='Test';});
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
  justify-content:center;
  padding:24px;
}
.page-title{
  font-size:11px;
  letter-spacing:0.12em;
  text-transform:uppercase;
  color:#555;
  margin-bottom:24px;
}
.cards{display:flex;flex-direction:column;gap:16px;width:100%%;max-width:420px}
.card{
  background:#141414;
  border:1px solid #222;
  border-radius:16px;
  padding:24px;
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
.reset-button{width:100%%;margin-top:18px;border:1px solid #2a2a2a;border-radius:10px;background:#1a1a1a;color:#e5e5e5;font-size:12px;font-weight:700;letter-spacing:.06em;text-transform:uppercase;padding:10px 12px;cursor:pointer}
.reset-button:hover{background:#232323;border-color:#3a3a3a}
.form-section{border-top:1px solid #1e1e1e;margin-top:20px;padding-top:20px}
.form-section-header{display:flex;align-items:center;justify-content:space-between;margin-bottom:14px}
.form-section-title{font-size:10px;letter-spacing:0.08em;text-transform:uppercase;color:#555}
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
</style>
</head>
<body>
<div class="page-title">Traffic Quota</div>
<div class="cards">%s</div>
</body>
</html>`, title, content)
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

func formatRateLimit(bytesPerSec int64) string {
	if bytesPerSec <= 0 {
		return ""
	}
	if bytesPerSec%125000 == 0 {
		return fmt.Sprintf("%dMbps", bytesPerSec/125000)
	}
	if bytesPerSec%125 == 0 && bytesPerSec < 125000 {
		return fmt.Sprintf("%dKbps", bytesPerSec/125)
	}
	if bytesPerSec >= 1024*1024 {
		mb := float64(bytesPerSec) / (1024 * 1024)
		if mb == float64(int64(mb)) {
			return fmt.Sprintf("%dMB/s", int64(mb))
		}
		return fmt.Sprintf("%.1fMB/s", mb)
	}
	if bytesPerSec >= 1024 {
		kb := float64(bytesPerSec) / 1024
		if kb == float64(int64(kb)) {
			return fmt.Sprintf("%dKB/s", int64(kb))
		}
		return fmt.Sprintf("%.1fKB/s", kb)
	}
	return fmt.Sprintf("%dB/s", bytesPerSec)
}
