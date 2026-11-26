package quota

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func dialPortalProbe(t *testing.T, h *portalOutbound, inboundTag string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			c, s := net.Pipe()
			go h.serveHTTP(s, inboundTag, "alice")
			return c, nil
		},
	}}
	return websocket.Dial(ctx, "ws://portal.test"+probePath, &websocket.DialOptions{HTTPClient: client})
}

func TestPortalProbeEchoes(t *testing.T) {
	ws, _, err := dialPortalProbe(t, &portalOutbound{}, "ss-in")
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, msg := range []string{"0", "1", "42"} {
		if err := ws.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			t.Fatal(err)
		}
		kind, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kind != websocket.MessageText || string(data) != msg {
			t.Fatalf("echo = %v %q, want text %q", kind, data, msg)
		}
	}
}

func TestPortalProbeNeedsInbound(t *testing.T) {
	if ws, _, err := dialPortalProbe(t, &portalOutbound{}, ""); err == nil {
		ws.CloseNow()
		t.Fatal("probe accepted a connection without an inbound")
	}
}

func TestPortalProbeRejectsLargeMessages(t *testing.T) {
	ws, _, err := dialPortalProbe(t, &portalOutbound{}, "ss-in")
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageText, make([]byte, probeMaxMessage+1)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.Read(ctx); err == nil {
		t.Fatal("oversized message was echoed")
	}
}
