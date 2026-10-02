package quota

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	R "github.com/sagernet/sing-box/route/rule"
)

func TestTrafficStateFallsBackToAggregateUser(t *testing.T) {
	m := NewManager()
	m.AddUser("direct-in", adapter.QuotaUser{Name: adapter.QuotaInboundUser, QuotaBytes: 100})

	state := m.trafficState("direct-in", "")
	if state == nil {
		t.Fatal("expected aggregate state for empty user name")
	}
	if state.Key.UserName != adapter.QuotaInboundUser {
		t.Fatalf("expected key user name %q, got %q", adapter.QuotaInboundUser, state.Key.UserName)
	}

	if got := m.trafficState("direct-in", "someone-else"); got != nil {
		t.Fatalf("expected no state for unregistered named user, got %+v", got)
	}
}

func TestCheckUserBlocksOnAggregateQuota(t *testing.T) {
	m := NewManager()
	m.AddUser("direct-in", adapter.QuotaUser{Name: adapter.QuotaInboundUser, QuotaBytes: 10})

	if err := m.checkUser("direct-in", ""); err != nil {
		t.Fatalf("expected no error under quota, got %v", err)
	}

	state := m.userState("direct-in", adapter.QuotaInboundUser)
	state.Uplink.Store(10)

	err := m.checkUser("direct-in", "")
	if err == nil {
		t.Fatal("expected quota exceeded error")
	}
	if !R.IsRejected(err) {
		t.Fatalf("expected a rejected error, got %v (%T)", err, err)
	}
}

func TestCheckUserRealUserWithoutQuotaNotBlockedByAggregate(t *testing.T) {
	m := NewManager()
	m.AddUser("mix-in", adapter.QuotaUser{Name: adapter.QuotaInboundUser, QuotaBytes: 1})
	m.userState("mix-in", adapter.QuotaInboundUser).Uplink.Store(1) // aggregate already blocked

	if err := m.checkUser("mix-in", "someone"); err != nil {
		t.Fatalf("named user with no registered quota must be unaffected by the aggregate block, got %v", err)
	}
}

func TestRoutedConnectionTracksAggregateUser(t *testing.T) {
	m := NewManager()
	m.AddUser("direct-in", adapter.QuotaUser{Name: adapter.QuotaInboundUser, QuotaBytes: 1000})

	client, server := net.Pipe()
	defer client.Close()

	metadata := adapter.InboundContext{Inbound: "direct-in", User: ""}
	wrapped := m.RoutedConnection(context.Background(), server, metadata, nil, nil)
	if wrapped == server {
		t.Fatal("expected the connection to be wrapped for aggregate quota tracking")
	}

	done := make(chan struct{})
	go func() {
		buf := make([]byte, 5)
		_, _ = io.ReadFull(client, buf)
		close(done)
	}()
	if _, err := wrapped.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	<-done

	state := m.userState("direct-in", adapter.QuotaInboundUser)
	if used := state.Uplink.Load() + state.Downlink.Load(); used != 5 {
		t.Fatalf("expected 5 tracked bytes, got %d", used)
	}

	// The connection must be filed under the resolved aggregate key
	// (adapter.QuotaInboundUser), not the raw empty metadata.User, so that
	// CloseUserConnections can find and close it.
	m.CloseUserConnections("direct-in", adapter.QuotaInboundUser)

	if _, err := wrapped.Write([]byte("x")); err == nil {
		t.Fatal("expected write to fail after CloseUserConnections closed the connection")
	}
}
