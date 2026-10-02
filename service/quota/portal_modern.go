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
	Heat         *modernHeat
	Subscription *modernSubscription
	Outbound     *modernOutbound
	Users        []modernUser
	Message      template.HTML
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
	Pointer     template.CSS
	Flip        bool
	Ticks       [5]string
}

type modernHeat struct {
	Rows       []modernHeatRow
	BySlot     [heatmapSlots][]modernHeatCell
	SlotLabels [heatmapSlots]string
	WeekTotal  string
	NowSlot    int
	Ramp       []template.CSS
}

type modernHeatRow struct {
	Weekday    int
	Today      bool
	Total      string
	TotalShort string
	HasTotal   bool
	Cells      []modernHeatCell
}

type modernHeatCell struct {
	Day    int
	Slot   int
	Value  string
	Class  string
	Style  template.CSS
	Future bool
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
	page := modernPage{Title: inboundTag + " / " + userName}
	if snapshot, ok := h.manager.Snapshot(inboundTag, userName); ok {
		user := newModernUser(snapshot)
		page.Self = &user
		if usage, ok := h.shichenUsage(inboundTag, userName, time.Now()); ok {
			page.Heat = newModernHeat(usage)
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
		Pointer:     template.CSS(fmt.Sprintf("--p:%.3f%%", pointer)),
		Flip:        pointer > 70,
	}
	for i := range user.Ticks {
		user.Ticks[i] = strconv.FormatFloat(math.Round(quotaGB*float64(i)/4*10)/10, 'f', -1, 64)
	}
	user.Ticks[4] += " GB"
	return user
}

// The heatmap shows the last heatmapDays Beijing-time days split into the
// twelve two-hour shichen (时辰), today included. As with shichen, a day starts
// at 子时, 23:00 of the previous calendar day, and ends at 23:00.
const (
	heatmapDays  = 7
	heatmapSlots = 12
)

var heatmapLocation = time.FixedZone("BJT", 8*60*60)

var shichenNames = [heatmapSlots]string{"子", "丑", "寅", "卯", "辰", "巳", "午", "未", "申", "酉", "戌", "亥"}

// shichenUsage holds a user's traffic per shichen, oldest day first;
// Days[heatmapDays-1] is today.
type shichenUsage struct {
	Days     [heatmapDays][heatmapSlots]int64
	Weekdays [heatmapDays]time.Weekday
	NowSlot  int
}

func (h *portalOutbound) shichenUsage(inboundTag, userName string, now time.Time) (shichenUsage, bool) {
	if h.manager.history == nil {
		return shichenUsage{}, false
	}
	// Shifting by an hour makes 23:00 the start of the next day's slot 0.
	shifted := now.In(heatmapLocation).Add(time.Hour)
	todayStart := time.Date(shifted.Year(), shifted.Month(), shifted.Day(), 0, 0, 0, 0, heatmapLocation).Add(-time.Hour)
	start := todayStart.AddDate(0, 0, -(heatmapDays - 1))
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	points, err := h.manager.history.Query(ctx, HistoryQuery{
		From: start, To: todayStart.AddDate(0, 0, 1), Granularity: "hour",
		InboundTag: inboundTag, UserName: userName,
	})
	if err != nil {
		if h.logger != nil {
			h.logger.Error("query hourly quota history: ", err)
		}
		return shichenUsage{}, false
	}
	usage := shichenUsage{NowSlot: shifted.Hour() / 2}
	for day := range usage.Weekdays {
		usage.Weekdays[day] = start.Add(time.Hour).AddDate(0, 0, day).Weekday()
	}
	for _, point := range points {
		offset := (point.BucketStart - start.Unix()) / 3600
		if offset < 0 || offset >= heatmapDays*24 {
			continue
		}
		usage.Days[offset/24][offset%24/2] += point.UsedBytes
	}
	return usage, true
}

func newModernHeat(usage shichenUsage) *modernHeat {
	var max int64 = 1
	for _, day := range usage.Days {
		for _, v := range day {
			if v > max {
				max = v
			}
		}
	}
	heat := &modernHeat{NowSlot: usage.NowSlot, SlotLabels: shichenNames}
	var week int64
	for day := range usage.Days {
		today := day == heatmapDays-1
		var total int64
		row := modernHeatRow{Weekday: int(usage.Weekdays[day]), Today: today}
		for slot, v := range usage.Days[day] {
			total += v
			cell := modernHeatCell{Day: day, Slot: slot, Value: formatMB(v)}
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
		week += total
		row.Total = formatMB(total)
		row.TotalShort = strings.NewReplacer(" GB", "G", " MB", "M").Replace(row.Total)
		row.HasTotal = total > 0
		heat.Rows = append(heat.Rows, row)
	}
	heat.WeekTotal = formatMB(week)
	for _, t := range []float64{0.2, 0.44, 0.71, 1} {
		heat.Ramp = append(heat.Ramp, template.CSS(fmt.Sprintf("--t:%.2f", t)))
	}
	return heat
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

// formatSize is formatMB with two decimals for MB as well.
func formatSize(b int64) string {
	mb := float64(b) / mib
	if mb < 1024 {
		return fmt.Sprintf("%.2f MB", mb)
	}
	return fmt.Sprintf("%.2f GB", float64(b)/gib)
}
