package quota

import (
	"context"
	"io"
	"net"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
)

// nopPacketConn is a packet connection over a net.Conn that only needs to
// close.
type nopPacketConn struct{ net.Conn }

func (nopPacketConn) ReadPacket(*buf.Buffer) (M.Socksaddr, error)    { return M.Socksaddr{}, io.EOF }
func (nopPacketConn) WritePacket(b *buf.Buffer, _ M.Socksaddr) error { b.Release(); return nil }

type fakeFlowHandle struct {
	tracker tun.FlowTracker
	closed  bool
}

func (h *fakeFlowHandle) CloseFlow() {
	h.closed = true
	h.tracker.CloseFlow(tun.FlowCloseReset)
}

func newConnTestManager() *Manager {
	m := NewManager()
	m.AddUser("ss-in", adapter.QuotaUser{Name: "alice", QuotaBytes: 1 << 30, Admin: true})
	m.AddUser("ss-in", adapter.QuotaUser{Name: "bob", QuotaBytes: 1 << 30})
	m.SetPortal([]netip.Addr{netip.MustParseAddr("203.0.113.1")}, nil)
	return m
}

func routeTCP(t *testing.T, m *Manager, user, host string) (net.Conn, net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	metadata := adapter.InboundContext{Inbound: "ss-in", User: user, Destination: M.ParseSocksaddrHostPort(host, 443)}
	return client, m.RoutedConnection(context.Background(), server, metadata, nil, nil)
}

func TestConnectionsTrackPerUser(t *testing.T) {
	m := newConnTestManager()
	client, conn := routeTCP(t, m, "alice", "example.com")
	routeTCP(t, m, "bob", "example.org")

	// Writing to the routed connection sends to the client: download.
	go io.ReadFull(client, make([]byte, 5))
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}

	alice, ok := m.UserConnections("ss-in", "alice")
	if !ok || alice.ActiveCount != 1 || len(alice.Active) != 1 {
		t.Fatalf("alice = %+v, want one active", alice)
	}
	if got := alice.Active[0]; got.Host != "example.com" || got.Network != "tcp" || got.Download != 5 || !got.Active || got.Rule != "final" {
		t.Fatalf("alice's connection = %+v", got)
	}
	bob, _ := m.UserConnections("ss-in", "bob")
	if bob.ActiveCount != 1 || bob.Active[0].Host != "example.org" {
		t.Fatalf("bob = %+v, want only his own", bob)
	}

	conn.Close()
	conn.Close()
	alice, _ = m.UserConnections("ss-in", "alice")
	if alice.ActiveCount != 0 || alice.ClosedTotal != 1 || len(alice.Closed) != 1 || alice.Closed[0].Active || alice.Closed[0].ClosedAt == 0 || alice.Closed[0].Download != 5 {
		t.Fatalf("alice after close = %+v", alice)
	}
	if snapshot, _ := m.Snapshot("ss-in", "bob"); snapshot.ActiveConnections != 1 {
		t.Fatalf("bob's active_connections = %d, want 1", snapshot.ActiveConnections)
	}
}

func TestConnectionsClosedRingKeepsNewest(t *testing.T) {
	m := newConnTestManager()
	total := connClosedLimit + 25
	for i := 0; i < total; i++ {
		_, conn := routeTCP(t, m, "alice", "example.com")
		conn.Close()
	}
	alice, _ := m.UserConnections("ss-in", "alice")
	if alice.ClosedTotal != int64(total) || len(alice.Closed) != connClosedLimit {
		t.Fatalf("closed_total = %d, kept %d", alice.ClosedTotal, len(alice.Closed))
	}
	// Newest first: the last ID handed out leads, and IDs drop by one.
	if alice.Closed[0].ID != "525" || alice.Closed[connClosedLimit-1].ID != "26" {
		t.Fatalf("kept %s..%s, want 525..26", alice.Closed[0].ID, alice.Closed[connClosedLimit-1].ID)
	}
}

func TestConnectionsKeepClosedForMembers(t *testing.T) {
	m := newConnTestManager()
	_, conn := routeTCP(t, m, "bob", "example.org")
	conn.Close()
	bob, _ := m.UserConnections("ss-in", "bob")
	if bob.ClosedTotal != 1 || len(bob.Closed) != 1 || bob.Closed[0].Host != "example.org" {
		t.Fatalf("bob = %+v, want his closed connection kept", bob)
	}
}

func TestConnectionsHidePortalButNotCustomOutbound(t *testing.T) {
	m := newConnTestManager()
	_, portal := routeTCP(t, m, "alice", "203.0.113.1")
	routeTCP(t, m, "alice", "example.com")

	alice, _ := m.UserConnections("ss-in", "alice")
	if alice.ActiveCount != 1 || alice.Active[0].Host != "example.com" {
		t.Fatalf("alice = %+v, want only the non-portal connection", alice)
	}
	portal.Close()
	if alice, _ = m.UserConnections("ss-in", "alice"); alice.ClosedTotal != 0 {
		t.Fatalf("closing the portal's own connection was recorded: %+v", alice)
	}
}

func TestConnectionsCloseOneAndAll(t *testing.T) {
	m := newConnTestManager()
	routeTCP(t, m, "alice", "203.0.113.1")
	_, a := routeTCP(t, m, "alice", "a.example")
	_, b := routeTCP(t, m, "alice", "b.example")
	routeTCP(t, m, "bob", "c.example")

	alice, _ := m.UserConnections("ss-in", "alice")
	bob, _ := m.UserConnections("ss-in", "bob")
	if m.CloseUserConnection("ss-in", "alice", bob.Active[0].ID) {
		t.Fatal("alice closed bob's connection")
	}
	if !m.CloseUserConnection("ss-in", "alice", alice.Active[0].ID) {
		t.Fatal("alice couldn't close her own connection")
	}
	if n := m.CloseUserActiveConnections("ss-in", "alice"); n != 1 {
		t.Fatalf("closed %d, want 1", n)
	}
	for _, c := range []net.Conn{a, b} {
		if _, err := c.Write([]byte("x")); err == nil {
			t.Fatal("write succeeded on a closed connection")
		}
	}
	// The portal's own connection survives closing the visible ones, and
	// closing everything (an outbound change) takes it too.
	if n := m.conns.closeAll(UserKey{"ss-in", "alice"}, true); n != 1 {
		t.Fatalf("hidden connections left = %d, want 1", n)
	}
	if bob, _ = m.UserConnections("ss-in", "bob"); bob.ActiveCount != 1 {
		t.Fatalf("bob = %+v, want untouched", bob)
	}
}

func TestConnectionsPacketAndSubscribe(t *testing.T) {
	m := newConnTestManager()
	changed, stop := m.conns.subscribe(UserKey{"ss-in", "alice"})
	defer stop()

	a, b := net.Pipe()
	defer b.Close()
	conn := m.RoutedPacketConnection(context.Background(), nopPacketConn{a}, adapter.InboundContext{Inbound: "ss-in", User: "alice", Destination: M.ParseSocksaddrHostPort("8.8.8.8", 443)}, nil, nil)
	select {
	case <-changed:
	default:
		t.Fatal("no notification on open")
	}
	alice, _ := m.UserConnections("ss-in", "alice")
	if alice.ActiveCount != 1 || alice.Active[0].Network != "udp" || alice.Active[0].Host != "8.8.8.8" {
		t.Fatalf("alice = %+v", alice)
	}
	conn.Close()
	select {
	case <-changed:
	default:
		t.Fatal("no notification on close")
	}
	if alice, _ = m.UserConnections("ss-in", "alice"); alice.ClosedTotal != 1 {
		t.Fatalf("alice = %+v", alice)
	}
}

func TestConnectionsFlow(t *testing.T) {
	m := newConnTestManager()
	metadata := adapter.InboundContext{Inbound: "ss-in", User: "alice", Network: "udp", Destination: M.ParseSocksaddrHostPort("9.9.9.9", 443)}
	tracker := m.RoutedFlow(context.Background(), metadata, nil, nil)
	handle := &fakeFlowHandle{tracker: tracker}
	tracker.AttachFlow(handle)
	tracker.CountForward(3)
	tracker.CountReverse(7)

	alice, _ := m.UserConnections("ss-in", "alice")
	if alice.ActiveCount != 1 || alice.Active[0].Upload != 3 || alice.Active[0].Download != 7 || alice.Active[0].Network != "udp" {
		t.Fatalf("alice = %+v", alice)
	}
	if !m.CloseUserConnection("ss-in", "alice", alice.Active[0].ID) || !handle.closed {
		t.Fatal("closing the flow didn't reach its handle")
	}
	if alice, _ = m.UserConnections("ss-in", "alice"); alice.ActiveCount != 0 || alice.ClosedTotal != 1 {
		t.Fatalf("alice = %+v", alice)
	}
}
