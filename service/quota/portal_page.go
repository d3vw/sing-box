package quota

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
)

// The dashboard is a Svelte app (./web) built into one page, ui/index.html,
// beside the files it links to (the number font, icons and the manifest).
// The portal serves the page with the page data inlined for the first
// paint, and the app refreshes the data from pageAPIPath.
//
//go:embed ui
var portalUI embed.FS

var portalPageHTML = func() string {
	page, err := portalUI.ReadFile("ui/index.html")
	if err != nil {
		panic(err)
	}
	return string(page)
}()

// portalAssetTypes are the files beside the page the portal serves, by
// extension.
var portalAssetTypes = map[string]string{
	".woff2":       "font/woff2",
	".png":         "image/png",
	".ico":         "image/x-icon",
	".svg":         "image/svg+xml",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
}

// portalAsset returns a file beside the page by its URL path.
func portalAsset(urlPath string) ([]byte, string, bool) {
	name := path.Clean(urlPath)
	contentType, ok := portalAssetTypes[path.Ext(name)]
	if !ok || !strings.HasPrefix(name, "/") || name == "/index.html" {
		return nil, "", false
	}
	content, err := portalUI.ReadFile("ui" + name)
	if err != nil {
		return nil, "", false
	}
	return content, contentType, true
}

const (
	pageAPIPath = "/quota/api/page"
	// pageDataMarker is the placeholder in ui/index.html the page data
	// replaces; the app fetches the data instead while it is left in place.
	pageDataMarker = `"__QUOTA_PAGE_DATA__"`
)

// shadowsocksMethods are offered in the dashboard's cipher picker.
var shadowsocksMethods = []string{
	"2022-blake3-aes-128-gcm",
	"2022-blake3-aes-256-gcm",
	"2022-blake3-chacha20-poly1305",
	"aes-128-gcm",
	"aes-192-gcm",
	"aes-256-gcm",
	"chacha20-ietf-poly1305",
	"xchacha20-ietf-poly1305",
}

// pageData is everything the dashboard shows. Sizes come formatted, so the
// page and the tests agree on units; the page words the sentences in
// either language from the numbers.
type pageData struct {
	Title    string        `json:"title"`
	Self     *pageUser     `json:"self"`
	Reset    *pageReset    `json:"reset"`
	Expiry   *pageExpiry   `json:"expiry"`
	Forecast *pageForecast `json:"forecast"`
	Daily    *pageDaily    `json:"daily"`
	Heat     *pageHeat     `json:"heat"`
	// VsUsual is how much more (positive) or less today's usage so far is
	// than the same time on previous days, in percent.
	VsUsual      *int              `json:"vs"`
	Subscription *pageSubscription `json:"subscription"`
	// Outbound is the member's nodes, for the outbound tab; nil without a
	// member outbound directory.
	Outbound *pageOutbound `json:"outbound"`
	// Conns is the first paint of the connections tab; the page then
	// follows it over connectionsPath.
	Conns *pageConns `json:"conns"`
	// Users lists everyone for admins.
	Users []pageUser `json:"users"`
}

type pageUser struct {
	InboundTag  string `json:"tag"`
	UserName    string `json:"name"`
	Blocked     bool   `json:"blocked"`
	RemainingGB string `json:"remain"`
	Upload      string `json:"up"`
	Download    string `json:"down"`
	Used        string `json:"used"`
	Quota       string `json:"quota"`
	UsedPct     string `json:"usedPct"`
	// Pointer is where the ruler points, in percent of the quota, and
	// UploadShare the part of it that is upload.
	Pointer     float64   `json:"p"`
	UploadShare float64   `json:"pu"`
	Ticks       [5]string `json:"ticks"`
	// Tone colors the ruler pointer after the forecast: ok, warn or err.
	Tone string `json:"tone"`
	// ValidUntil is the last day of the service period (YYYY-MM-DD), empty
	// without one; Expired is set once it has passed.
	ValidUntil string `json:"validUntil,omitempty"`
	Expired    bool   `json:"expired,omitempty"`
	// Conns counts the user's open connections.
	Conns int `json:"conns"`
}

type pageConns struct {
	Active int   `json:"active"`
	Closed int64 `json:"closed"`
}

type pageReset struct {
	Month    int `json:"m"`
	Day      int `json:"d"`
	DaysLeft int `json:"left"`
}

// pageExpiry is the last day of the user's service period.
type pageExpiry struct {
	Year     int    `json:"y"`
	Month    int    `json:"m"`
	Day      int    `json:"d"`
	DaysLeft int    `json:"left"`
	Tone     string `json:"tone"`
}

// expiryWarnDays is how close to expiry the portal starts warning.
const expiryWarnDays = 7

type pageForecast struct {
	Tone   string  `json:"tone"`
	RunOut float64 `json:"p"`
	Left   string  `json:"left"`
	// Range is where the quota left at the end likely falls (10th to 90th
	// percentile), told when the forecast expects it to last.
	Range  string `json:"range"`
	PerDay string `json:"perDay"`
	// End is what the forecast runs to: reset or expiry.
	End string `json:"end"`
	// From and To are the month and day the quota likely runs out between.
	From *[2]int `json:"from"`
	To   *[2]int `json:"to"`
}

// pageDaily is the daily chart. Positions are percentages of the chart's
// height.
type pageDaily struct {
	Bars      []pageBar `json:"bars"`
	Days      int       `json:"days"`
	Avg       string    `json:"avg"`
	AvgPos    float64   `json:"avgPos"`
	Peak      int       `json:"peak"`
	PeakValue string    `json:"peakValue"`
	Today     string    `json:"today"`
	Budget    string    `json:"budget"`
	BudgetPos float64   `json:"budgetPos"`
	Over      int       `json:"over"`
	First     string    `json:"first"`
	Mid       string    `json:"mid"`
}

type pageBar struct {
	Value   string  `json:"v"`
	Month   int     `json:"m"`
	Day     int     `json:"d"`
	Weekday int     `json:"w"`
	Ago     int     `json:"ago"`
	Hot     bool    `json:"hot"`
	Over    string  `json:"over,omitempty"`
	Height  float64 `json:"h"`
}

type pageHeat struct {
	Rows    []pageHeatRow `json:"rows"`
	Slots   []pageSlot    `json:"slots"`
	Insight *pageInsight  `json:"insight"`
}

type pageHeatRow struct {
	Weekday int            `json:"w"`
	Today   bool           `json:"today"`
	Cells   []pageHeatCell `json:"cells"`
}

type pageHeatCell struct {
	Value string `json:"v"`
	// Share is the cell's percentage of its day, empty on an empty day.
	Share string `json:"sh,omitempty"`
	// Heat is the cell's intensity against the busiest cell, 0 when empty.
	Heat float64 `json:"t,omitempty"`
	// Future marks today's slots yet to come.
	Future bool `json:"f,omitempty"`
}

type pageSlot struct {
	Start   int  `json:"s"`
	Current bool `json:"cur,omitempty"`
	Peak    bool `json:"pk,omitempty"`
}

type pageInsight struct {
	Lo    int    `json:"lo"`
	Hi    int    `json:"hi"`
	Range string `json:"range"`
	Share int    `json:"share"`
}

type pageSubscription struct {
	URI string `json:"uri"`
	QR  string `json:"qr,omitempty"`
}

// pageOutbound is the outbound tab: the member's nodes and the ciphers the
// editor offers.
type pageOutbound struct {
	Methods []string `json:"methods"`
	profilesView
}

// renderPage serves the dashboard with its data inlined. encoding/json
// escapes <, > and &, so the data cannot close the script element it sits in.
func renderPage(page pageData) string {
	data, err := json.Marshal(page)
	if err != nil {
		return renderMessagePage("Error", err.Error(), "")
	}
	return strings.Replace(portalPageHTML, pageDataMarker, string(data), 1)
}

func (h *portalOutbound) buildPage(inboundTag, userName string, isAdmin bool) pageData {
	return h.buildPageAt(inboundTag, userName, isAdmin, time.Now())
}

func (h *portalOutbound) buildPageAt(inboundTag, userName string, isAdmin bool, now time.Time) pageData {
	page := pageData{Title: inboundTag + " / " + userName}
	if snapshot, ok := h.manager.Snapshot(inboundTag, userName); ok {
		user := newPageUser(snapshot, now)
		if conns, ok := h.manager.UserConnections(inboundTag, userName); ok {
			page.Conns = &pageConns{Active: conns.ActiveCount, Closed: conns.ClosedTotal}
		}
		if snapshot.ExpiresAt != 0 {
			lastDay, left := validUntil(time.Unix(snapshot.ExpiresAt, 0), now)
			lastDay = lastDay.In(quotaLocation)
			page.Expiry = &pageExpiry{Year: lastDay.Year(), Month: int(lastDay.Month()), Day: lastDay.Day(), DaysLeft: left, Tone: "ok"}
			if left == 0 {
				page.Expiry.Tone = "err"
			} else if left <= expiryWarnDays {
				page.Expiry.Tone = "warn"
			}
		}
		resetAt, hasReset := h.manager.NextReset(now)
		if hasReset {
			local := resetAt.In(quotaLocation)
			page.Reset = &pageReset{Month: int(local.Month()), Day: local.Day(), DaysLeft: int(math.Ceil(resetAt.Sub(now).Hours() / 24))}
		}
		// The forecast and daily budget run to the reset or the end of the
		// service period, whichever comes first.
		endAt, hasEnd, endKind := resetAt, hasReset, "reset"
		if snapshot.ExpiresAt != 0 {
			if expiry := time.Unix(snapshot.ExpiresAt, 0); expiry.After(now) && (!hasEnd || expiry.Before(endAt)) {
				endAt, hasEnd, endKind = expiry, true, "expiry"
			}
		}
		daysLeft := 0
		if hasEnd {
			daysLeft = int(math.Ceil(endAt.Sub(now).Hours() / 24))
		}
		if usage, ok := h.shichenUsage(inboundTag, userName, now); ok {
			page.Heat, page.VsUsual = newPageHeat(usage)
			page.Daily = newPageDaily(usage, snapshot.RemainingBytes, daysLeft, hasEnd)
			if hasEnd {
				page.Forecast = newPageForecast(usage, snapshot.RemainingBytes, daysLeft, now, endAt)
				if page.Forecast != nil {
					page.Forecast.End = endKind
				}
			}
		}
		if page.Forecast != nil {
			user.Tone = page.Forecast.Tone
		}
		page.Self = &user
		if state := h.manager.userState(inboundTag, userName); state != nil {
			if uri, ok := state.SubscriptionURI(); ok {
				page.Subscription = newPageSubscription(uri)
			}
		}
		if h.memberOutboundConfigDirectory != "" {
			profiles, err := readMemberProfiles(h.memberOutboundConfigDirectory, inboundTag, userName)
			if err != nil {
				if h.logger != nil {
					h.logger.Error("quota-portal: read nodes of ", inboundTag, "/", userName, ": ", err)
				}
				profiles = &memberProfiles{}
			}
			page.Outbound = &pageOutbound{Methods: shadowsocksMethods, profilesView: newProfilesView(profiles)}
		}
	}
	if isAdmin {
		for _, s := range h.manager.Snapshots() {
			page.Users = append(page.Users, newPageUser(s, now))
		}
	}
	return page
}

// round3 keeps chart positions short in JSON.
func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}

func newPageUser(s UserSnapshot, now time.Time) pageUser {
	usedPct := 0.0
	if s.QuotaBytes > 0 {
		usedPct = float64(s.UsedBytes) / float64(s.QuotaBytes) * 100
	}
	pointer := math.Min(100, math.Max(0, usedPct))
	// The ruler's fill splits the used share into upload and download.
	upload := 0.0
	if total := s.UplinkBytes + s.DownlinkBytes; total > 0 {
		upload = pointer * float64(s.UplinkBytes) / float64(total)
	}
	quotaGB := float64(s.QuotaBytes) / gib
	user := pageUser{
		InboundTag:  s.InboundTag,
		UserName:    s.UserName,
		Blocked:     s.Blocked,
		RemainingGB: fmt.Sprintf("%.2f", float64(s.RemainingBytes)/gib),
		Upload:      formatSize(s.UplinkBytes),
		Download:    formatSize(s.DownlinkBytes),
		Used:        formatMB(s.UsedBytes),
		Quota:       formatMB(s.QuotaBytes),
		UsedPct:     fmt.Sprintf("%.1f", usedPct),
		Pointer:     round3(pointer),
		UploadShare: round3(upload),
		Tone:        "ok",
		Conns:       s.ActiveConnections,
	}
	for i := range user.Ticks {
		user.Ticks[i] = strconv.FormatFloat(math.Round(quotaGB*float64(i)/4*10)/10, 'f', -1, 64)
	}
	user.Ticks[4] += " GB"
	if s.ExpiresAt != 0 {
		lastDay, left := validUntil(time.Unix(s.ExpiresAt, 0), now)
		user.ValidUntil = lastDay.In(quotaLocation).Format(time.DateOnly)
		user.Expired = left == 0
	}
	return user
}

// The charts read the last historyDays Beijing-time days split into the
// twelve two-hour shichen (时辰), today included: the daily chart shows all of
// them and the heatmap the last heatmapDays. As with shichen, a day starts at
// 子时, 23:00 of the previous calendar day, and ends at 23:00.
const (
	dailyDays    = 28
	historyDays  = dailyDays + 1
	heatmapDays  = 7
	heatmapSlots = 12
)

// slotStartHour is the hour a shichen starts; 子时 starts at 23:00.
func slotStartHour(slot int) int {
	return (slot*2 + 23) % 24
}

// shichenUsage holds a user's traffic per shichen, oldest day first;
// Days[historyDays-1] is today.
type shichenUsage struct {
	Days [historyDays][heatmapSlots]int64
	// Dates holds the calendar day (BJT) each day mostly falls on.
	Dates      [historyDays]time.Time
	TodayStart time.Time
	NowSlot    int
	// Known is how many past days the history covers, at most dailyDays.
	Known int
}

func (h *portalOutbound) shichenUsage(inboundTag, userName string, now time.Time) (shichenUsage, bool) {
	if h.manager.history == nil {
		return shichenUsage{}, false
	}
	shifted, todayStart := bjtDayStart(now)
	start := todayStart.AddDate(0, 0, -(historyDays - 1))
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	points, err := h.manager.history.Query(ctx, HistoryQuery{
		From: start, To: todayStart.AddDate(0, 0, 1), Granularity: "hour",
		InboundTag: inboundTag, UserName: userName,
	})
	var first time.Time
	var found bool
	if err == nil {
		first, found, err = h.manager.history.FirstBucket(ctx, inboundTag, userName)
	}
	if err != nil {
		if h.logger != nil {
			h.logger.Error("query hourly quota history: ", err)
		}
		return shichenUsage{}, false
	}
	usage := shichenUsage{TodayStart: todayStart, NowSlot: shifted.Hour() / 2}
	for day := range usage.Dates {
		date := start.Add(time.Hour).AddDate(0, 0, day)
		usage.Dates[day] = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, quotaLocation)
	}
	for _, point := range points {
		offset := (point.BucketStart - start.Unix()) / 3600
		if offset < 0 || offset >= historyDays*24 {
			continue
		}
		usage.Days[offset/24][offset%24/2] += point.UsedBytes
	}
	if found {
		firstDay := int(math.Floor(first.Sub(start).Hours() / 24))
		usage.Known = min(dailyDays, max(0, historyDays-1-max(0, firstDay)))
	}
	return usage, true
}

func (u *shichenUsage) dayTotal(day int) int64 {
	var total int64
	for _, v := range u.Days[day] {
		total += v
	}
	return total
}

// newPageHeat builds the heatmap of the last heatmapDays days, and compares
// today so far with the same time on the previous days the history covers.
func newPageHeat(usage shichenUsage) (*pageHeat, *int) {
	first := historyDays - heatmapDays
	last := historyDays - 1
	var max int64 = 1
	for day := first; day <= last; day++ {
		for _, v := range usage.Days[day] {
			if v > max {
				max = v
			}
		}
	}
	heat := &pageHeat{}
	insight, vsUsual := heatInsight(usage)
	heat.Insight = insight
	for slot := range heatmapSlots {
		heat.Slots = append(heat.Slots, pageSlot{
			Start:   slotStartHour(slot),
			Current: slot == usage.NowSlot,
			Peak:    insight != nil && slot >= insight.Lo && slot <= insight.Hi,
		})
	}
	for day := first; day <= last; day++ {
		today := day == last
		total := usage.dayTotal(day)
		row := pageHeatRow{Weekday: int(usage.Dates[day].Weekday()), Today: today}
		for slot, v := range usage.Days[day] {
			cell := pageHeatCell{Value: formatTip(v)}
			if total > 0 {
				cell.Share = strconv.Itoa(int(math.Round(float64(v) / float64(total) * 100)))
			}
			switch {
			case today && slot > usage.NowSlot:
				cell.Future = true
			case v > 0:
				cell.Heat = round3(float64(v) / float64(max))
			}
			row.Cells = append(row.Cells, cell)
		}
		heat.Rows = append(heat.Rows, row)
	}
	return heat, vsUsual
}

// heatInsight finds the busiest shichen over the previous full days of the
// heatmap, joined with neighbors at 80% of it or more, and how today so far
// compares with the same slots on those days.
func heatInsight(usage shichenUsage) (*pageInsight, *int) {
	days := min(heatmapDays-1, usage.Known)
	if days == 0 {
		return nil, nil
	}
	today := historyDays - 1
	var avg [heatmapSlots]float64
	var dayAvg, usual float64
	for day := today - days; day < today; day++ {
		for slot, v := range usage.Days[day] {
			avg[slot] += float64(v) / float64(days)
			dayAvg += float64(v) / float64(days)
			if slot <= usage.NowSlot {
				usual += float64(v) / float64(days)
			}
		}
	}
	var vsUsual *int
	if usual > 0 {
		var sofar float64
		for slot := 0; slot <= usage.NowSlot; slot++ {
			sofar += float64(usage.Days[today][slot])
		}
		pct := int(math.Round((sofar/usual - 1) * 100))
		vsUsual = &pct
	}
	if dayAvg <= 0 {
		return nil, vsUsual
	}
	top := 0
	for slot, v := range avg {
		if v > avg[top] {
			top = slot
		}
	}
	lo, hi := top, top
	for lo > 0 && avg[lo-1] >= avg[top]*0.8 {
		lo--
	}
	for hi < heatmapSlots-1 && avg[hi+1] >= avg[top]*0.8 {
		hi++
	}
	var peak float64
	for slot := lo; slot <= hi; slot++ {
		peak += avg[slot]
	}
	return &pageInsight{
		Lo:    lo,
		Hi:    hi,
		Range: fmt.Sprintf("%02d:00–%02d:00", slotStartHour(lo), slotStartHour(hi+1)),
		Share: int(math.Round(peak / dayAvg * 100)),
	}, vsUsual
}

// newPageDaily builds the daily chart: one bar per past day the history
// covers and one for today so far. With a reset cycle, a dashed line marks
// the daily budget, the remaining quota spread over the days left, and the
// days above it are highlighted.
func newPageDaily(usage shichenUsage, remain int64, daysLeft int, hasBudget bool) *pageDaily {
	if usage.Known == 0 {
		return nil
	}
	firstDay := historyDays - 1 - usage.Known
	totals := make([]int64, 0, usage.Known+1)
	for day := firstDay; day < historyDays; day++ {
		totals = append(totals, usage.dayTotal(day))
	}
	full := totals[:usage.Known]
	var budget float64
	if hasBudget {
		budget = float64(max(0, remain)) / float64(max(1, daysLeft))
	}
	top := budget
	var sum int64
	peak := 0
	for i, v := range totals {
		top = math.Max(top, float64(v))
		if i < len(full) {
			sum += v
			if v > full[peak] {
				peak = i
			}
		}
	}
	top = math.Max(1, top*1.12)
	avg := float64(sum) / float64(len(full))
	daily := &pageDaily{
		Days:      len(full),
		Avg:       formatMB(int64(avg)),
		AvgPos:    round3(avg / top * 100),
		Peak:      peak,
		PeakValue: formatMB(full[peak]),
		Today:     formatTip(totals[len(totals)-1]),
	}
	if hasBudget {
		daily.Budget = formatApprox(int64(budget))
		daily.BudgetPos = round3(budget / top * 100)
	}
	for i, v := range totals {
		date := usage.Dates[firstDay+i]
		bar := pageBar{
			Value:   formatTip(v),
			Month:   int(date.Month()),
			Day:     date.Day(),
			Weekday: int(date.Weekday()),
			Ago:     len(full) - i,
			Height:  round3(math.Max(2, float64(v)/top*100)),
		}
		if bar.Ago != 0 && hasBudget && float64(v) > budget {
			bar.Hot = true
			bar.Over = formatApprox(v - int64(budget))
			daily.Over++
		}
		daily.Bars = append(daily.Bars, bar)
	}
	first, mid := usage.Dates[firstDay], usage.Dates[historyDays-1-int(math.Round(float64(len(full))/2))]
	daily.First = fmt.Sprintf("%d/%d", first.Month(), first.Day())
	daily.Mid = fmt.Sprintf("%d/%d", mid.Month(), mid.Day())
	return daily
}

func newPageForecast(usage shichenUsage, remain int64, daysLeft int, now, resetAt time.Time) *pageForecast {
	firstDay := historyDays - 1 - usage.Known
	input := forecastInput{
		Remain: remain,
		Today:  usage.Dates[historyDays-1].Weekday(),
		Seed:   uint64(usage.TodayStart.Unix() / 86400),
	}
	for day := firstDay; day < historyDays-1; day++ {
		input.History = append(input.History, usage.dayTotal(day))
		input.Weekdays = append(input.Weekdays, usage.Dates[day].Weekday())
	}
	todayEnd := usage.TodayStart.AddDate(0, 0, 1)
	input.RestOfToday = math.Min(todayEnd.Sub(now).Hours(), resetAt.Sub(now).Hours()) / 24
	input.LastDay = max(0, int(resetAt.Sub(todayEnd)/(24*time.Hour)))
	result, ok := forecastRunOut(input)
	if !ok {
		return nil
	}
	forecast := &pageForecast{
		Tone:   result.tone(),
		RunOut: result.RunOut,
		Left:   formatApprox(result.LeftP50),
		Range:  formatApproxRange(result.LeftP10, result.LeftP90),
		PerDay: formatApprox(max(0, remain) / int64(max(1, daysLeft))),
	}
	if result.HasOut {
		today := usage.Dates[historyDays-1]
		from, to := today.AddDate(0, 0, result.OutP25), today.AddDate(0, 0, result.OutP75)
		forecast.From = &[2]int{int(from.Month()), from.Day()}
		forecast.To = &[2]int{int(to.Month()), to.Day()}
	}
	return forecast
}

func newPageSubscription(uri string) *pageSubscription {
	subscription := &pageSubscription{URI: uri}
	if png, err := qrcode.Encode(uri, qrcode.Medium, 320); err == nil {
		subscription.QR = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	return subscription
}

// Sizes use binary units (1 GB = 1024 MB), matching how quota_bytes counts.
const (
	mib = 1 << 20
	gib = 1 << 30
)

// formatMB renders bytes as "0", "348 MB" or "1.61 GB".
func formatMB(b int64) string {
	mb := float64(b) / mib
	if b == 0 {
		return "0"
	}
	if mb < 1024 {
		return fmt.Sprintf("%d MB", int64(math.Round(mb)))
	}
	return fmt.Sprintf("%.2f GB", float64(b)/gib)
}

// formatTip is formatMB with a unit on zero, for chart tooltips.
func formatTip(b int64) string {
	if b == 0 {
		return "0 MB"
	}
	return formatMB(b)
}

// formatApprox is formatMB for estimates: whole GB from 10 GB up, one
// decimal from 1 GB (72.94 GB → 73 GB, 8.26 GB → 8.3 GB).
func formatApprox(b int64) string {
	switch {
	case b >= 10*gib:
		return fmt.Sprintf("%d GB", int64(math.Round(float64(b)/gib)))
	case b >= gib:
		return fmt.Sprintf("%.1f GB", float64(b)/gib)
	}
	return formatMB(b)
}

// formatApproxRange renders lo to hi like formatApprox, sharing the unit when
// both are in GB ("3.1–5.4 GB"), and as a single value when they round alike.
func formatApproxRange(lo, hi int64) string {
	if lo < gib {
		if hi < gib || lo == 0 {
			return formatApprox(lo) + "–" + formatApprox(hi)
		}
		return formatApprox(lo) + " – " + formatApprox(hi)
	}
	gb := func(b int64) string {
		if b >= 10*gib {
			return strconv.FormatInt(int64(math.Round(float64(b)/gib)), 10)
		}
		return fmt.Sprintf("%.1f", float64(b)/gib)
	}
	a, z := gb(lo), gb(hi)
	if a == z {
		return a + " GB"
	}
	return a + "–" + z + " GB"
}

// formatSize is formatMB with two decimals for MB as well.
func formatSize(b int64) string {
	mb := float64(b) / mib
	if mb < 1024 {
		return fmt.Sprintf("%.2f MB", mb)
	}
	return fmt.Sprintf("%.2f GB", float64(b)/gib)
}
