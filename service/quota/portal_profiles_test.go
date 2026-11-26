package quota

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
)

type typedOutbound struct {
	mockDialOutbound
	kind, tag string
}

func (o *typedOutbound) Type() string { return o.kind }
func (o *typedOutbound) Tag() string  { return o.tag }

func newProfilesPortal(t *testing.T) *portalOutbound {
	t.Helper()
	return &portalOutbound{
		ctx:                           context.Background(),
		logger:                        log.NewNOPFactory().NewLogger("quota-portal"),
		manager:                       newTestPortalManager(),
		memberOutboundConfigDirectory: t.TempDir(),
		reload:                        func() {},
		buildOutbound: func(inboundTag, userName string, profile *outboundProfile) (adapter.Outbound, error) {
			for _, method := range shadowsocksMethods {
				if method == profile.Method {
					return &typedOutbound{kind: C.TypeShadowsocks, tag: profile.Name}, nil
				}
			}
			return nil, errors.New("unknown method: " + profile.Method)
		},
		memberOutboundConnectivityTester: func(ctx context.Context, inboundTag, server string, serverPort int, method, password string, mux, padding bool) (uint16, error) {
			if server == "down.example" {
				return 0, errors.New("unreachable")
			}
			return 42, nil
		},
	}
}

func postProfiles(t *testing.T, h *portalOutbound, action string, form url.Values) (int, map[string]any) {
	t.Helper()
	resp, err := portalClient(h, testInboundTag, testUserName).Post("http://portal.test"+profilesPath+action, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func nodeForm(name, server string) url.Values {
	return url.Values{"name": {name}, "server": {server}, "server_port": {"8388"}, "method": {"aes-128-gcm"}, "password": {"secret"}}
}

func liveTag(h *portalOutbound) string {
	val, ok := h.liveOutbounds.Load(liveOutboundKey(testInboundTag, testUserName))
	if !ok {
		return ""
	}
	return val.(adapter.Outbound).Tag()
}

func TestProfilesSaveActivateSwitchDelete(t *testing.T) {
	h := newProfilesPortal(t)

	status, body := postProfiles(t, h, "/save", nodeForm("Tokyo", "tokyo.example"))
	if status != http.StatusOK || body["active"] != "" {
		t.Fatalf("save: %d %v", status, body)
	}
	tokyo := body["id"].(string)
	if liveTag(h) != "" {
		t.Fatal("saving a node must not start using it")
	}
	_, body = postProfiles(t, h, "/save", nodeForm("", "hk.example"))
	hk := body["id"].(string)
	if profiles := body["profiles"].([]any); len(profiles) != 2 || profiles[1].(map[string]any)["name"] != "hk.example" {
		t.Fatalf("profiles = %v, want the second named after its server", profiles)
	}

	if status, _ = postProfiles(t, h, "/activate", url.Values{"id": {tokyo}}); status != http.StatusOK || liveTag(h) != "Tokyo" {
		t.Fatalf("activate: %d, live %q", status, liveTag(h))
	}
	if status, _ = postProfiles(t, h, "/activate", url.Values{"id": {hk}}); status != http.StatusOK || liveTag(h) != "hk.example" {
		t.Fatalf("switch: %d, live %q", status, liveTag(h))
	}
	// Editing the node in use swaps it in place.
	form := nodeForm("Hong Kong", "hk.example")
	form.Set("id", hk)
	if status, _ = postProfiles(t, h, "/save", form); status != http.StatusOK || liveTag(h) != "Hong Kong" {
		t.Fatalf("edit active: %d, live %q", status, liveTag(h))
	}
	// Back to the server's routing.
	if status, body = postProfiles(t, h, "/activate", url.Values{"id": {""}}); status != http.StatusOK || body["active"] != "" || liveTag(h) != "" {
		t.Fatalf("default: %d %v, live %q", status, body, liveTag(h))
	}
	postProfiles(t, h, "/activate", url.Values{"id": {tokyo}})
	if status, body = postProfiles(t, h, "/delete", url.Values{"id": {tokyo}}); status != http.StatusOK || body["active"] != "" || liveTag(h) != "" {
		t.Fatalf("deleting the node in use: %d %v, live %q", status, body, liveTag(h))
	}

	saved, err := readMemberProfiles(h.memberOutboundConfigDirectory, testInboundTag, testUserName)
	if err != nil || len(saved.Profiles) != 1 || saved.Profiles[0].ID != hk || saved.Active != "" {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	info, _ := os.Stat(filepath.Join(h.memberOutboundConfigDirectory, memberProfilesName(testInboundTag, testUserName)))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("node file mode %v, want 0600", info.Mode().Perm())
	}
}

func TestProfilesRejectBadInput(t *testing.T) {
	h := newProfilesPortal(t)
	for _, c := range []struct {
		action string
		form   url.Values
		status int
	}{
		{"/save", url.Values{"server": {"x"}, "server_port": {"0"}, "method": {"aes-128-gcm"}, "password": {"p"}}, http.StatusBadRequest},
		{"/save", url.Values{"server": {"x"}, "server_port": {"1"}, "method": {"no-such-cipher"}, "password": {"p"}}, http.StatusBadRequest},
		{"/save", url.Values{"id": {"missing"}, "server": {"x"}, "server_port": {"1"}, "method": {"aes-128-gcm"}, "password": {"p"}}, http.StatusNotFound},
		{"/activate", url.Values{"id": {"missing"}}, http.StatusNotFound},
		{"/delete", url.Values{"id": {"missing"}}, http.StatusNotFound},
		{"/nope", nil, http.StatusNotFound},
	} {
		if status, body := postProfiles(t, h, c.action, c.form); status != c.status || body["error"] == nil {
			t.Fatalf("%s %v: %d %v, want %d with an error", c.action, c.form, status, body, c.status)
		}
	}
	form := nodeForm(strings.Repeat("名", maxProfileName+1), "x.example")
	if status, _ := postProfiles(t, h, "/save", form); status != http.StatusBadRequest {
		t.Fatalf("long name: %d", status)
	}
	for i := 0; i < maxProfiles; i++ {
		postProfiles(t, h, "/save", nodeForm("", "x.example"))
	}
	if status, _ := postProfiles(t, h, "/save", nodeForm("", "x.example")); status != http.StatusBadRequest {
		t.Fatalf("node %d: %d, want refused", maxProfiles+1, status)
	}
}

func TestProfilesNeedDirectoryAndUser(t *testing.T) {
	h := newProfilesPortal(t)
	h.memberOutboundConfigDirectory = ""
	if status, _ := postProfiles(t, h, "/save", nodeForm("", "x.example")); status != http.StatusForbidden {
		t.Fatalf("without a directory: %d", status)
	}
	h = newProfilesPortal(t)
	resp, err := portalClient(h, testInboundTag, "stranger").Post("http://portal.test"+profilesPath+"/save", "application/x-www-form-urlencoded", strings.NewReader(nodeForm("", "x.example").Encode()))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown user: %d", resp.StatusCode)
	}
}

func TestProfilesTest(t *testing.T) {
	h := newProfilesPortal(t)
	if status, body := postProfiles(t, h, "/test", nodeForm("", "up.example")); status != http.StatusOK || body["delay"] != float64(42) {
		t.Fatalf("form test: %d %v", status, body)
	}
	_, body := postProfiles(t, h, "/save", nodeForm("", "down.example"))
	if status, body := postProfiles(t, h, "/test", url.Values{"id": {body["id"].(string)}}); status != http.StatusBadRequest || body["error"] != "unreachable" {
		t.Fatalf("saved test: %d %v", status, body)
	}
}

func TestOverrideOutboundUsesNodeInUse(t *testing.T) {
	h := newProfilesPortal(t)
	node := &typedOutbound{kind: C.TypeShadowsocks, tag: "node"}
	metadata := &adapter.InboundContext{Inbound: testInboundTag, User: testUserName}
	proxy := &typedOutbound{kind: C.TypeShadowsocks, tag: "server-proxy"}
	direct := &typedOutbound{kind: C.TypeDirect, tag: "direct-out"}

	// Without a node the server's choice stands, direct included.
	for _, picked := range []adapter.Outbound{proxy, direct} {
		if got := h.OverrideOutbound(metadata, picked); got != picked {
			t.Fatalf("no node: %s became %s", picked.Tag(), got.Tag())
		}
	}
	h.liveOutbounds.Store(liveOutboundKey(testInboundTag, testUserName), node)
	for _, picked := range []adapter.Outbound{proxy, direct} {
		if got := h.OverrideOutbound(metadata, picked); got != node {
			t.Fatalf("with a node: %s stayed %s", picked.Tag(), got.Tag())
		}
	}
	for _, kind := range []string{C.TypeBlock, C.TypeDNS, C.TypeQuotaPortal, C.TypeSpeedtestPortal} {
		picked := &typedOutbound{kind: kind, tag: kind}
		if got := h.OverrideOutbound(metadata, picked); got != picked {
			t.Fatalf("%s must not be overridden", kind)
		}
	}
	other := &adapter.InboundContext{Inbound: testInboundTag, User: "someone-else"}
	if got := h.OverrideOutbound(other, proxy); got != proxy {
		t.Fatal("another member's traffic was overridden")
	}
}

func TestProfileSwitchClosesVisibleConnections(t *testing.T) {
	h := newProfilesPortal(t)
	h.manager.SetPortal([]netip.Addr{netip.MustParseAddr("203.0.113.1")}, nil)
	route := func(host string) net.Conn {
		client, server := net.Pipe()
		t.Cleanup(func() { client.Close(); server.Close() })
		metadata := adapter.InboundContext{Inbound: testInboundTag, User: testUserName, Destination: M.ParseSocksaddrHostPort(host, 443)}
		return h.manager.RoutedConnection(context.Background(), server, metadata, nil, nil)
	}
	page := route("203.0.113.1")
	traffic := route("example.com")
	_, body := postProfiles(t, h, "/save", nodeForm("", "x.example"))
	postProfiles(t, h, "/activate", url.Values{"id": {body["id"].(string)}})
	deadline := time.Now().Add(time.Second)
	for {
		snapshot, _ := h.manager.UserConnections(testInboundTag, testUserName)
		if snapshot.ActiveCount == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("connections left open after switching: %+v", snapshot)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := traffic.Write([]byte("x")); err == nil {
		t.Fatal("traffic connection still open")
	}
	// The portal's own connection (this page) stays up.
	if n := h.manager.conns.closeAll(UserKey{testInboundTag, testUserName}, true); n != 1 {
		t.Fatalf("hidden connections = %d, want the page's one", n)
	}
	_ = page
}

func TestMigrateMemberOutbounds(t *testing.T) {
	dir := t.TempDir()
	old := memberSSConfig{InboundTag: "ix-in", UserName: "grey", Server: "4.3.2.1", ServerPort: 8888, Method: "2022-blake3-aes-128-gcm", Password: "k", Mux: true, Padding: true}
	data, _ := json.Marshal(old)
	quotaFile := filepath.Join(dir, "quota-member-ix-in-grey.quota")
	fragment := filepath.Join(dir, "00-quota-member-ix-in-grey.json")
	unrelated := filepath.Join(dir, "00-grey-youtube-direct.json")
	for path, content := range map[string][]byte{quotaFile: data, fragment: []byte("{}"), unrelated: []byte("{}")} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	migrated, err := migrateMemberOutbounds(dir)
	if err != nil || len(migrated) != 1 || migrated[0] != "ix-in/grey" {
		t.Fatalf("migrated %v, %v", migrated, err)
	}
	profiles, err := readMemberProfiles(dir, "ix-in", "grey")
	if err != nil || len(profiles.Profiles) != 1 {
		t.Fatalf("profiles = %+v, %v", profiles, err)
	}
	p := profiles.Profiles[0]
	if profiles.Active != p.ID || p.Name != "4.3.2.1" || p.Server != old.Server || p.ServerPort != 8888 || p.Method != old.Method || p.Password != "k" || !p.Mux || !p.Padding {
		t.Fatalf("migrated node = %+v, active %q", p, profiles.Active)
	}
	for _, gone := range []string{quotaFile, fragment} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed", filepath.Base(gone))
		}
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("an unrelated config fragment was removed")
	}
	if again, _ := migrateMemberOutbounds(dir); len(again) != 0 {
		t.Fatalf("second run migrated %v", again)
	}
}

func TestPostStartMigratesLoadsAndReloadsOnce(t *testing.T) {
	h := newProfilesPortal(t)
	reloaded := make(chan struct{}, 2)
	h.reload = func() { reloaded <- struct{}{} }
	data, _ := json.Marshal(memberSSConfig{InboundTag: testInboundTag, UserName: testUserName, Server: "4.3.2.1", ServerPort: 8888, Method: "aes-128-gcm", Password: "k"})
	if err := os.WriteFile(filepath.Join(h.memberOutboundConfigDirectory, memberSSFileForTest(testInboundTag, testUserName)), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.PostStart(); err != nil {
		t.Fatal(err)
	}
	if liveTag(h) != "4.3.2.1" {
		t.Fatalf("live = %q, want the migrated node in use", liveTag(h))
	}
	select {
	case <-reloaded:
	case <-time.After(3 * time.Second):
		t.Fatal("no reload after migrating")
	}
	// A later start finds nothing to migrate and doesn't reload.
	h2 := newProfilesPortal(t)
	h2.memberOutboundConfigDirectory = h.memberOutboundConfigDirectory
	h2.reload = func() { reloaded <- struct{}{} }
	if err := h2.PostStart(); err != nil || liveTag(h2) != "4.3.2.1" {
		t.Fatalf("second start: live %q, %v", liveTag(h2), err)
	}
	select {
	case <-reloaded:
		t.Fatal("reloaded without migrating anything")
	case <-time.After(1500 * time.Millisecond):
	}
}

func memberSSFileForTest(inboundTag, userName string) string {
	return "quota-member-" + safeConfigName(inboundTag) + "-" + safeConfigName(userName) + ".quota"
}

func TestPortalModernOutboundTab(t *testing.T) {
	h := newProfilesPortal(t)
	_, body := postProfiles(t, h, "/save", nodeForm("Tokyo <1>", "tokyo.example"))
	postProfiles(t, h, "/activate", url.Values{"id": {body["id"].(string)}})

	page := h.buildPage(testInboundTag, testUserName, false)
	if page.Outbound == nil || page.Outbound.Active != body["id"].(string) || len(page.Outbound.Profiles) != 1 || page.Outbound.Profiles[0].Server != "tokyo.example" || page.Outbound.Profiles[0].Name != "Tokyo <1>" {
		t.Fatalf("unexpected outbound tab: %+v", page.Outbound)
	}
	// The side column is the subscription's alone now.
	if page.Subscription != nil {
		t.Fatal("page without a subscription info still has one")
	}

	h.memberOutboundConfigDirectory = ""
	page = h.buildPage(testInboundTag, testUserName, false)
	if page.Outbound != nil {
		t.Fatal("outbound tab shown without a member outbound directory")
	}
}

// sing-box reads every .json file in the config directory, which is usually
// the member directory too, so nothing the portal writes there may end in
// .json.
func TestProfilesNeverWriteJSONFiles(t *testing.T) {
	h := newProfilesPortal(t)
	_, body := postProfiles(t, h, "/save", nodeForm("", "x.example"))
	postProfiles(t, h, "/activate", url.Values{"id": {body["id"].(string)}})
	entries, _ := os.ReadDir(h.memberOutboundConfigDirectory)
	if len(entries) == 0 {
		t.Fatal("nothing written")
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			t.Fatalf("wrote %s into the config directory", entry.Name())
		}
	}
}

func TestMigrateRenamesProfilesJSON(t *testing.T) {
	dir := t.TempDir()
	if err := writeMemberProfiles(dir, &memberProfiles{InboundTag: "ix-in", UserName: "grey", Profiles: []outboundProfile{{ID: "a", Name: "n"}}}); err != nil {
		t.Fatal(err)
	}
	name := memberProfilesName("ix-in", "grey")
	if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, name+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateMemberOutbounds(dir); err != nil {
		t.Fatal(err)
	}
	if p, err := readMemberProfiles(dir, "ix-in", "grey"); err != nil || len(p.Profiles) != 1 {
		t.Fatalf("profiles after rename = %+v, %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(dir, name+".json")); !os.IsNotExist(err) {
		t.Fatal(".profiles.json left behind")
	}
}
