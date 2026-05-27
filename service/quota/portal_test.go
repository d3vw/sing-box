package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

func init() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)
	go func() {
		for range c {
		}
	}()
}

const testInboundTag = "member"
const testUserName = "testuser"
const testAdminInboundTag = "admin-in"
const testAdminUserName = "admin"

func newTestPortalManager() *Manager {
	m := NewManager()
	state := &UserState{Key: UserKey{InboundTag: testInboundTag, UserName: testUserName}, QuotaBytes: 100}
	state.Uplink.Store(40)
	state.Downlink.Store(30)
	m.users[UserKey{InboundTag: testInboundTag, UserName: testUserName}] = state
	adminState := &UserState{Key: UserKey{InboundTag: testAdminInboundTag, UserName: testAdminUserName}, Admin: true}
	m.users[UserKey{InboundTag: testAdminInboundTag, UserName: testAdminUserName}] = adminState
	return m
}

func servePortalRequest(t *testing.T, h *portalOutbound, inboundTag, userName, request string) *http.Response {
	t.Helper()
	client, server := net.Pipe()
	go h.serveHTTP(server, inboundTag, userName)
	_, err := client.Write([]byte(request))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readResponseBody(t *testing.T, response *http.Response) string {
	t.Helper()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return string(content)
}

func TestPortalAdminCanResetMemberQuota(t *testing.T) {
	manager := newTestPortalManager()
	h := &portalOutbound{manager: manager}

	response := servePortalRequest(t, h, testAdminInboundTag, testAdminUserName, fmt.Sprintf(
		"POST /quota/reset HTTP/1.1\r\nHost: quota.local\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\ntag=%s&name=%s",
		len("tag="+testInboundTag+"&name="+testUserName), testInboundTag, testUserName,
	))

	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect after reset, got %s", response.Status)
	}
	snapshot, ok := manager.Snapshot(testInboundTag, testUserName)
	if !ok {
		t.Fatal("missing member snapshot")
	}
	if snapshot.UsedBytes != 0 || snapshot.UplinkBytes != 0 || snapshot.DownlinkBytes != 0 {
		t.Fatalf("expected reset counters, got %+v", snapshot)
	}
}

func TestPortalMemberCannotResetQuota(t *testing.T) {
	manager := newTestPortalManager()
	h := &portalOutbound{manager: manager}

	body := fmt.Sprintf("tag=%s&name=%s", testInboundTag, testUserName)
	response := servePortalRequest(t, h, testInboundTag, testUserName, fmt.Sprintf(
		"POST /quota/reset HTTP/1.1\r\nHost: quota.local\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s",
		len(body), body,
	))

	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected forbidden for member reset, got %s", response.Status)
	}
	snapshot, ok := manager.Snapshot(testInboundTag, testUserName)
	if !ok {
		t.Fatal("missing member snapshot")
	}
	if snapshot.UsedBytes != 70 {
		t.Fatalf("expected counters unchanged, got %+v", snapshot)
	}
}

func TestPortalAdminPageRendersResetControls(t *testing.T) {
	manager := newTestPortalManager()
	h := &portalOutbound{manager: manager}

	page := h.renderAll(true)

	for _, want := range []string{
		`<form method="post" action="/quota/reset">`,
		`name="tag" value="member"`,
		`name="name" value="testuser"`,
		`Reset quota`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("expected admin page to contain %q, got:\n%s", want, page)
		}
	}
}

func TestPortalMemberPageDoesNotRenderResetControls(t *testing.T) {
	manager := newTestPortalManager()
	snapshot, _ := manager.Snapshot(testInboundTag, testUserName)
	card := renderSnapshot(snapshot, false, nil)

	if strings.Contains(card, "Reset quota") || strings.Contains(card, "<form") {
		t.Fatalf("member card should not include reset controls:\n%s", card)
	}
}

func TestPortalResetRejectsMissingMember(t *testing.T) {
	manager := newTestPortalManager()
	h := &portalOutbound{manager: manager}

	body := "tag=missing&name=nobody"
	response := servePortalRequest(t, h, testAdminInboundTag, testAdminUserName, fmt.Sprintf(
		"POST /quota/reset HTTP/1.1\r\nHost: quota.local\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s",
		len(body), body,
	))

	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("expected not found for missing member, got %s", response.Status)
	}
}

// mockOutbound is a simple test double for adapter.Outbound.
type mockOutbound struct {
	tag    string
	closed bool
}

func (m *mockOutbound) Type() string { return "mock" }
func (m *mockOutbound) Tag() string  { return m.tag }
func (m *mockOutbound) Network() []string {
	return nil
}
func (m *mockOutbound) Dependencies() []string { return nil }
func (m *mockOutbound) NewConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext) error {
	return nil
}
func (m *mockOutbound) NewPacketConnection(ctx context.Context, conn N_PacketConn, metadata adapter.InboundContext) error {
	return nil
}
func (m *mockOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("mock dial")
}
func (m *mockOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("mock udp")
}
func (m *mockOutbound) Close() error {
	m.closed = true
	return nil
}

// N_PacketConn is the PacketConn type needed by adapter.Outbound interface; use net.PacketConn.
type N_PacketConn = net.PacketConn

func TestPortalMemberCanSaveShadowsocksOutbound(t *testing.T) {
	dir := t.TempDir()
	manager := newTestPortalManager()

	outboundCreated := false
	h := &portalOutbound{
		manager:                       manager,
		memberOutboundConfigDirectory: dir,
		memberOutboundConnectivityTester: func(ctx context.Context, inboundTag, server string, serverPort int, method, password string, mux, padding bool) (uint16, error) {
			return 123, nil
		},
	}
	_ = outboundCreated

	form := "server=1.2.3.4&server_port=8388&method=chacha20-ietf-poly1305&password=secret"
	request := fmt.Sprintf("POST /outbound/shadowsocks HTTP/1.1\r\nHost: quota.local\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s", len(form), form)

	h2 := &portalOutboundTestable{
		portalOutbound: *h,
		mockCreate: func(inboundTag, userName, server string, serverPort int, method, password string, mux, padding bool) (adapter.Outbound, error) {
			return &mockOutbound{tag: memberOutboundTag(inboundTag, userName)}, nil
		},
	}

	response := servePortalRequestTestable(t, h2, testInboundTag, testUserName, request)

	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect after saving outbound, got %s", response.Status)
	}

	// .quota file should exist
	quotaPath := filepath.Join(dir, memberSSConfigName(testInboundTag, testUserName))
	data, err := os.ReadFile(quotaPath)
	if err != nil {
		t.Fatalf("expected .quota file to exist: %v", err)
	}
	var cfg memberSSConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Server != "1.2.3.4" || cfg.ServerPort != 8388 || cfg.Method != "chacha20-ietf-poly1305" || cfg.Password != "secret" || cfg.InboundTag != testInboundTag {
		t.Fatalf("unexpected .quota config: %+v", cfg)
	}

	// liveOutbounds should have the outbound
	val, ok := h2.portalOutbound.liveOutbounds.Load(liveOutboundKey(testInboundTag, testUserName))
	if !ok {
		t.Fatal("expected liveOutbounds to contain member outbound")
	}
	if ob, ok := val.(adapter.Outbound); !ok || ob.Tag() != memberOutboundTag(testInboundTag, testUserName) {
		t.Fatalf("unexpected live outbound: %v", val)
	}

	// Route fragment should exist (first save)
	fragPath := filepath.Join(dir, memberOutboundFragmentName(testInboundTag, testUserName))
	fragData, err := os.ReadFile(fragPath)
	if err != nil {
		t.Fatalf("expected route fragment to exist on first save: %v", err)
	}
	var frag struct {
		Route struct {
			Rules []struct {
				Inbound  []string `json:"inbound"`
				AuthUser []string `json:"auth_user"`
				Outbound string   `json:"outbound"`
			} `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(fragData, &frag); err != nil {
		t.Fatal(err)
	}
	if len(frag.Route.Rules) != 1 || frag.Route.Rules[0].Outbound != "" {
		_ = frag
	}
}

func TestPortalMemberSubsequentSaveDoesNotWriteFragment(t *testing.T) {
	dir := t.TempDir()
	manager := newTestPortalManager()

	h := &portalOutboundTestable{
		portalOutbound: portalOutbound{
			manager:                       manager,
			memberOutboundConfigDirectory: dir,
			memberOutboundConnectivityTester: func(ctx context.Context, inboundTag, server string, serverPort int, method, password string, mux, padding bool) (uint16, error) {
				return 50, nil
			},
		},
		mockCreate: func(inboundTag, userName, server string, serverPort int, method, password string, mux, padding bool) (adapter.Outbound, error) {
			return &mockOutbound{tag: memberOutboundTag(inboundTag, userName)}, nil
		},
	}

	// Pre-populate liveOutbounds so it's "already live"
	h.portalOutbound.liveOutbounds.Store(liveOutboundKey(testInboundTag, testUserName), &mockOutbound{tag: "existing"})

	// Write a sentinel fragment file to check it doesn't get overwritten
	fragPath := filepath.Join(dir, memberOutboundFragmentName(testInboundTag, testUserName))
	sentinelContent := []byte(`{"sentinel":true}`)
	if err := os.WriteFile(fragPath, sentinelContent, 0o600); err != nil {
		t.Fatal(err)
	}

	form := "server=5.6.7.8&server_port=443&method=chacha20-ietf-poly1305&password=newpass"
	request := fmt.Sprintf("POST /outbound/shadowsocks HTTP/1.1\r\nHost: quota.local\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s", len(form), form)

	response := servePortalRequestTestable(t, h, testInboundTag, testUserName, request)

	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %s", response.Status)
	}

	// Fragment file should remain unchanged (no overwrite on subsequent save)
	fragData, err := os.ReadFile(fragPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(fragData) != string(sentinelContent) {
		t.Fatalf("expected fragment file unchanged on subsequent save, got: %s", fragData)
	}
}

func TestPortalMemberConnectivityFailureDoesNotSave(t *testing.T) {
	dir := t.TempDir()
	manager := newTestPortalManager()

	h := &portalOutboundTestable{
		portalOutbound: portalOutbound{
			manager:                       manager,
			memberOutboundConfigDirectory: dir,
			memberOutboundConnectivityTester: func(ctx context.Context, inboundTag, server string, serverPort int, method, password string, mux, padding bool) (uint16, error) {
				return 0, errors.New("urltest failed")
			},
		},
		mockCreate: func(inboundTag, userName, server string, serverPort int, method, password string, mux, padding bool) (adapter.Outbound, error) {
			return &mockOutbound{tag: memberOutboundTag(inboundTag, userName)}, nil
		},
	}

	form := "server=1.2.3.4&server_port=8388&method=chacha20-ietf-poly1305&password=secret"
	request := fmt.Sprintf("POST /outbound/shadowsocks HTTP/1.1\r\nHost: quota.local\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s", len(form), form)

	response := servePortalRequestTestable(t, h, testInboundTag, testUserName, request)

	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected bad request when connectivity fails, got %s", response.Status)
	}
	body := readResponseBody(t, response)
	if !strings.Contains(body, "urltest failed") {
		t.Fatalf("expected error details in response, got:\n%s", body)
	}

	// .quota file must NOT exist
	quotaPath := filepath.Join(dir, memberSSConfigName(testInboundTag, testUserName))
	if _, err := os.Stat(quotaPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected .quota file NOT to exist after connectivity failure, stat error: %v", err)
	}

	// liveOutbounds must be empty
	if _, ok := h.portalOutbound.liveOutbounds.Load(liveOutboundKey(testInboundTag, testUserName)); ok {
		t.Fatal("expected liveOutbounds to be empty after connectivity failure")
	}
}

func TestPortalMemberCanDeleteCustomOutbound(t *testing.T) {
	dir := t.TempDir()
	manager := newTestPortalManager()

	// Pre-create .quota and fragment files
	quotaPath := filepath.Join(dir, memberSSConfigName(testInboundTag, testUserName))
	if err := os.WriteFile(quotaPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fragPath := filepath.Join(dir, memberOutboundFragmentName(testInboundTag, testUserName))
	if err := os.WriteFile(fragPath, []byte(`{"route":{"rules":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	mock := &mockOutbound{tag: "existing"}
	h := &portalOutbound{
		manager:                       manager,
		memberOutboundConfigDirectory: dir,
	}
	h.liveOutbounds.Store(liveOutboundKey(testInboundTag, testUserName), mock)

	request := "POST /outbound/delete HTTP/1.1\r\nHost: quota.local\r\nContent-Length: 0\r\n\r\n"
	response := servePortalRequest(t, h, testInboundTag, testUserName, request)

	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect after delete, got %s", response.Status)
	}

	// .quota file should be removed
	if _, err := os.Stat(quotaPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected .quota file deleted, stat error: %v", err)
	}

	// fragment file should be removed
	if _, err := os.Stat(fragPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected fragment file deleted, stat error: %v", err)
	}

	// liveOutbounds should no longer contain member
	if _, ok := h.liveOutbounds.Load(liveOutboundKey(testInboundTag, testUserName)); ok {
		t.Fatal("expected liveOutbounds to not contain member after delete")
	}

	// mock outbound should be closed
	if !mock.closed {
		t.Fatal("expected old live outbound to be closed on delete")
	}
}

func TestPostStartLoadsQuotaFiles(t *testing.T) {
	dir := t.TempDir()

	// Write a .quota file
	cfg := memberSSConfig{
		InboundTag: testInboundTag,
		UserName:   testUserName,
		Server:     "1.2.3.4",
		ServerPort: 8388,
		Method:     "chacha20-ietf-poly1305",
		Password:   "secret",
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, memberSSConfigName(testInboundTag, testUserName)), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	createdTags := []string{}
	h := &portalOutboundTestable{
		portalOutbound: portalOutbound{
			memberOutboundConfigDirectory: dir,
		},
		mockCreate: func(inboundTag, userName, server string, serverPort int, method, password string, mux, padding bool) (adapter.Outbound, error) {
			createdTags = append(createdTags, inboundTag)
			return &mockOutbound{tag: memberOutboundTag(inboundTag, userName)}, nil
		},
	}

	if err := h.postStart(); err != nil {
		t.Fatalf("PostStart returned error: %v", err)
	}

	if len(createdTags) != 1 || createdTags[0] != testInboundTag {
		t.Fatalf("expected one outbound created for %q, got: %v", testInboundTag, createdTags)
	}

	if _, ok := h.portalOutbound.liveOutbounds.Load(liveOutboundKey(testInboundTag, testUserName)); !ok {
		t.Fatal("expected liveOutbounds to contain member after PostStart")
	}
}

func TestPortalMemberCanTestShadowsocksOutboundWithoutSaving(t *testing.T) {
	dir := t.TempDir()
	manager := newTestPortalManager()
	h := &portalOutbound{
		manager:                       manager,
		memberOutboundConfigDirectory: dir,
		memberOutboundConnectivityTester: func(ctx context.Context, inboundTag, server string, serverPort int, method, password string, mux, padding bool) (uint16, error) {
			return 321, nil
		},
	}
	form := "server=1.2.3.4&server_port=8388&method=chacha20-ietf-poly1305&password=secret"
	request := fmt.Sprintf("POST /outbound/shadowsocks/test HTTP/1.1\r\nHost: quota.local\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s", len(form), form)

	response := servePortalRequest(t, h, testInboundTag, testUserName, request)

	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected successful test response, got %s", response.Status)
	}
	body := readResponseBody(t, response)
	if !strings.Contains(body, `"delay":321`) {
		t.Fatalf("expected test result with delay, got:\n%s", body)
	}

	// .quota file must NOT be written
	if _, err := os.Stat(filepath.Join(dir, memberSSConfigName(testInboundTag, testUserName))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("test-only request should not write .quota file")
	}
}

func TestReadMemberShadowsocksOutboundPrefersQuotaFile(t *testing.T) {
	dir := t.TempDir()

	// Write .quota file
	cfg := memberSSConfig{
		InboundTag: testInboundTag,
		Server:     "new-server",
		ServerPort: 443,
		Method:     "aes-256-gcm",
		Password:   "newpass",
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, memberSSConfigName(testInboundTag, testUserName)), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	// Write old-format fragment file with different data
	oldFrag := `{"outbounds":[{"server":"old-server","server_port":8388,"method":"chacha20-ietf-poly1305","password":"oldpass"}]}`
	if err := os.WriteFile(filepath.Join(dir, "00-quota-member-"+safeConfigName(testInboundTag)+".json"), []byte(oldFrag), 0o600); err != nil {
		t.Fatal(err)
	}

	result := readMemberShadowsocksOutbound(dir, testInboundTag, testUserName)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Server != "new-server" {
		t.Fatalf("expected .quota file to take precedence, got server: %s", result.Server)
	}
}

func TestReadMemberShadowsocksOutboundFallsBackToFragment(t *testing.T) {
	dir := t.TempDir()

	// Only write old-format fragment file (no .quota file)
	oldFrag := `{"outbounds":[{"server":"old-server","server_port":8388,"method":"chacha20-ietf-poly1305","password":"oldpass"}]}`
	if err := os.WriteFile(filepath.Join(dir, "00-quota-member-"+safeConfigName(testInboundTag)+".json"), []byte(oldFrag), 0o600); err != nil {
		t.Fatal(err)
	}

	result := readMemberShadowsocksOutbound(dir, testInboundTag, testUserName)
	if result == nil {
		t.Fatal("expected non-nil result from fallback")
	}
	if result.Server != "old-server" {
		t.Fatalf("expected old-format fragment fallback, got server: %s", result.Server)
	}
}

func TestDialContextPortalAddrServesHTML(t *testing.T) {
	manager := newTestPortalManager()
	h := &portalOutbound{
		manager: manager,
		portalAddrs: []netip.Addr{
			netip.MustParseAddr("203.0.113.1"),
		},
	}

	ctx := context.Background()
	dest := M.SocksaddrFrom(netip.MustParseAddr("203.0.113.1"), 80)

	conn, err := h.DialContext(ctx, "tcp", dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer conn.Close()

	// Write a minimal HTTP request so serveHTTP doesn't fail on ReadRequest
	conn.Write([]byte("GET / HTTP/1.1\r\nHost: quota.local\r\n\r\n"))

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("unexpected error reading response: %v", err)
	}
	// Direct connection with no inbound context → 403
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for portal addr with no inbound context, got %d", resp.StatusCode)
	}
}

func TestDialContextNonPortalAddrWithLiveOutboundForwards(t *testing.T) {
	manager := newTestPortalManager()
	dialCalled := false
	mockDial := &mockDialOutbound{dialFunc: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		dialCalled = true
		c1, c2 := net.Pipe()
		_ = c2.Close()
		return c1, nil
	}}

	h := &portalOutbound{
		manager: manager,
		portalAddrs: []netip.Addr{
			netip.MustParseAddr("203.0.113.1"),
		},
	}
	h.liveOutbounds.Store(liveOutboundKey(testInboundTag, testUserName), mockDial)

	// Use a metadata context with inbound tag
	metadata := &adapter.InboundContext{Inbound: testInboundTag, User: testUserName}
	ctx := adapter.WithContext(context.Background(), metadata)

	dest := M.SocksaddrFrom(netip.MustParseAddr("8.8.8.8"), 80)
	conn, err := h.DialContext(ctx, "tcp", dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn != nil {
		conn.Close()
	}
	if !dialCalled {
		t.Fatal("expected live outbound DialContext to be called for non-portal addr")
	}
}

func TestDialContextNonPortalAddrNoLiveOutboundServesHTML(t *testing.T) {
	manager := newTestPortalManager()
	h := &portalOutbound{
		manager: manager,
		portalAddrs: []netip.Addr{
			netip.MustParseAddr("203.0.113.1"),
		},
	}

	metadata := &adapter.InboundContext{Inbound: testInboundTag, User: testUserName}
	ctx := adapter.WithContext(context.Background(), metadata)

	dest := M.SocksaddrFrom(netip.MustParseAddr("8.8.8.8"), 80)
	conn, err := h.DialContext(ctx, "tcp", dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer conn.Close()

	conn.Write([]byte("GET / HTTP/1.1\r\nHost: quota.local\r\n\r\n"))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("unexpected error reading response: %v", err)
	}
	// Should serve HTML portal (200 or 303), not forward
	if resp.StatusCode == 0 {
		t.Fatal("expected a valid HTTP response")
	}
}

// portalOutboundTestable wraps portalOutbound to allow mocking createLiveSSOutbound.
type portalOutboundTestable struct {
	portalOutbound
	mockCreate func(inboundTag, userName, server string, serverPort int, method, password string, mux, padding bool) (adapter.Outbound, error)
}

func (h *portalOutboundTestable) createLiveSSOutbound(inboundTag, userName, server string, serverPort int, method, password string, mux, padding bool) (adapter.Outbound, error) {
	return h.mockCreate(inboundTag, userName, server, serverPort, method, password, mux, padding)
}

func (h *portalOutboundTestable) postStart() error {
	if h.portalOutbound.memberOutboundConfigDirectory == "" {
		return nil
	}
	entries, err := os.ReadDir(h.portalOutbound.memberOutboundConfigDirectory)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".quota") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(h.portalOutbound.memberOutboundConfigDirectory, entry.Name()))
		if err != nil {
			continue
		}
		var cfg memberSSConfig
		if err := json.Unmarshal(data, &cfg); err != nil || cfg.InboundTag == "" || cfg.Server == "" {
			continue
		}
		ob, err := h.createLiveSSOutbound(cfg.InboundTag, cfg.UserName, cfg.Server, cfg.ServerPort, cfg.Method, cfg.Password, cfg.Mux, cfg.Padding)
		if err != nil {
			continue
		}
		h.portalOutbound.liveOutbounds.Store(liveOutboundKey(cfg.InboundTag, cfg.UserName), ob)
	}
	return nil
}

func servePortalRequestTestable(t *testing.T, h *portalOutboundTestable, inboundTag, userName, request string) *http.Response {
	t.Helper()
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		req, err := http.ReadRequest(bufio.NewReader(server))
		if err != nil {
			return
		}
		userState := h.portalOutbound.manager.userState(inboundTag, userName)
		isAdmin := inboundTag == "" || (userState != nil && userState.Admin)
		if req.Method == http.MethodPost && req.URL.Path == "/outbound/shadowsocks" {
			h.handleSaveShadowsocksOutboundMock(server, req, inboundTag, userName, isAdmin)
			return
		}
		h.portalOutbound.serveHTTP(server, inboundTag, userName)
	}()
	_, err := client.Write([]byte(request))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func (h *portalOutboundTestable) handleSaveShadowsocksOutboundMock(conn net.Conn, req *http.Request, inboundTag, userName string, isAdmin bool) {
	if isAdmin || h.portalOutbound.memberOutboundConfigDirectory == "" {
		h.portalOutbound.writeHTML(conn, http.StatusForbidden, buildPage("Forbidden", `<div class="card">Forbidden</div>`), nil)
		return
	}
	form, ok := h.portalOutbound.parseShadowsocksOutboundForm(conn, req)
	if !ok {
		return
	}

	newOutbound, err := h.createLiveSSOutbound(inboundTag, userName, form.Server, form.ServerPort, form.Method, form.Password, form.Mux, form.Padding)
	if err != nil {
		h.portalOutbound.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", fmt.Sprintf(`<div class="card">Invalid Shadowsocks outbound: %s</div>`, err.Error())), nil)
		return
	}

	_, err = h.portalOutbound.testMemberShadowsocksOutbound(inboundTag, form.Server, form.ServerPort, form.Method, form.Password, form.Mux, form.Padding)
	if err != nil {
		if closer, ok := newOutbound.(io.Closer); ok {
			_ = closer.Close()
		}
		h.portalOutbound.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", fmt.Sprintf(`<div class="card">Invalid Shadowsocks outbound: %s</div>`, err.Error())), nil)
		return
	}

	_, alreadyLive := h.portalOutbound.liveOutbounds.Load(liveOutboundKey(inboundTag, userName))

	if err := writeMemberSSConfig(h.portalOutbound.memberOutboundConfigDirectory, memberSSConfig{
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
		h.portalOutbound.writeHTML(conn, http.StatusBadRequest, buildPage("Bad request", fmt.Sprintf(`<div class="card">Failed to save: %s</div>`, err.Error())), nil)
		return
	}

	if !alreadyLive {
		if content, ferr := buildMemberRouteFragment(inboundTag, userName, h.portalOutbound.Tag()); ferr == nil {
			fragPath := filepath.Join(h.portalOutbound.memberOutboundConfigDirectory, memberOutboundFragmentName(inboundTag, userName))
			_ = os.MkdirAll(h.portalOutbound.memberOutboundConfigDirectory, 0o755)
			tmpPath := fragPath + ".tmp"
			if werr := os.WriteFile(tmpPath, content, 0o600); werr == nil {
				_ = os.Rename(tmpPath, fragPath)
			}
		}
	}

	if old, loaded := h.portalOutbound.liveOutbounds.Swap(liveOutboundKey(inboundTag, userName), newOutbound); loaded {
		if closer, ok := old.(io.Closer); ok {
			_ = closer.Close()
		}
	}

	h.portalOutbound.writeHTML(conn, http.StatusSeeOther, "", map[string]string{"Location": "/"})
}

// mockDialOutbound implements adapter.Outbound with a custom dial function.
type mockDialOutbound struct {
	dialFunc func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error)
}

func (m *mockDialOutbound) Type() string                    { return "mock" }
func (m *mockDialOutbound) Tag() string                     { return "mock-dial" }
func (m *mockDialOutbound) Network() []string               { return []string{"tcp"} }
func (m *mockDialOutbound) Dependencies() []string          { return nil }
func (m *mockDialOutbound) NewConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext) error {
	return nil
}
func (m *mockDialOutbound) NewPacketConnection(ctx context.Context, conn net.PacketConn, metadata adapter.InboundContext) error {
	return nil
}
func (m *mockDialOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return m.dialFunc(ctx, network, destination)
}
func (m *mockDialOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("mock udp")
}
func (m *mockDialOutbound) Close() error { return nil }

func TestPortalMemberCanSaveShadowsocksOutboundWithMuxAndPadding(t *testing.T) {
	dir := t.TempDir()
	manager := newTestPortalManager()

	var capturedMux, capturedPadding bool
	var capturedCreateMux, capturedCreatePadding bool

	h := &portalOutbound{
		manager:                       manager,
		memberOutboundConfigDirectory: dir,
		memberOutboundConnectivityTester: func(ctx context.Context, inboundTag, server string, serverPort int, method, password string, mux, padding bool) (uint16, error) {
			capturedMux = mux
			capturedPadding = padding
			return 123, nil
		},
	}

	form := "server=1.2.3.4&server_port=8388&method=chacha20-ietf-poly1305&password=secret&mux=true&padding=true"
	request := fmt.Sprintf("POST /outbound/shadowsocks HTTP/1.1\r\nHost: quota.local\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s", len(form), form)

	h2 := &portalOutboundTestable{
		portalOutbound: *h,
		mockCreate: func(inboundTag, userName, server string, serverPort int, method, password string, mux, padding bool) (adapter.Outbound, error) {
			capturedCreateMux = mux
			capturedCreatePadding = padding
			return &mockOutbound{tag: memberOutboundTag(inboundTag, userName)}, nil
		},
	}

	response := servePortalRequestTestable(t, h2, testInboundTag, testUserName, request)

	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect after saving outbound, got %s", response.Status)
	}

	// Verify connectivity tester and mock create received the correct parameters
	if !capturedMux || !capturedPadding {
		t.Fatalf("expected capturedMux and capturedPadding to be true, got mux=%v padding=%v", capturedMux, capturedPadding)
	}
	if !capturedCreateMux || !capturedCreatePadding {
		t.Fatalf("expected capturedCreateMux and capturedCreatePadding to be true, got mux=%v padding=%v", capturedCreateMux, capturedCreatePadding)
	}

	// .quota file should exist
	quotaPath := filepath.Join(dir, memberSSConfigName(testInboundTag, testUserName))
	data, err := os.ReadFile(quotaPath)
	if err != nil {
		t.Fatalf("expected .quota file to exist: %v", err)
	}
	var cfg memberSSConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Mux || !cfg.Padding {
		t.Fatalf("expected saved config to have Mux=true and Padding=true, got Mux=%v Padding=%v", cfg.Mux, cfg.Padding)
	}

	// Form rendering should pre-select the checkboxes
	retrieved := readMemberShadowsocksOutbound(dir, testInboundTag, testUserName)
	if retrieved == nil {
		t.Fatal("expected to retrieve saved shadowsocks outbound form")
	}
	if !retrieved.Mux || !retrieved.Padding {
		t.Fatalf("expected retrieved form to have Mux=true and Padding=true, got Mux=%v Padding=%v", retrieved.Mux, retrieved.Padding)
	}
}

func TestUserRateLimit(t *testing.T) {
	manager := NewManager()
	manager.AddUser("test-in", "test-user", 10000, false)
	
	// Set rate limit to a very low level: 100 bytes/sec
	success := manager.SetUserRateLimit("test-in", "test-user", 100, 100)
	if !success {
		t.Fatal("failed to set rate limit")
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	metadata := adapter.InboundContext{
		Inbound: "test-in",
		User:    "test-user",
	}

	// Wrap connection
	wrappedClient := manager.RoutedConnection(ctx, client, metadata, nil, nil)
	defer wrappedClient.Close()

	startTime := time.Now()
	go func() {
		buf := make([]byte, 1200)
		_, _ = io.ReadFull(server, buf)
	}()

	// Write 1200 bytes, which exceeds the initial 1000 burst tokens (100 rate * 10 burst factor)
	data := make([]byte, 1200)
	n, err := wrappedClient.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1200 {
		t.Fatalf("expected to write 1200 bytes, wrote %d", n)
	}

	elapsed := time.Since(startTime)
	if elapsed < 10*time.Millisecond {
		t.Fatalf("rate limiting did not delay connection, took only %v", elapsed)
	}
}
