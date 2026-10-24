package quota

import (
	"context"
	"fmt"
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
	if usage.NowSlot != 5 || usage.Dates[historyDays-1].Weekday() != time.Monday {
		t.Fatalf("unexpected today: slot=%d weekday=%s", usage.NowSlot, usage.Dates[historyDays-1].Weekday())
	}
	if got := usage.Days[historyDays-1][5]; got != 300 {
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
	if usage.NowSlot != 0 || usage.Dates[historyDays-1].Weekday() != time.Tuesday || usage.Days[historyDays-1][0] != 42 {
		t.Fatalf("expected 42 bytes in Tuesday 子时, got slot=%d weekday=%s days=%v", usage.NowSlot, usage.Dates[historyDays-1].Weekday(), usage.Days[historyDays-1])
	}
}

func TestModernUIUsesBinaryUnits(t *testing.T) {
	user := newModernUser(UserSnapshot{QuotaBytes: 200 << 30, UsedBytes: 1 << 30, RemainingBytes: 199 << 30, UplinkBytes: 512 << 20, DownlinkBytes: 3 << 29}, time.Now())
	if user.RemainingGB != "199.00" || user.Quota != "200.00 GB" || user.Used != "1.00 GB" || user.Upload != "512.00 MB" || user.Download != "1.50 GB" || user.Ticks[4] != "200 GB" {
		t.Fatalf("unexpected sizes: %+v", user)
	}
}

func addTestHistory(t *testing.T, store *HistoryStore, at time.Time, bytes int64) {
	t.Helper()
	store.lastFlush = at.Add(-time.Minute)
	_, downlink := store.Counters(HistoryDimensions{InboundTag: testInboundTag, UserName: testUserName})
	downlink.Add(bytes)
	if err := store.Flush(at); err != nil {
		t.Fatal(err)
	}
}

// newTestDashboard has ten days of 1 GB at 20:00 BJT and 300 MB so far today,
// with 100 GB monthly quota resetting on the 1st.
func newTestDashboard(t *testing.T) (*portalOutbound, time.Time) {
	t.Helper()
	store, err := OpenHistory(filepath.Join(t.TempDir(), "history.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	manager := newTestPortalManager()
	manager.SetHistory(store)
	manager.SetResetDay(1)
	manager.userState(testInboundTag, testUserName).QuotaBytes = 100 * gib
	now := bjt(2026, 10, 22, 14, 30)
	for day := 10; day >= 1; day-- {
		addTestHistory(t, store, bjt(2026, 10, 22-day, 20, 10), gib)
	}
	addTestHistory(t, store, bjt(2026, 10, 22, 9, 10), 300*mib)
	return &portalOutbound{ctx: context.Background(), manager: manager}, now
}

func TestPortalModernDashboard(t *testing.T) {
	h, now := newTestDashboard(t)
	page := h.renderModernAt(testInboundTag, testUserName, false, now)
	for _, want := range []string{
		// Nov 1 is 9.4 days away.
		`"reset":{"m":11,"d":1,"left":10}`,
		`class="fc-tag fc-m ok"`, `class="ruler ok"`,
		// 100 GB over 10 days is 10 GB a day, no day above it.
		`<span data-i18n="budgetLabel">每天可用</span> <b>10.00 GB</b>`, `"over":0`,
		// The heatmap's insight: 戌时 (19:00–21:00) carries all of it.
		`"insight":{"lo":10,"hi":10,"range":"19:00–21:00","share":100}`,
		`<b>300 MB</b>`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("expected dashboard to contain %q, got:\n%s", want, page)
		}
	}
	if got := strings.Count(page, `<div class="bar `); got != 11 {
		t.Fatalf("expected ten past days and today as bars, got %d", got)
	}
	// 300 MB by 巳时 against 0 on previous days at that time is not comparable.
	if !strings.Contains(page, `"vs":null`) {
		t.Fatal("expected no comparison with usual usage")
	}
	if strings.Contains(page, "ZgotmplZ") {
		t.Fatal("unexpected template sanitizing")
	}
}

func TestPortalModernDailyMarksDaysOverBudget(t *testing.T) {
	h, now := newTestDashboard(t)
	h.manager.userState(testInboundTag, testUserName).Uplink.Store(95 * gib)
	page := h.renderModernAt(testInboundTag, testUserName, false, now)
	// About 0.5 GB a day remains for 10 days; every past 1 GB day is over.
	for _, want := range []string{`"over":10`, `class="bar hot"`, `class="ruler err"`, `class="fc-tag err"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("expected %q, got:\n%s", want, page)
		}
	}
}

func TestPortalModernWithoutResetDayOmitsForecast(t *testing.T) {
	h, now := newTestDashboard(t)
	h.manager.SetResetDay(0)
	page := h.renderModernAt(testInboundTag, testUserName, false, now)
	if strings.Contains(page, `class="fc-tag`) || strings.Contains(page, `class="bl budget"`) || !strings.Contains(page, `"reset":null`) {
		t.Fatal("expected no forecast, budget or reset date without reset_day")
	}
	if !strings.Contains(page, `id="bars"`) {
		t.Fatal("expected the daily chart from history")
	}
}

func TestPortalModernCopyLinkKeepsSubscriptionURI(t *testing.T) {
	manager := newTestPortalManager()
	state := manager.userState(testInboundTag, testUserName)
	state.Method, state.UserPassword, state.SubscriptionServer, state.SubscriptionPort = "aes-128-gcm", "secret", "example.com", 8388
	page := (&portalOutbound{manager: manager}).renderModern(testInboundTag, testUserName, false)
	if !strings.Contains(page, `data-link="ss://`) {
		t.Fatalf("expected the copy button to carry the ss:// link, got:\n%s", page)
	}
}

func TestPortalOutboundEndpointsAnswerJSON(t *testing.T) {
	h := &portalOutbound{manager: newTestPortalManager(), memberOutboundConfigDirectory: t.TempDir()}
	form := "server=&server_port=8388&method=aes-128-gcm&password=secret"
	for _, path := range []string{"/outbound/shadowsocks", "/outbound/shadowsocks/test"} {
		request := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: quota.local\r\nAccept: application/json\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: %d\r\n\r\n%s", path, len(form), form)
		response := servePortalRequest(t, h, testInboundTag, testUserName, request)
		body := readResponseBody(t, response)
		if response.StatusCode != http.StatusBadRequest || response.Header.Get("Content-Type") != "application/json; charset=utf-8" || !strings.Contains(body, `"error":"Invalid Shadowsocks outbound"`) {
			t.Fatalf("%s: expected JSON error, got %s %q", path, response.Status, body)
		}
	}
	request := "POST /outbound/delete HTTP/1.1\r\nHost: quota.local\r\nAccept: application/json\r\nContent-Length: 0\r\n\r\n"
	response := servePortalRequest(t, h, testInboundTag, testUserName, request)
	if body := readResponseBody(t, response); response.StatusCode != http.StatusOK || body != `{"ok":true}` {
		t.Fatalf("expected JSON delete result, got %s %q", response.Status, body)
	}
}
