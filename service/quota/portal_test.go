package quota

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"

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

// The dashboard resets over fetch and takes JSON back instead of a redirect.
func TestPortalAdminResetAnswersJSON(t *testing.T) {
	manager := newTestPortalManager()
	h := &portalOutbound{manager: manager}
	reset := func(name string) (*http.Response, string) {
		body := "tag=" + testInboundTag + "&name=" + name
		response := servePortalRequest(t, h, testAdminInboundTag, testAdminUserName, fmt.Sprintf(
			"POST /quota/reset HTTP/1.1\r\nHost: quota.local\r\nAccept: application/json\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s",
			len(body), body,
		))
		return response, readResponseBody(t, response)
	}
	if response, got := reset(testUserName); response.StatusCode != http.StatusOK || got != `{"ok":true}` {
		t.Fatalf("expected JSON success, got %s %s", response.Status, got)
	}
	if snapshot, _ := manager.Snapshot(testInboundTag, testUserName); snapshot.UsedBytes != 0 {
		t.Fatalf("expected reset counters, got %+v", snapshot)
	}
	if response, got := reset("nobody"); response.StatusCode != http.StatusNotFound || got != `{"error":"User not found"}` {
		t.Fatalf("expected a JSON 404, got %s %s", response.Status, got)
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

func TestPortalMemberPageDoesNotRenderResetControls(t *testing.T) {
	h := &portalOutbound{manager: newTestPortalManager()}
	page := h.buildPage(testInboundTag, testUserName, false)

	if page.Users != nil {
		t.Fatalf("member page should not list users to reset: %+v", page.Users)
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

func TestSubscriptionURI(t *testing.T) {
	state := &UserState{
		Key:                UserKey{InboundTag: "ss-in", UserName: "Grey"},
		Method:             "2022-blake3-aes-128-gcm",
		ServerPassword:     "serverpsk",
		UserPassword:       "userpsk",
		SubscriptionServer: "example.com",
		SubscriptionPort:   8888,
	}
	uri, ok := state.SubscriptionURI()
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := "ss://" + base64.RawURLEncoding.EncodeToString([]byte("2022-blake3-aes-128-gcm:serverpsk:userpsk")) + "@example.com:8888#Grey"
	if uri != want {
		t.Fatalf("unexpected uri: got %q want %q", uri, want)
	}
}

func TestSubscriptionURIMissingInfo(t *testing.T) {
	state := &UserState{Key: UserKey{InboundTag: "ss-in", UserName: "Grey"}}
	if _, ok := state.SubscriptionURI(); ok {
		t.Fatal("expected ok=false when no subscription info is configured")
	}
}

func TestPortalOwnPageRendersSubscription(t *testing.T) {
	manager := newTestPortalManager()
	state := manager.userState(testInboundTag, testUserName)
	state.Method = "2022-blake3-aes-128-gcm"
	state.ServerPassword = "serverpsk"
	state.UserPassword = "userpsk"
	state.SubscriptionServer = "example.com"
	state.SubscriptionPort = 8888

	h := &portalOutbound{manager: manager}
	response := servePortalRequest(t, h, testInboundTag, testUserName, "GET / HTTP/1.1\r\nHost: quota.local\r\n\r\n")
	body := readResponseBody(t, response)

	if !strings.Contains(body, "ss://") || !strings.Contains(body, "Subscription") {
		t.Fatalf("expected subscription section in own page, got:\n%s", body)
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

func TestListenPacketWithLiveOutboundForwards(t *testing.T) {
	manager := newTestPortalManager()
	listenCalled := false
	mock := &mockDialOutbound{listenFunc: func(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
		listenCalled = true
		return nil, nil
	}}

	h := &portalOutbound{manager: manager}
	h.liveOutbounds.Store(liveOutboundKey(testInboundTag, testUserName), mock)

	metadata := &adapter.InboundContext{Inbound: testInboundTag, User: testUserName}
	ctx := adapter.WithContext(context.Background(), metadata)

	_, err := h.ListenPacket(ctx, M.SocksaddrFrom(netip.MustParseAddr("8.8.8.8"), 53))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !listenCalled {
		t.Fatal("expected live outbound ListenPacket to be called")
	}
}

func TestListenPacketWithoutLiveOutboundErrors(t *testing.T) {
	h := &portalOutbound{manager: newTestPortalManager()}

	metadata := &adapter.InboundContext{Inbound: testInboundTag, User: testUserName}
	ctx := adapter.WithContext(context.Background(), metadata)

	if _, err := h.ListenPacket(ctx, M.SocksaddrFrom(netip.MustParseAddr("8.8.8.8"), 53)); err == nil {
		t.Fatal("expected error when no live outbound is configured for the user")
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
// mockDialOutbound implements adapter.Outbound with a custom dial function.
type mockDialOutbound struct {
	dialFunc   func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error)
	listenFunc func(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error)
}

func (m *mockDialOutbound) Type() string           { return "mock" }
func (m *mockDialOutbound) Tag() string            { return "mock-dial" }
func (m *mockDialOutbound) Network() []string      { return []string{"tcp"} }
func (m *mockDialOutbound) Dependencies() []string { return nil }
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
	if m.listenFunc != nil {
		return m.listenFunc(ctx, destination)
	}
	return nil, errors.New("mock udp")
}
func (m *mockDialOutbound) Close() error { return nil }

func TestSubscriptionURIAnyTLS(t *testing.T) {
	state := &UserState{
		Key:              UserKey{InboundTag: "anytls-in", UserName: "grey"},
		Type:             "anytls",
		UserPassword:     "p@ss/word",
		ServerName:       "riven.d3v.ing",
		SubscriptionPort: 33999,
	}
	uri, ok := state.SubscriptionURI()
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := "anytls://p%40ss%2Fword@riven.d3v.ing:33999?sni=riven.d3v.ing#grey"
	if uri != want {
		t.Fatalf("unexpected uri: got %q want %q", uri, want)
	}
	state.SubscriptionServer = "1.2.3.4"
	uri, _ = state.SubscriptionURI()
	if want := "anytls://p%40ss%2Fword@1.2.3.4:33999?sni=riven.d3v.ing#grey"; uri != want {
		t.Fatalf("unexpected uri: got %q want %q", uri, want)
	}
	state.UserPassword = ""
	if _, ok := state.SubscriptionURI(); ok {
		t.Fatal("expected ok=false without password")
	}
}
