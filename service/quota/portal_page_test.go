package quota

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// inlinedPageData reads the page data the portal inlines into the
// dashboard.
func inlinedPageData(t *testing.T, html string) map[string]any {
	t.Helper()
	const open = `<script type="application/json" id="qd">`
	start := strings.Index(html, open)
	if start < 0 {
		t.Fatalf("no page data in:\n%.500s", html)
	}
	rest := html[start+len(open):]
	end := strings.Index(rest, "</script>")
	var data map[string]any
	if err := json.Unmarshal([]byte(rest[:end]), &data); err != nil {
		t.Fatalf("page data: %v in %q", err, rest[:end])
	}
	return data
}

func TestPortalServesDashboardWithData(t *testing.T) {
	dir := t.TempDir()
	h := &portalOutbound{manager: newTestPortalManager(), memberOutboundConfigDirectory: dir}
	response := servePortalRequest(t, h, testInboundTag, testUserName, "GET / HTTP/1.1\r\nHost: quota.local\r\n\r\n")
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("unexpected response: %s %s", response.Status, response.Header.Get("Content-Type"))
	}
	page := readResponseBody(t, response)
	if strings.Contains(page, pageDataMarker) {
		t.Fatal("page data placeholder left in the page")
	}
	data := inlinedPageData(t, page)
	self, _ := data["self"].(map[string]any)
	if self["name"] != testUserName || self["tag"] != testInboundTag {
		t.Fatalf("unexpected self: %v", data["self"])
	}
	outbound, _ := data["outbound"].(map[string]any)
	methods, _ := outbound["methods"].([]any)
	if len(methods) != len(shadowsocksMethods) || outbound["active"] != "" {
		t.Fatalf("unexpected outbound tab: %v", data["outbound"])
	}
	if data["users"] != nil {
		t.Fatalf("member page lists users: %v", data["users"])
	}
}

func TestPortalPageAPI(t *testing.T) {
	h := &portalOutbound{manager: newTestPortalManager()}
	response := servePortalRequest(t, h, testInboundTag, testUserName, "GET "+pageAPIPath+" HTTP/1.1\r\nHost: quota.local\r\n\r\n")
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("unexpected response: %s %s", response.Status, response.Header.Get("Content-Type"))
	}
	var page pageData
	if err := json.Unmarshal([]byte(readResponseBody(t, response)), &page); err != nil {
		t.Fatal(err)
	}
	if page.Self == nil || page.Self.UserName != testUserName || page.Self.UsedPct != "70.0" {
		t.Fatalf("unexpected page: %+v", page.Self)
	}
	response = servePortalRequest(t, h, "", "", "GET "+pageAPIPath+" HTTP/1.1\r\nHost: quota.local\r\nAccept: application/json\r\n\r\n")
	if response.StatusCode != http.StatusForbidden || !strings.Contains(readResponseBody(t, response), `"error"`) {
		t.Fatalf("expected a JSON 403 without an inbound, got %s", response.Status)
	}
}

// The data sits in a script element; nothing in it may close the element.
func TestPortalPageDataCannotCloseScript(t *testing.T) {
	page := renderPage(pageData{Title: "</script><script>alert(1)</script>"})
	if strings.Count(page, "<script>alert(1)") != 0 {
		t.Fatal("page data escaped its script element")
	}
	if inlinedPageData(t, page)["title"] != "</script><script>alert(1)</script>" {
		t.Fatal("title lost in escaping")
	}
}

func TestPortalAdminPageListsUsers(t *testing.T) {
	h := &portalOutbound{manager: newTestPortalManager()}
	page := h.buildPage(testAdminInboundTag, testAdminUserName, true)
	var found bool
	for _, u := range page.Users {
		if u.InboundTag == testInboundTag && u.UserName == testUserName {
			found = true
		}
	}
	if !found {
		t.Fatalf("admin page lacks %s/%s: %+v", testInboundTag, testUserName, page.Users)
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
	page := h.buildPage(testInboundTag, testUserName, false)
	if page.Heat != nil || page.Daily != nil {
		t.Fatal("expected no charts without a history store")
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
	user := newPageUser(UserSnapshot{QuotaBytes: 200 << 30, UsedBytes: 1 << 30, RemainingBytes: 199 << 30, UplinkBytes: 512 << 20, DownlinkBytes: 3 << 29}, time.Now())
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
	page := h.buildPageAt(testInboundTag, testUserName, false, now)
	// Nov 1 is 9.4 days away.
	if page.Reset == nil || *page.Reset != (pageReset{Month: 11, Day: 1, DaysLeft: 10}) {
		t.Fatalf("unexpected reset: %+v", page.Reset)
	}
	if page.Forecast == nil || page.Forecast.Tone != "ok" || page.Self.Tone != "ok" {
		t.Fatalf("expected an ok forecast: %+v", page.Forecast)
	}
	// 1 GB a day over the 9 days left leaves 91 GB of the 99.7 GB.
	if page.Forecast.Left != "91 GB" || page.Forecast.Range != "91 GB" {
		t.Fatalf("unexpected forecast: %+v", page.Forecast)
	}
	// 100 GB over 10 days is 10 GB a day, no day above it.
	if page.Daily == nil || page.Daily.Budget != "10.0 GB" || page.Daily.Over != 0 || page.Daily.Today != "300 MB" {
		t.Fatalf("unexpected daily chart: %+v", page.Daily)
	}
	if len(page.Daily.Bars) != 11 {
		t.Fatalf("expected ten past days and today as bars, got %d", len(page.Daily.Bars))
	}
	// The heatmap's insight: 戌时 (19:00–21:00) carries all of it.
	if page.Heat == nil || page.Heat.Insight == nil || *page.Heat.Insight != (pageInsight{Lo: 10, Hi: 10, Range: "19:00–21:00", Share: 100}) {
		t.Fatalf("unexpected insight: %+v", page.Heat)
	}
	// 300 MB by 巳时 against 0 on previous days at that time is not comparable.
	if page.VsUsual != nil {
		t.Fatalf("expected no comparison with usual usage, got %d", *page.VsUsual)
	}
}

func TestPortalModernDailyMarksDaysOverBudget(t *testing.T) {
	h, now := newTestDashboard(t)
	h.manager.userState(testInboundTag, testUserName).Uplink.Store(95 * gib)
	page := h.buildPageAt(testInboundTag, testUserName, false, now)
	// About 0.5 GB a day remains for 10 days; every past 1 GB day is over.
	if page.Daily.Over != 10 || !page.Daily.Bars[0].Hot || page.Daily.Bars[len(page.Daily.Bars)-1].Hot {
		t.Fatalf("expected the ten past days over budget: %+v", page.Daily)
	}
	if page.Self.Tone != "err" || page.Forecast.Tone != "err" {
		t.Fatalf("expected the forecast to warn: %+v", page.Forecast)
	}
}

func TestPortalModernWithoutResetDayOmitsForecast(t *testing.T) {
	h, now := newTestDashboard(t)
	h.manager.SetResetDay(0)
	page := h.buildPageAt(testInboundTag, testUserName, false, now)
	if page.Forecast != nil || page.Daily.Budget != "" || page.Reset != nil {
		t.Fatal("expected no forecast, budget or reset date without reset_day")
	}
	if page.Daily == nil {
		t.Fatal("expected the daily chart from history")
	}
}

func TestPortalModernSubscription(t *testing.T) {
	manager := newTestPortalManager()
	state := manager.userState(testInboundTag, testUserName)
	state.Method, state.UserPassword, state.SubscriptionServer, state.SubscriptionPort = "aes-128-gcm", "secret", "example.com", 8388
	page := (&portalOutbound{manager: manager}).buildPage(testInboundTag, testUserName, false)
	if page.Subscription == nil || !strings.HasPrefix(page.Subscription.URI, "ss://") || !strings.HasPrefix(page.Subscription.QR, "data:image/png;base64,") {
		t.Fatalf("expected the ss:// link and its QR code, got %+v", page.Subscription)
	}
}

func TestFormatApprox(t *testing.T) {
	for _, c := range []struct {
		lo, hi int64
		want   string
	}{
		{0, 0, "0–0"},
		{300 * mib, 800 * mib, "300 MB–800 MB"},
		{0, 3 * gib, "0–3.0 GB"},
		{500 * mib, 3 * gib, "500 MB – 3.0 GB"},
		{gib * 31 / 10, gib * 54 / 10, "3.1–5.4 GB"},
		{gib * 94 / 10, 12 * gib, "9.4–12 GB"},
		{gib * 7302 / 100, gib * 7298 / 100, "73 GB"},
	} {
		if got := formatApproxRange(c.lo, c.hi); got != c.want {
			t.Errorf("formatApproxRange(%d, %d) = %q, want %q", c.lo, c.hi, got, c.want)
		}
	}
	for b, want := range map[int64]string{gib * 826 / 100: "8.3 GB", gib * 7294 / 100: "73 GB", 348 * mib: "348 MB"} {
		if got := formatApprox(b); got != want {
			t.Errorf("formatApprox(%d) = %q, want %q", b, got, want)
		}
	}
}

func TestPortalServesDashboardFiles(t *testing.T) {
	h := &portalOutbound{manager: newTestPortalManager()}
	for file, contentType := range map[string]string{
		"/fonts/archivo-latin.woff2": "font/woff2",
		"/favicon.ico":               "image/x-icon",
		"/apple-touch-icon.png":      "image/png",
		"/manifest.webmanifest":      "application/manifest+json",
	} {
		// Files are public: a page outside the tunnel still loads its icon.
		response := servePortalRequest(t, h, "", "", "GET "+file+" HTTP/1.1\r\nHost: quota.local\r\n\r\n")
		body := readResponseBody(t, response)
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != contentType || len(body) == 0 {
			t.Fatalf("%s: %s %s, %d bytes", file, response.Status, response.Header.Get("Content-Type"), len(body))
		}
	}
	// The page itself and paths outside ui are never served as files.
	for _, file := range []string{"/index.html", "/../portal.go", "/fonts/../../portal_page.go", "/missing.png"} {
		response := servePortalRequest(t, h, testInboundTag, testUserName, "GET "+file+" HTTP/1.1\r\nHost: quota.local\r\n\r\n")
		if body := readResponseBody(t, response); !strings.Contains(body, `id="qd"`) {
			t.Fatalf("%s: expected the dashboard, got %s", file, response.Status)
		}
	}
}
