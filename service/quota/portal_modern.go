package quota

import (
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
)

// The portal ships two UIs: the original single-column cards ("classic") and
// the dashboard layout ("modern"). Visitors switch with /?ui=<name>, which is
// remembered in a cookie; modern is the default.
const (
	portalUICookie  = "quota_ui"
	portalUIClassic = "classic"
	portalUIModern  = "modern"
)

func portalUIFromRequest(req *http.Request) string {
	if cookie, err := req.Cookie(portalUICookie); err == nil && cookie.Value == portalUIClassic {
		return portalUIClassic
	}
	return portalUIModern
}

//go:embed portal_modern.html
var portalModernHTML string

var portalModernTemplate = template.Must(template.New("portal_modern").Parse(portalModernHTML))

// shadowsocksMethods are offered in the modern UI's cipher picker.
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

type modernPage struct {
	Title        string
	Self         *modernUser
	Reset        *modernReset
	Forecast     *modernForecast
	Daily        *modernDaily
	Heat         *modernHeat
	Subscription *modernSubscription
	Outbound     *modernOutbound
	Users        []modernUser
	Message      template.HTML
	// Data is what the page script needs to word sentences in either
	// language; the template renders it as JSON.
	Data modernData
}

type modernData struct {
	Reset    *modernReset    `json:"reset"`
	Forecast *modernForecast `json:"forecast"`
	Daily    *modernDaily    `json:"daily"`
	Heat     *modernHeat     `json:"heat"`
	// VsUsual is how much more (positive) or less today's usage so far is
	// than the same time on previous days, in percent.
	VsUsual *int `json:"vs"`
}

type modernUser struct {
	InboundTag  string
	UserName    string
	Blocked     bool
	RemainingGB string
	Upload      string
	Download    string
	Used        string
	Quota       string
	UsedPct     string
	Ruler       template.CSS
	Flip        bool
	FlipMobile  bool
	Ticks       [5]string
	// Tone colors the ruler pointer after the forecast: ok, warn or err.
	Tone string
}

type modernReset struct {
	Month    int `json:"m"`
	Day      int `json:"d"`
	DaysLeft int `json:"left"`
}

type modernForecast struct {
	Tone   string  `json:"tone"`
	RunOut float64 `json:"p"`
	Left   string  `json:"left"`
	PerDay string  `json:"perDay"`
	// From and To are the month and day the quota likely runs out between.
	From *[2]int `json:"from"`
	To   *[2]int `json:"to"`
}

type modernDaily struct {
	Bars      []modernBar  `json:"bars"`
	Days      int          `json:"days"`
	Avg       string       `json:"avg"`
	AvgPos    template.CSS `json:"-"`
	Peak      int          `json:"peak"`
	PeakValue string       `json:"-"`
	Today     string       `json:"-"`
	Budget    string       `json:"budget"`
	BudgetPos template.CSS `json:"-"`
	Over      int          `json:"over"`
	First     string       `json:"-"`
	Mid       string       `json:"-"`
}

type modernBar struct {
	Value   string       `json:"v"`
	Month   int          `json:"m"`
	Day     int          `json:"d"`
	Weekday int          `json:"w"`
	Ago     int          `json:"ago"`
	Hot     bool         `json:"hot"`
	Over    string       `json:"over,omitempty"`
	Class   string       `json:"-"`
	Style   template.CSS `json:"-"`
}

type modernHeat struct {
	Rows    []modernHeatRow                `json:"rows"`
	BySlot  [heatmapSlots][]modernHeatCell `json:"-"`
	Slots   []modernSlot                   `json:"slots"`
	Insight *modernInsight                 `json:"insight"`
	Ramp    []template.CSS                 `json:"-"`
}

type modernHeatRow struct {
	Weekday int              `json:"w"`
	Today   bool             `json:"today"`
	Cells   []modernHeatCell `json:"-"`
}

type modernHeatCell struct {
	Day    int
	Slot   int
	Value  string
	Share  string
	Class  string
	Style  template.CSS
	Future bool
}

type modernSlot struct {
	Index   int  `json:"-"`
	Start   int  `json:"s"`
	Current bool `json:"-"`
	Peak    bool `json:"-"`
}

type modernInsight struct {
	Lo    int    `json:"lo"`
	Hi    int    `json:"hi"`
	Range string `json:"range"`
	Share int    `json:"share"`
}

type modernSubscription struct {
	URI string
	QR  template.URL
}

type modernOutbound struct {
	Server   string
	Port     string
	Method   string
	Methods  []string
	Password string
	Mux      bool
	Padding  bool
	Saved    bool
}

func (h *portalOutbound) renderModern(inboundTag, userName string, isAdmin bool) string {
	return h.renderModernAt(inboundTag, userName, isAdmin, time.Now())
}

func (h *portalOutbound) renderModernAt(inboundTag, userName string, isAdmin bool, now time.Time) string {
	page := modernPage{Title: inboundTag + " / " + userName}
	if snapshot, ok := h.manager.Snapshot(inboundTag, userName); ok {
		user := newModernUser(snapshot)
		page.Self = &user
		resetAt, hasReset := h.manager.NextReset(now)
		daysLeft := 0
		if hasReset {
			daysLeft = int(math.Ceil(resetAt.Sub(now).Hours() / 24))
			local := resetAt.In(quotaLocation)
			page.Reset = &modernReset{Month: int(local.Month()), Day: local.Day(), DaysLeft: daysLeft}
		}
		if usage, ok := h.shichenUsage(inboundTag, userName, now); ok {
			page.Heat, page.Data.VsUsual = newModernHeat(usage)
			page.Daily = newModernDaily(usage, snapshot.RemainingBytes, daysLeft, hasReset)
			if hasReset {
				page.Forecast = newModernForecast(usage, snapshot.RemainingBytes, daysLeft, now, resetAt)
			}
		}
		if page.Forecast != nil {
			user.Tone = page.Forecast.Tone
		}
		if state := h.manager.userState(inboundTag, userName); state != nil {
			if uri, ok := state.SubscriptionURI(); ok {
				page.Subscription = newModernSubscription(uri)
			}
		}
		if h.memberOutboundConfigDirectory != "" {
			page.Outbound = newModernOutbound(readMemberShadowsocksOutbound(h.memberOutboundConfigDirectory, inboundTag, userName))
		}
	}
	if isAdmin {
		for _, s := range h.manager.Snapshots() {
			page.Users = append(page.Users, newModernUser(s))
		}
	}
	if page.Self == nil && len(page.Users) == 0 {
		page.Message = `<div class="card">No quota configured.</div>`
	}
	page.Data.Reset, page.Data.Forecast, page.Data.Daily, page.Data.Heat = page.Reset, page.Forecast, page.Daily, page.Heat
	return executeModernPage(page)
}

func executeModernPage(page modernPage) string {
	var sb strings.Builder
	if err := portalModernTemplate.Execute(&sb, page); err != nil {
		return buildPage("Error", fmt.Sprintf(`<div class="card">%s</div>`, template.HTMLEscapeString(err.Error())))
	}
	return sb.String()
}

func newModernUser(s UserSnapshot) modernUser {
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
	user := modernUser{
		InboundTag:  s.InboundTag,
		UserName:    s.UserName,
		Blocked:     s.Blocked,
		RemainingGB: fmt.Sprintf("%.2f", float64(s.RemainingBytes)/gib),
		Upload:      formatSize(s.UplinkBytes),
		Download:    formatSize(s.DownlinkBytes),
		Used:        formatMB(s.UsedBytes),
		Quota:       formatMB(s.QuotaBytes),
		UsedPct:     fmt.Sprintf("%.1f", usedPct),
		Ruler:       template.CSS(fmt.Sprintf("--p:%.3f%%;--pu:%.3f%%", pointer, upload)),
		Flip:        pointer > 70,
		FlipMobile:  pointer > 55,
		Tone:        "ok",
	}
	for i := range user.Ticks {
		user.Ticks[i] = strconv.FormatFloat(math.Round(quotaGB*float64(i)/4*10)/10, 'f', -1, 64)
	}
	user.Ticks[4] += " GB"
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

var shichenNames = [heatmapSlots]string{"子", "丑", "寅", "卯", "辰", "巳", "午", "未", "申", "酉", "戌", "亥"}

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
	// Shifting by an hour makes 23:00 the start of the next day's slot 0.
	shifted := now.In(quotaLocation).Add(time.Hour)
	todayStart := time.Date(shifted.Year(), shifted.Month(), shifted.Day(), 0, 0, 0, 0, quotaLocation).Add(-time.Hour)
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

// heatRamp holds the legend's intensities; 0 is drawn as an empty cell.
var heatRamp = []float64{0.25, 0.5, 0.75, 1}

// newModernHeat builds the heatmap of the last heatmapDays days, and compares
// today so far with the same time on the previous days the history covers.
func newModernHeat(usage shichenUsage) (*modernHeat, *int) {
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
	heat := &modernHeat{}
	insight, vsUsual := heatInsight(usage)
	heat.Insight = insight
	for slot := range heatmapSlots {
		heat.Slots = append(heat.Slots, modernSlot{
			Index:   slot,
			Start:   slotStartHour(slot),
			Current: slot == usage.NowSlot,
			Peak:    insight != nil && slot >= insight.Lo && slot <= insight.Hi,
		})
	}
	for day := first; day <= last; day++ {
		today := day == last
		total := usage.dayTotal(day)
		row := modernHeatRow{Weekday: int(usage.Dates[day].Weekday()), Today: today}
		for slot, v := range usage.Days[day] {
			cell := modernHeatCell{Day: day - first, Slot: slot, Value: formatTip(v)}
			if total > 0 {
				cell.Share = strconv.Itoa(int(math.Round(float64(v) / float64(total) * 100)))
			}
			switch {
			case today && slot > usage.NowSlot:
				cell.Class = "future"
				cell.Future = true
			case v > 0:
				cell.Class = "heat"
				cell.Style = template.CSS(fmt.Sprintf("--t:%.3f", float64(v)/float64(max)))
			default:
				cell.Class = "empty"
			}
			row.Cells = append(row.Cells, cell)
			heat.BySlot[slot] = append(heat.BySlot[slot], cell)
		}
		heat.Rows = append(heat.Rows, row)
	}
	for _, t := range heatRamp {
		heat.Ramp = append(heat.Ramp, template.CSS(fmt.Sprintf("--t:%.2f", t)))
	}
	return heat, vsUsual
}

// heatInsight finds the busiest shichen over the previous full days of the
// heatmap, joined with neighbors at 80% of it or more, and how today so far
// compares with the same slots on those days.
func heatInsight(usage shichenUsage) (*modernInsight, *int) {
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
	return &modernInsight{
		Lo:    lo,
		Hi:    hi,
		Range: fmt.Sprintf("%02d:00–%02d:00", slotStartHour(lo), slotStartHour(hi+1)),
		Share: int(math.Round(peak / dayAvg * 100)),
	}, vsUsual
}

// newModernDaily builds the daily chart: one bar per past day the history
// covers and one for today so far. With a reset cycle, a dashed line marks
// the daily budget, the remaining quota spread over the days left, and the
// days above it are highlighted.
func newModernDaily(usage shichenUsage, remain int64, daysLeft int, hasBudget bool) *modernDaily {
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
	daily := &modernDaily{
		Days:      len(full),
		Avg:       formatMB(int64(avg)),
		AvgPos:    template.CSS(fmt.Sprintf("bottom:%.3f%%", avg/top*100)),
		Peak:      peak,
		PeakValue: formatMB(full[peak]),
		Today:     formatTip(totals[len(totals)-1]),
	}
	if hasBudget {
		daily.Budget = formatApprox(int64(budget))
		daily.BudgetPos = template.CSS(fmt.Sprintf("bottom:%.3f%%", budget/top*100))
	}
	for i, v := range totals {
		date := usage.Dates[firstDay+i]
		bar := modernBar{
			Value:   formatTip(v),
			Month:   int(date.Month()),
			Day:     date.Day(),
			Weekday: int(date.Weekday()),
			Ago:     len(full) - i,
			Style:   template.CSS(fmt.Sprintf("height:%.3f%%;--i:%d", math.Max(2, float64(v)/top*100), i)),
		}
		switch {
		case bar.Ago == 0:
			bar.Class = "today"
		case hasBudget && float64(v) > budget:
			bar.Hot = true
			bar.Over = formatApprox(v - int64(budget))
			bar.Class = "hot"
			daily.Over++
		}
		daily.Bars = append(daily.Bars, bar)
	}
	first, mid := usage.Dates[firstDay], usage.Dates[historyDays-1-int(math.Round(float64(len(full))/2))]
	daily.First = fmt.Sprintf("%d/%d", first.Month(), first.Day())
	daily.Mid = fmt.Sprintf("%d/%d", mid.Month(), mid.Day())
	return daily
}

func newModernForecast(usage shichenUsage, remain int64, daysLeft int, now, resetAt time.Time) *modernForecast {
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
	forecast := &modernForecast{
		Tone:   result.tone(),
		RunOut: result.RunOut,
		Left:   formatApprox(result.LeftP50),
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

func newModernSubscription(uri string) *modernSubscription {
	subscription := &modernSubscription{URI: uri}
	if png, err := qrcode.Encode(uri, qrcode.Medium, 320); err == nil {
		subscription.QR = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
	}
	return subscription
}

func newModernOutbound(existing *shadowsocksOutboundForm) *modernOutbound {
	outbound := &modernOutbound{Method: "aes-256-gcm"}
	if existing != nil {
		outbound.Server = existing.Server
		if existing.ServerPort > 0 {
			outbound.Port = strconv.Itoa(existing.ServerPort)
		}
		if existing.Method != "" {
			outbound.Method = existing.Method
		}
		outbound.Password = existing.Password
		outbound.Mux = existing.Mux
		outbound.Padding = existing.Padding
		outbound.Saved = existing.Saved
	}
	outbound.Methods = shadowsocksMethods
	known := false
	for _, method := range shadowsocksMethods {
		if method == outbound.Method {
			known = true
			break
		}
	}
	if !known {
		outbound.Methods = append([]string{outbound.Method}, shadowsocksMethods...)
	}
	return outbound
}

// Sizes use binary units (1 GB = 1024 MB), matching how quota_bytes and the
// classic page count.
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

// formatApprox is formatMB for estimates: whole GB from 10 GB up.
func formatApprox(b int64) string {
	if b >= 10*gib {
		return fmt.Sprintf("%d GB", int64(math.Round(float64(b)/gib)))
	}
	return formatMB(b)
}

// formatSize is formatMB with two decimals for MB as well.
func formatSize(b int64) string {
	mb := float64(b) / mib
	if mb < 1024 {
		return fmt.Sprintf("%.2f MB", mb)
	}
	return fmt.Sprintf("%.2f GB", float64(b)/gib)
}
