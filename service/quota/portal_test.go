package quota

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
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
	card := renderSnapshot(snapshot, false)

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
