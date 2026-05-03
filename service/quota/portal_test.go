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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestPortalManager() *Manager {
	member := &InboundState{Tag: "member", QuotaBytes: 100}
	member.Uplink.Store(40)
	member.Downlink.Store(30)
	return &Manager{inbounds: map[string]*InboundState{"member": member}}
}

func servePortalRequest(t *testing.T, h *portalOutbound, inboundTag, request string) *http.Response {
	t.Helper()
	client, server := net.Pipe()
	go h.serveHTTP(server, inboundTag)
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

	response := servePortalRequest(t, h, "admin", "POST /quota/reset?tag=member HTTP/1.1\r\nHost: quota.local\r\nContent-Length: 0\r\n\r\n")

	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect after reset, got %s", response.Status)
	}
	snapshot, ok := manager.Snapshot("member")
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

	response := servePortalRequest(t, h, "member", "POST /quota/reset?tag=member HTTP/1.1\r\nHost: quota.local\r\nContent-Length: 0\r\n\r\n")

	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected forbidden for member reset, got %s", response.Status)
	}
	snapshot, ok := manager.Snapshot("member")
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
		`Reset quota`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("expected admin page to contain %q, got:\n%s", want, page)
		}
	}
}

func TestPortalMemberPageDoesNotRenderResetControls(t *testing.T) {
	manager := newTestPortalManager()
	snapshot, _ := manager.Snapshot("member")
	card := renderSnapshot(snapshot, false, false)

	if strings.Contains(card, "Reset quota") || strings.Contains(card, "<form") {
		t.Fatalf("member card should not include reset controls:\n%s", card)
	}
}

func TestPortalResetRejectsMissingMember(t *testing.T) {
	manager := newTestPortalManager()
	h := &portalOutbound{manager: manager}

	response := servePortalRequest(t, h, "admin", fmt.Sprintf("POST /quota/reset?tag=%s HTTP/1.1\r\nHost: quota.local\r\nContent-Length: 0\r\n\r\n", "missing"))

	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("expected not found for missing member, got %s", response.Status)
	}
}

func TestPortalMemberCanSaveShadowsocksOutboundFragment(t *testing.T) {
	dir := t.TempDir()
	manager := newTestPortalManager()
	h := &portalOutbound{
		manager:                          manager,
		memberOutboundConfigDirectory:    dir,
		memberOutboundConfigChecker:      func(string) error { return nil },
		memberOutboundConnectivityTester: func(context.Context, string, string, int, string, string) (uint16, error) { return 123, nil },
	}
	form := "server=1.2.3.4&server_port=8388&method=chacha20-ietf-poly1305&password=secret"
	request := fmt.Sprintf("POST /outbound/shadowsocks HTTP/1.1\r\nHost: quota.local\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s", len(form), form)

	response := servePortalRequest(t, h, "member", request)

	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected success page after saving outbound, got %s", response.Status)
	}
	body := readResponseBody(t, response)
	if !strings.Contains(body, "Saved Shadowsocks outbound") || !strings.Contains(body, "123 ms") {
		t.Fatalf("expected success page with delay, got:\n%s", body)
	}
	content, err := os.ReadFile(filepath.Join(dir, "40-quota-member-member.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fragment struct {
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
	}
	if err := json.Unmarshal(content, &fragment); err != nil {
		t.Fatal(err)
	}
	if len(fragment.Outbounds) != 1 {
		t.Fatalf("expected one outbound, got %d", len(fragment.Outbounds))
	}
	outbound := fragment.Outbounds[0]
	if outbound.Type != "shadowsocks" || outbound.Tag != "quota-member-custom-out" || outbound.Server != "1.2.3.4" || outbound.ServerPort != 8388 || outbound.Method != "chacha20-ietf-poly1305" || outbound.Password != "secret" {
		t.Fatalf("unexpected outbound: %+v", outbound)
	}
	if len(fragment.Route.Rules) != 1 || len(fragment.Route.Rules[0].Inbound) != 1 || fragment.Route.Rules[0].Inbound[0] != "member" || fragment.Route.Rules[0].Outbound != "quota-member-custom-out" {
		t.Fatalf("unexpected route rules: %+v", fragment.Route.Rules)
	}
}

func TestPortalMemberPageRendersShadowsocksOutboundForm(t *testing.T) {
	manager := newTestPortalManager()
	snapshot, _ := manager.Snapshot("member")
	card := renderSnapshot(snapshot, false, true)

	for _, want := range []string{
		`<form method="post" action="/outbound/shadowsocks">`,
		`name="server"`,
		`name="server_port"`,
		`name="method"`,
		`name="password"`,
		`Save Shadowsocks outbound`,
	} {
		if !strings.Contains(card, want) {
			t.Fatalf("expected member card to contain %q, got:\n%s", want, card)
		}
	}
}

func TestPortalMemberOutboundSaveRunsConfigCheckBeforeWritingFinalFragment(t *testing.T) {
	dir := t.TempDir()
	manager := newTestPortalManager()
	checkerCalled := false
	connectivityCalled := false
	h := &portalOutbound{
		manager:                       manager,
		memberOutboundConfigDirectory: dir,
		memberOutboundConnectivityTester: func(ctx context.Context, inboundTag, server string, serverPort int, method, password string) (uint16, error) {
			connectivityCalled = true
			if inboundTag != "member" || server != "1.2.3.4" || serverPort != 8388 || method != "chacha20-ietf-poly1305" || password != "secret" {
				t.Fatalf("unexpected connectivity test arguments: %s %s %d %s %s", inboundTag, server, serverPort, method, password)
			}
			if _, err := os.Stat(filepath.Join(dir, "40-quota-member-member.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("final fragment should not exist before successful connectivity test, stat error: %v", err)
			}
			return 123, nil
		},
		memberOutboundConfigChecker: func(checkDir string) error {
			checkerCalled = true
			if checkDir == dir {
				t.Fatal("expected checker to run against a temporary validation directory, not the live directory")
			}
			if _, err := os.Stat(filepath.Join(checkDir, "40-quota-member-member.json")); err != nil {
				t.Fatalf("checker should see candidate fragment in validation directory: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "40-quota-member-member.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("final fragment should not exist before successful check, stat error: %v", err)
			}
			return nil
		},
	}
	form := "server=1.2.3.4&server_port=8388&method=chacha20-ietf-poly1305&password=secret"
	request := fmt.Sprintf("POST /outbound/shadowsocks HTTP/1.1\r\nHost: quota.local\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s", len(form), form)

	response := servePortalRequest(t, h, "member", request)

	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected success page after saving outbound, got %s", response.Status)
	}
	if !checkerCalled {
		t.Fatal("expected config checker to be called")
	}
	if !connectivityCalled {
		t.Fatal("expected connectivity tester to be called")
	}
	if _, err := os.Stat(filepath.Join(dir, "40-quota-member-member.json")); err != nil {
		t.Fatalf("expected final fragment after successful check: %v", err)
	}
}

func TestPortalMemberOutboundConnectivityFailureDoesNotOverwriteExistingFragment(t *testing.T) {
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "40-quota-member-member.json")
	oldContent := []byte("{\"outbounds\":[]}")
	if err := os.WriteFile(finalPath, oldContent, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := newTestPortalManager()
	h := &portalOutbound{
		manager:                       manager,
		memberOutboundConfigDirectory: dir,
		memberOutboundConfigChecker:   func(string) error { return nil },
		memberOutboundConnectivityTester: func(ctx context.Context, inboundTag, server string, serverPort int, method, password string) (uint16, error) {
			return 0, errors.New("urltest failed")
		},
	}
	form := "server=1.2.3.4&server_port=8388&method=chacha20-ietf-poly1305&password=secret"
	request := fmt.Sprintf("POST /outbound/shadowsocks HTTP/1.1\r\nHost: quota.local\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s", len(form), form)

	response := servePortalRequest(t, h, "member", request)

	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected bad request when connectivity test fails, got %s", response.Status)
	}
	body := readResponseBody(t, response)
	if !strings.Contains(body, "urltest failed") {
		t.Fatalf("expected connectivity error details in response, got:\n%s", body)
	}
	content, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(oldContent) {
		t.Fatalf("expected old fragment to remain unchanged, got %s", content)
	}
}

func TestPortalMemberOutboundCheckFailureDoesNotOverwriteExistingFragment(t *testing.T) {
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "40-quota-member-member.json")
	oldContent := []byte("{\"outbounds\":[]}")
	if err := os.WriteFile(finalPath, oldContent, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := newTestPortalManager()
	h := &portalOutbound{
		manager:                       manager,
		memberOutboundConfigDirectory: dir,
		memberOutboundConfigChecker: func(checkDir string) error {
			return errors.New("sing-box check failed")
		},
	}
	form := "server=1.2.3.4&server_port=8388&method=2022-blake3-aes-128-gcm&password=bad"
	request := fmt.Sprintf("POST /outbound/shadowsocks HTTP/1.1\r\nHost: quota.local\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s", len(form), form)

	response := servePortalRequest(t, h, "member", request)

	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected bad request when generated config fails check, got %s", response.Status)
	}
	body := readResponseBody(t, response)
	if !strings.Contains(body, "sing-box check failed") {
		t.Fatalf("expected check error details in response, got:\n%s", body)
	}
	content, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(oldContent) {
		t.Fatalf("expected old fragment to remain unchanged, got %s", content)
	}
}

func TestPortalMemberCanTestShadowsocksOutboundWithoutSaving(t *testing.T) {
	dir := t.TempDir()
	manager := newTestPortalManager()
	h := &portalOutbound{
		manager:                          manager,
		memberOutboundConfigDirectory:    dir,
		memberOutboundConfigChecker:      func(string) error { return nil },
		memberOutboundConnectivityTester: func(context.Context, string, string, int, string, string) (uint16, error) { return 321, nil },
	}
	form := "server=1.2.3.4&server_port=8388&method=chacha20-ietf-poly1305&password=secret"
	request := fmt.Sprintf("POST /outbound/shadowsocks/test HTTP/1.1\r\nHost: quota.local\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s", len(form), form)

	response := servePortalRequest(t, h, "member", request)

	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected successful test response, got %s", response.Status)
	}
	body := readResponseBody(t, response)
	if !strings.Contains(body, "Connection test passed") || !strings.Contains(body, "321 ms") {
		t.Fatalf("expected test result with delay, got:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "40-quota-member-member.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("test-only request should not write final fragment, stat error: %v", err)
	}
}

func TestPortalMemberCanDeleteCustomOutboundAfterConfigCheck(t *testing.T) {
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "40-quota-member-member.json")
	if err := os.WriteFile(finalPath, []byte("{\"outbounds\":[]}"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := newTestPortalManager()
	checkerCalled := false
	h := &portalOutbound{
		manager:                       manager,
		memberOutboundConfigDirectory: dir,
		memberOutboundConfigChecker: func(checkDir string) error {
			checkerCalled = true
			if _, err := os.Stat(filepath.Join(checkDir, "40-quota-member-member.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("candidate delete check should not include member fragment, stat error: %v", err)
			}
			if _, err := os.Stat(finalPath); err != nil {
				t.Fatalf("live fragment should still exist during check: %v", err)
			}
			return nil
		},
	}
	request := "POST /outbound/delete HTTP/1.1\r\nHost: quota.local\r\nContent-Length: 0\r\n\r\n"

	response := servePortalRequest(t, h, "member", request)

	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected delete success, got %s", response.Status)
	}
	if !checkerCalled {
		t.Fatal("expected config checker before delete")
	}
	if _, err := os.Stat(finalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected member fragment deleted, stat error: %v", err)
	}
}

func TestPortalMemberDeleteCheckFailureKeepsExistingFragment(t *testing.T) {
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "40-quota-member-member.json")
	oldContent := []byte("{\"outbounds\":[]}")
	if err := os.WriteFile(finalPath, oldContent, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := newTestPortalManager()
	h := &portalOutbound{
		manager:                       manager,
		memberOutboundConfigDirectory: dir,
		memberOutboundConfigChecker:   func(string) error { return errors.New("delete check failed") },
	}
	request := "POST /outbound/delete HTTP/1.1\r\nHost: quota.local\r\nContent-Length: 0\r\n\r\n"

	response := servePortalRequest(t, h, "member", request)

	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected bad request on delete check failure, got %s", response.Status)
	}
	body := readResponseBody(t, response)
	if !strings.Contains(body, "delete check failed") {
		t.Fatalf("expected delete error details, got:\n%s", body)
	}
	content, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(oldContent) {
		t.Fatalf("expected old fragment unchanged, got %s", content)
	}
}
