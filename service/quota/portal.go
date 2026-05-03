package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

type memberOutboundConnectivityTester func(ctx context.Context, inboundTag, server string, serverPort int, method, password string) (uint16, error)

type portalOutbound struct {
	boxOutbound.Adapter
	ctx                              context.Context
	router                           adapter.Router
	logger                           log.ContextLogger
	manager                          *Manager
	memberOutboundConfigDirectory    string
	memberOutboundConfigChecker      func(directory string) error
	memberOutboundConnectivityTester memberOutboundConnectivityTester
}

func newPortalOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.QuotaPortalOutboundOptions) (adapter.Outbound, error) {
	manager := service.FromContext[*Manager](ctx)
	if manager == nil {
		return nil, fmt.Errorf("quota-portal outbound requires a quota service to be configured")
	}
	return &portalOutbound{
		Adapter:                          boxOutbound.NewAdapter(C.TypeQuotaPortal, tag, []string{N.NetworkTCP}, nil),
		ctx:                              ctx,
		router:                           router,
		logger:                           logger,
		manager:                          manager,
		memberOutboundConfigDirectory:    options.MemberOutboundConfigDirectory,
		memberOutboundConfigChecker:      runSingBoxConfigCheck,
		memberOutboundConnectivityTester: nil,
	}, nil
}

func (h *portalOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	inboundTag := ""
	if metadata := adapter.ContextFrom(ctx); metadata != nil {
		inboundTag = metadata.Inbound
	}

	client, server := net.Pipe()
	go h.serveHTTP(server, inboundTag)
	return client, nil
}

func (h *portalOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, fmt.Errorf("quota-portal does not support UDP")
}

func (h *portalOutbound) serveHTTP(conn net.Conn, inboundTag string) {
	defer conn.Close()
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	isAdmin := inboundTag == "" || h.manager.state(inboundTag) == nil
	if req.Method == http.MethodPost && req.URL.Path == "/quota/reset" {
		h.handleReset(conn, req, isAdmin)
		return
	}
	if req.Method == http.MethodPost && req.URL.Path == "/outbound/shadowsocks" {
		h.handleSaveShadowsocksOutbound(conn, req, inboundTag, isAdmin)
		return
	}
	if req.Method == http.MethodPost && req.URL.Path == "/outbound/shadowsocks/test" {
		h.handleTestShadowsocksOutbound(conn, req, inboundTag, isAdmin)
		return
	}
	if req.Method == http.MethodPost && req.URL.Path == "/outbound/delete" {
		h.handleDeleteMemberOutbound(conn, inboundTag, isAdmin)
		return
	}

	var body string
	if isAdmin {
		body = h.renderAll(true)
	} else if snapshot, ok := h.manager.Snapshot(inboundTag); ok {
		var existing *shadowsocksOutboundForm
		if h.memberOutboundConfigDirectory != "" {
			existing = readMemberShadowsocksOutbound(h.memberOutboundConfigDirectory, inboundTag)
		}
		body = buildPage(inboundTag, renderSnapshot(snapshot, false, existing))
	} else {
		body = h.renderAll(true)
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
	if tag == "" {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", `<div class="card">Missing inbound tag</div>`), nil)
		return
	}
	if !h.manager.Reset(tag) {
		h.writeHTML(conn, http.StatusNotFound, buildPage("Not found", `<div class="card">Inbound not found</div>`), nil)
		return
	}
	h.writeHTML(conn, http.StatusSeeOther, "", map[string]string{"Location": "/"})
}

type shadowsocksOutboundForm struct {
	Server     string
	ServerPort int
	Method     string
	Password   string
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
	return shadowsocksOutboundForm{Server: server, ServerPort: port, Method: method, Password: password}, true
}

func (h *portalOutbound) handleSaveShadowsocksOutbound(conn net.Conn, req *http.Request, inboundTag string, isAdmin bool) {
	if isAdmin || h.memberOutboundConfigDirectory == "" || h.manager.state(inboundTag) == nil {
		h.writeHTML(conn, http.StatusForbidden, buildPage("Forbidden", `<div class="card">Forbidden</div>`), nil)
		return
	}
	form, ok := h.parseShadowsocksOutboundForm(conn, req)
	if !ok {
		return
	}
	_, err := h.writeMemberShadowsocksOutboundFragment(inboundTag, form.Server, form.ServerPort, form.Method, form.Password)
	if err != nil {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", fmt.Sprintf(`<div class="card">Invalid Shadowsocks outbound: %s</div>`, html.EscapeString(err.Error()))), nil)
		return
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGHUP)
	h.writeHTML(conn, http.StatusSeeOther, "", map[string]string{"Location": "/"})
}

func (h *portalOutbound) handleTestShadowsocksOutbound(conn net.Conn, req *http.Request, inboundTag string, isAdmin bool) {
	writeJSON := func(status int, body string) {
		resp := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
			status, http.StatusText(status), len(body), body)
		conn.Write([]byte(resp))
	}
	if isAdmin || h.memberOutboundConfigDirectory == "" || h.manager.state(inboundTag) == nil {
		writeJSON(http.StatusForbidden, `{"error":"forbidden"}`)
		return
	}
	form, ok := h.parseShadowsocksOutboundForm(conn, req)
	if !ok {
		return
	}
	checker := h.memberOutboundConfigChecker
	if checker == nil {
		checker = runSingBoxConfigCheck
	}
	if err := checkMemberShadowsocksOutboundFragment(h.memberOutboundConfigDirectory, inboundTag, form.Server, form.ServerPort, form.Method, form.Password, checker); err != nil {
		writeJSON(http.StatusBadRequest, fmt.Sprintf(`{"error":%s}`, jsonString(err.Error())))
		return
	}
	delay, err := h.testMemberShadowsocksOutbound(inboundTag, form.Server, form.ServerPort, form.Method, form.Password)
	if err != nil {
		writeJSON(http.StatusBadRequest, fmt.Sprintf(`{"error":%s}`, jsonString(err.Error())))
		return
	}
	writeJSON(http.StatusOK, fmt.Sprintf(`{"delay":%d}`, delay))
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (h *portalOutbound) handleDeleteMemberOutbound(conn net.Conn, inboundTag string, isAdmin bool) {
	if isAdmin || h.memberOutboundConfigDirectory == "" || h.manager.state(inboundTag) == nil {
		h.writeHTML(conn, http.StatusForbidden, buildPage("Forbidden", `<div class="card">Forbidden</div>`), nil)
		return
	}
	if err := h.deleteMemberOutboundFragment(inboundTag); err != nil {
		h.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", fmt.Sprintf(`<div class="card">Delete custom outbound failed: %s</div>`, html.EscapeString(err.Error()))), nil)
		return
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGHUP)
	h.writeHTML(conn, http.StatusSeeOther, "", map[string]string{"Location": "/"})
}

func (h *portalOutbound) writeMemberShadowsocksOutboundFragment(inboundTag, server string, serverPort int, method, password string) (uint16, error) {
	checker := h.memberOutboundConfigChecker
	if checker == nil {
		checker = runSingBoxConfigCheck
	}
	var delay uint16
	err := writeMemberShadowsocksOutboundFragment(h.memberOutboundConfigDirectory, inboundTag, server, serverPort, method, password, checker, func() error {
		var err error
		delay, err = h.testMemberShadowsocksOutbound(inboundTag, server, serverPort, method, password)
		return err
	})
	return delay, err
}

func (h *portalOutbound) testMemberShadowsocksOutbound(inboundTag, server string, serverPort int, method, password string) (uint16, error) {
	tester := h.memberOutboundConnectivityTester
	if tester == nil {
		tester = h.testShadowsocksOutboundConnectivity
	}
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return tester(ctx, inboundTag, server, serverPort, method, password)
}

func checkMemberShadowsocksOutboundFragment(directory, inboundTag, server string, serverPort int, method, password string, checker func(directory string) error) error {
	content, err := buildMemberShadowsocksOutboundFragment(inboundTag, server, serverPort, method, password)
	if err != nil {
		return err
	}
	return validateMemberOutboundFragmentCandidate(directory, memberOutboundFragmentName(inboundTag), content, checker, nil)
}

func writeMemberShadowsocksOutboundFragment(directory, inboundTag, server string, serverPort int, method, password string, checker func(directory string) error, preCommit func() error) error {
	content, err := buildMemberShadowsocksOutboundFragment(inboundTag, server, serverPort, method, password)
	if err != nil {
		return err
	}
	return validateAndWriteMemberOutboundFragment(directory, memberOutboundFragmentName(inboundTag), content, checker, preCommit)
}

func buildMemberShadowsocksOutboundFragment(inboundTag, server string, serverPort int, method, password string) ([]byte, error) {
	outboundTag := memberOutboundTag(inboundTag)
	fragment := struct {
		Outbounds []struct {
			Type       string `json:"type"`
			Tag        string `json:"tag"`
			Server     string `json:"server"`
			ServerPort int    `json:"server_port"`
			Method     string `json:"method"`
			Password   string `json:"password"`
		} `json:"outbounds"`
		Route struct {
			Rules []struct {
				Inbound  []string `json:"inbound"`
				Outbound string   `json:"outbound"`
			} `json:"rules"`
		} `json:"route"`
	}{}
	fragment.Outbounds = append(fragment.Outbounds, struct {
		Type       string `json:"type"`
		Tag        string `json:"tag"`
		Server     string `json:"server"`
		ServerPort int    `json:"server_port"`
		Method     string `json:"method"`
		Password   string `json:"password"`
	}{
		Type:       C.TypeShadowsocks,
		Tag:        outboundTag,
		Server:     server,
		ServerPort: serverPort,
		Method:     method,
		Password:   password,
	})
	fragment.Route.Rules = append(fragment.Route.Rules, struct {
		Inbound  []string `json:"inbound"`
		Outbound string   `json:"outbound"`
	}{
		Inbound:  []string{inboundTag},
		Outbound: outboundTag,
	})
	content, err := json.MarshalIndent(fragment, "", "  ")
	if err != nil {
		return nil, err
	}
	content = append(content, '\n')
	return content, nil
}

func (h *portalOutbound) testShadowsocksOutboundConnectivity(ctx context.Context, inboundTag, server string, serverPort int, method, password string) (uint16, error) {
	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	outbound, err := ssoutbound.NewOutbound(testCtx, h.router, h.logger, memberOutboundTag(inboundTag), option.ShadowsocksOutboundOptions{
		ServerOptions: option.ServerOptions{
			Server:     server,
			ServerPort: uint16(serverPort),
		},
		Method:   method,
		Password: password,
	})
	if err != nil {
		return 0, err
	}
	if closer, ok := outbound.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	return urltest.URLTest(testCtx, "", outbound)
}

func validateAndWriteMemberOutboundFragment(directory, fileName string, content []byte, checker func(directory string) error, preCommit func() error) error {
	if err := validateMemberOutboundFragmentCandidate(directory, fileName, content, checker, preCommit); err != nil {
		return err
	}
	finalPath := filepath.Join(directory, fileName)
	tempPath := finalPath + ".tmp"
	if err := os.WriteFile(tempPath, content, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func validateMemberOutboundFragmentCandidate(directory, fileName string, content []byte, checker func(directory string) error, preCommit func() error) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	validationDir, err := os.MkdirTemp("", "quota-check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(validationDir)
	if err := copyJSONConfigDirectory(directory, validationDir, fileName); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(validationDir, fileName), content, 0o600); err != nil {
		return err
	}
	if err := checker(validationDir); err != nil {
		return err
	}
	if preCommit != nil {
		if err := preCommit(); err != nil {
			return err
		}
	}
	return nil
}

func readMemberShadowsocksOutbound(directory, inboundTag string) *shadowsocksOutboundForm {
	path := filepath.Join(directory, memberOutboundFragmentName(inboundTag))
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
	return &shadowsocksOutboundForm{Server: ob.Server, ServerPort: ob.ServerPort, Method: ob.Method, Password: ob.Password}
}

func (h *portalOutbound) deleteMemberOutboundFragment(inboundTag string) error {
	checker := h.memberOutboundConfigChecker
	if checker == nil {
		checker = runSingBoxConfigCheck
	}
	return deleteMemberOutboundFragment(h.memberOutboundConfigDirectory, memberOutboundFragmentName(inboundTag), checker)
}

func deleteMemberOutboundFragment(directory, fileName string, checker func(directory string) error) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	validationDir, err := os.MkdirTemp("", "quota-check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(validationDir)
	if err := copyJSONConfigDirectory(directory, validationDir, fileName); err != nil {
		return err
	}
	entries, _ := os.ReadDir(validationDir)
	hasJSON := false
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			hasJSON = true
			break
		}
	}
	if hasJSON {
		if err := checker(validationDir); err != nil {
			return err
		}
	}
	finalPath := filepath.Join(directory, fileName)
	if err := os.Remove(finalPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func copyJSONConfigDirectory(sourceDir, destinationDir, replacementFileName string) error {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == replacementFileName {
			continue
		}
		content, err := os.ReadFile(filepath.Join(sourceDir, entry.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destinationDir, entry.Name()), content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func runSingBoxConfigCheck(directory string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	output, err := exec.Command(executable, "check", "-C", directory).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sing-box check failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func memberOutboundFragmentName(inboundTag string) string {
	return "40-quota-member-" + safeConfigName(inboundTag) + ".json"
}

func memberOutboundTag(inboundTag string) string {
	return "quota-" + safeConfigName(inboundTag) + "-custom-out"
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

func renderSnapshot(s InboundSnapshot, admin bool, existing *shadowsocksOutboundForm) string {
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
    <button type="submit" class="reset-button">Reset quota</button>
  </form>`, html.EscapeString(s.Tag))
	}
	extraControls := resetControl
	if existing != nil {
		extraControls += renderShadowsocksOutboundForm(existing)
	}
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
		html.EscapeString(s.Tag),
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
	if existing != nil {
		server = html.EscapeString(existing.Server)
		if existing.ServerPort > 0 {
			port = strconv.Itoa(existing.ServerPort)
		}
		method = html.EscapeString(existing.Method)
		password = html.EscapeString(existing.Password)
	}
	deleteButton := ""
	if existing != nil {
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
</script>`, deleteButton, server, port, method, password)
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
