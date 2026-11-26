package quota

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/coder/websocket"
)

func portalClient(h *portalOutbound, inboundTag, userName string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			c, s := net.Pipe()
			go h.serveHTTP(s, inboundTag, userName)
			return c, nil
		},
	}}
}

func TestPortalConnectionsJSON(t *testing.T) {
	m := newConnTestManager()
	routeTCP(t, m, "alice", "example.com")
	routeTCP(t, m, "bob", "example.org")
	h := &portalOutbound{manager: m}

	resp, err := portalClient(h, "ss-in", "alice").Get("http://portal.test" + connectionsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var snapshot ConnSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || snapshot.ActiveCount != 1 || snapshot.Active[0].Host != "example.com" {
		t.Fatalf("status %d, snapshot %+v", resp.StatusCode, snapshot)
	}
}

func TestPortalConnectionsWebSocket(t *testing.T) {
	m := newConnTestManager()
	h := &portalOutbound{manager: m}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://portal.test"+connectionsPath, &websocket.DialOptions{HTTPClient: portalClient(h, "ss-in", "alice")})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	read := func() connMessage {
		t.Helper()
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var message connMessage
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatal(err)
		}
		return message
	}
	if first := read(); first.Type != "snapshot" || first.ActiveCount != 0 {
		t.Fatalf("first = %+v", first)
	}
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"detail":true}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	_, conn := routeTCP(t, m, "alice", "example.com")
	routeTCP(t, m, "bob", "example.org")
	for {
		message := read()
		if message.ActiveCount == 1 {
			if len(message.Active) != 1 || message.Active[0].Host != "example.com" {
				t.Fatalf("pushed %+v", message)
			}
			break
		}
	}
	conn.Close()
	for {
		message := read()
		if message.ClosedTotal == 1 {
			if message.ActiveCount != 0 || len(message.Closed) != 1 {
				t.Fatalf("pushed %+v", message)
			}
			break
		}
	}
}

func TestPortalConnectionsClose(t *testing.T) {
	m := newConnTestManager()
	routeTCP(t, m, "alice", "a.example")
	routeTCP(t, m, "alice", "b.example")
	routeTCP(t, m, "bob", "c.example")
	h := &portalOutbound{manager: m}
	client := portalClient(h, "ss-in", "alice")
	post := func(form url.Values) (int, map[string]any) {
		t.Helper()
		resp, err := client.Post("http://portal.test"+connectionsClosePath, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body
	}

	bob, _ := m.UserConnections("ss-in", "bob")
	if status, _ := post(url.Values{"id": {bob.Active[0].ID}}); status != http.StatusNotFound {
		t.Fatalf("closing bob's connection as alice: %d", status)
	}
	alice, _ := m.UserConnections("ss-in", "alice")
	if status, body := post(url.Values{"id": {alice.Active[0].ID}}); status != http.StatusOK || body["closed"] != float64(1) {
		t.Fatalf("close one: %d %v", status, body)
	}
	if status, body := post(url.Values{"all": {"true"}}); status != http.StatusOK || body["closed"] != float64(1) {
		t.Fatalf("close all: %d %v", status, body)
	}
	if bob, _ = m.UserConnections("ss-in", "bob"); bob.ActiveCount != 1 {
		t.Fatalf("bob = %+v", bob)
	}
}

func TestPortalModernShowsConnections(t *testing.T) {
	m := newConnTestManager()
	m.AddUser("admin-in", adapterQuotaAdmin())
	routeTCP(t, m, "alice", "a.example")
	_, closed := routeTCP(t, m, "alice", "b.example")
	closed.Close()
	h := &portalOutbound{manager: m}

	page := h.buildPage("ss-in", "alice", true)
	if page.Conns == nil || *page.Conns != (pageConns{Active: 1, Closed: 1}) {
		t.Fatalf("member page lacks the connections tab: %+v", page.Conns)
	}
	admin := h.buildPage("admin-in", "root", true)
	var count int
	for _, u := range admin.Users {
		if u.UserName == "alice" {
			count = u.Conns
		}
	}
	if count != 1 {
		t.Fatalf("admin list lacks alice's connection count: %+v", admin.Users)
	}
	if admin.Conns == nil {
		t.Fatal("admin page lacks the connections tab")
	}
}

// Members follow and close their own connections like admins, never
// anyone else's.
func TestPortalConnectionsOpenToMembers(t *testing.T) {
	m := newConnTestManager()
	routeTCP(t, m, "alice", "a.example")
	routeTCP(t, m, "bob", "b.example")
	routeTCP(t, m, "bob", "c.example")
	h := &portalOutbound{manager: m}
	client := portalClient(h, "ss-in", "bob")

	resp, err := client.Get("http://portal.test" + connectionsPath)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot ConnSnapshot
	_ = json.NewDecoder(resp.Body).Decode(&snapshot)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || snapshot.ActiveCount != 2 {
		t.Fatalf("GET as a member: %d %+v", resp.StatusCode, snapshot)
	}
	for _, c := range snapshot.Active {
		if c.Host == "a.example" {
			t.Fatal("bob sees alice's connection")
		}
	}
	alice, _ := m.UserConnections("ss-in", "alice")
	resp, err = client.Post("http://portal.test"+connectionsClosePath, "application/x-www-form-urlencoded", strings.NewReader("id="+alice.Active[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bob closing alice's connection: %d, want 404", resp.StatusCode)
	}
	resp, err = client.Post("http://portal.test"+connectionsClosePath, "application/x-www-form-urlencoded", strings.NewReader("all=true"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if alice, _ = m.UserConnections("ss-in", "alice"); resp.StatusCode != http.StatusOK || alice.ActiveCount != 1 {
		t.Fatalf("bob closing all: %d, alice left with %d", resp.StatusCode, alice.ActiveCount)
	}
	if bob, _ := m.UserConnections("ss-in", "bob"); bob.ActiveCount != 0 {
		t.Fatalf("bob still has %d open", bob.ActiveCount)
	}

	page := h.buildPage("ss-in", "bob", false)
	if page.Conns == nil {
		t.Fatal("member page lacks the connections tab")
	}
	if page.Users != nil {
		t.Fatal("member page shows the admin user list")
	}
	resp, err = portalClient(h, "ss-in", "stranger").Get("http://portal.test" + connectionsPath)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unknown user: %d, want 403", resp.StatusCode)
	}
}

func TestPortalConnectionsNeedInbound(t *testing.T) {
	h := &portalOutbound{manager: newConnTestManager()}
	resp, err := portalClient(h, "", "").Get("http://portal.test" + connectionsPath)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403", resp.StatusCode)
	}
}

func adapterQuotaAdmin() adapter.QuotaUser {
	return adapter.QuotaUser{Name: "root", QuotaBytes: 1 << 30, Admin: true}
}
