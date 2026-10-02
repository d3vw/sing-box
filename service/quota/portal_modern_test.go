package quota

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPortalUISwitchSetsCookie(t *testing.T) {
	h := &portalOutbound{manager: newTestPortalManager()}
	response := servePortalRequest(t, h, testInboundTag, testUserName, "GET /?ui=classic HTTP/1.1\r\nHost: quota.local\r\n\r\n")
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/" {
		t.Fatalf("expected redirect to /, got %s %q", response.Status, response.Header.Get("Location"))
	}
	if !strings.Contains(response.Header.Get("Set-Cookie"), portalUICookie+"=classic") {
		t.Fatalf("expected ui cookie, got %q", response.Header.Get("Set-Cookie"))
	}
}

func TestPortalDefaultsToModernUI(t *testing.T) {
	dir := t.TempDir()
	h := &portalOutbound{manager: newTestPortalManager(), memberOutboundConfigDirectory: dir}
	page := readResponseBody(t, servePortalRequest(t, h, testInboundTag, testUserName, "GET / HTTP/1.1\r\nHost: quota.local\r\n\r\n"))
	for _, want := range []string{`href="/?ui=classic"`, `action="/outbound/shadowsocks"`, testUserName} {
		if !strings.Contains(page, want) {
			t.Fatalf("expected modern page to contain %q, got:\n%s", want, page)
		}
	}
	for _, unwanted := range []string{`action="/quota/reset"`, "ZgotmplZ"} {
		if strings.Contains(page, unwanted) {
			t.Fatalf("modern member page should not contain %q", unwanted)
		}
	}
}

func TestPortalClassicUICookie(t *testing.T) {
	h := &portalOutbound{manager: newTestPortalManager()}
	page := readResponseBody(t, servePortalRequest(t, h, testInboundTag, testUserName, "GET / HTTP/1.1\r\nHost: quota.local\r\nCookie: quota_ui=classic\r\n\r\n"))
	if !strings.Contains(page, `href="/?ui=modern"`) || !strings.Contains(page, `class="bar-track"`) {
		t.Fatalf("expected classic UI with classic cookie, got:\n%s", page)
	}
}

func TestPortalModernAdminPageListsUsersWithReset(t *testing.T) {
	h := &portalOutbound{manager: newTestPortalManager()}
	page := readResponseBody(t, servePortalRequest(t, h, testAdminInboundTag, testAdminUserName, "GET / HTTP/1.1\r\nHost: quota.local\r\n\r\n"))
	for _, want := range []string{`action="/quota/reset"`, `name="tag" value="member"`, `name="name" value="testuser"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("expected modern admin page to contain %q, got:\n%s", want, page)
		}
	}
}

func TestPortalModernHeatmapReadsHistoryInShichen(t *testing.T) {
	store, err := OpenHistory(filepath.Join(t.TempDir(), "history.db"), 30)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager := newTestPortalManager()
	manager.SetHistory(store)
	// 2026-10-12 02:30 UTC is 10:30 BJT on a Monday.
	now := time.Date(2026, 10, 12, 2, 30, 0, 0, time.UTC)
	store.lastFlush = now.Add(-time.Minute)
	uplink, downlink := store.Counters(HistoryDimensions{InboundTag: testInboundTag, UserName: testUserName, Target: "example.com"})
	uplink.Add(100)
	downlink.Add(200)
	if err = store.Flush(now); err != nil {
		t.Fatal(err)
	}
	other, _ := store.Counters(HistoryDimensions{InboundTag: testAdminInboundTag, UserName: testAdminUserName})
	other.Add(999)
	if err = store.Flush(now); err != nil {
		t.Fatal(err)
	}

	h := &portalOutbound{ctx: context.Background(), manager: manager}
	usage, ok := h.shichenUsage(testInboundTag, testUserName, now)
	if !ok {
		t.Fatal("expected shichen usage")
	}
	// 10:30 falls in 巳时 (09:00–11:00), slot 5.
	if usage.NowSlot != 5 || usage.Weekdays[heatmapDays-1] != time.Monday {
		t.Fatalf("unexpected today: slot=%d weekday=%s", usage.NowSlot, usage.Weekdays[heatmapDays-1])
	}
	if got := usage.Days[heatmapDays-1][5]; got != 300 {
		t.Fatalf("expected 300 bytes in 巳时 today, got %d", got)
	}
}

func TestPortalModernWithoutHistoryOmitsHeatmap(t *testing.T) {
	h := &portalOutbound{manager: newTestPortalManager()}
	page := h.renderModern(testInboundTag, testUserName, false)
	if strings.Contains(page, `id="hm-grid"`) {
		t.Fatal("expected no heatmap without a history store")
	}
}

func TestPortalModernHeatmapStartsDayAtZiShi(t *testing.T) {
	store, err := OpenHistory(filepath.Join(t.TempDir(), "history.db"), 30)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager := newTestPortalManager()
	manager.SetHistory(store)
	// 2026-10-12 15:30 UTC is Monday 23:30 BJT, which is already Tuesday's 子时.
	now := time.Date(2026, 10, 12, 15, 30, 0, 0, time.UTC)
	store.lastFlush = now.Add(-time.Minute)
	uplink, _ := store.Counters(HistoryDimensions{InboundTag: testInboundTag, UserName: testUserName})
	uplink.Add(42)
	if err = store.Flush(now); err != nil {
		t.Fatal(err)
	}
	h := &portalOutbound{ctx: context.Background(), manager: manager}
	usage, ok := h.shichenUsage(testInboundTag, testUserName, now)
	if !ok {
		t.Fatal("expected shichen usage")
	}
	if usage.NowSlot != 0 || usage.Weekdays[heatmapDays-1] != time.Tuesday || usage.Days[heatmapDays-1][0] != 42 {
		t.Fatalf("expected 42 bytes in Tuesday 子时, got slot=%d weekday=%s days=%v", usage.NowSlot, usage.Weekdays[heatmapDays-1], usage.Days[heatmapDays-1])
	}
}

func TestModernUIUsesBinaryUnits(t *testing.T) {
	user := newModernUser(UserSnapshot{QuotaBytes: 200 << 30, UsedBytes: 1 << 30, RemainingBytes: 199 << 30, UplinkBytes: 512 << 20, DownlinkBytes: 3 << 29})
	if user.RemainingGB != "199.00" || user.Quota != "200.00 GB" || user.Used != "1.00 GB" || user.Upload != "512.00 MB" || user.Download != "1.50 GB" || user.Ticks[4] != "200 GB" {
		t.Fatalf("unexpected sizes: %+v", user)
	}
}
