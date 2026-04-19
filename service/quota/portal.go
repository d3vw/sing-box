package quota

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	boxOutbound "github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterPortalOutbound(registry *boxOutbound.Registry) {
	boxOutbound.Register[option.QuotaPortalOutboundOptions](registry, C.TypeQuotaPortal, newPortalOutbound)
}

type portalOutbound struct {
	boxOutbound.Adapter
	logger  log.ContextLogger
	manager *Manager
}

func newPortalOutbound(ctx context.Context, _ adapter.Router, logger log.ContextLogger, tag string, _ option.QuotaPortalOutboundOptions) (adapter.Outbound, error) {
	manager := service.FromContext[*Manager](ctx)
	if manager == nil {
		return nil, fmt.Errorf("quota-portal outbound requires a quota service to be configured")
	}
	return &portalOutbound{
		Adapter: boxOutbound.NewAdapter(C.TypeQuotaPortal, tag, []string{N.NetworkTCP}, nil),
		logger:  logger,
		manager: manager,
	}, nil
}

func (h *portalOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	inboundTag := ""
	if metadata := adapter.ContextFrom(ctx); metadata != nil {
		inboundTag = metadata.Inbound
	}

	client, server := net.Pipe()
	go h.serveHTTP(server, inboundTag)
	return client, nil
}

func (h *portalOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, fmt.Errorf("quota-portal does not support UDP")
}

func (h *portalOutbound) serveHTTP(conn net.Conn, inboundTag string) {
	defer conn.Close()
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	_ = req

	var body string
	if inboundTag == "" {
		body = h.renderAll()
	} else if snapshot, ok := h.manager.Snapshot(inboundTag); ok {
		body = buildPage(inboundTag, renderSnapshot(snapshot))
	} else {
		body = h.renderAll()
	}

	resp := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		len(body), body)
	conn.Write([]byte(resp))
}

func (h *portalOutbound) renderAll() string {
	snapshots := h.manager.Snapshots()
	var sb strings.Builder
	for _, s := range snapshots {
		sb.WriteString(renderSnapshot(s))
	}
	if sb.Len() == 0 {
		return buildPage("No quota data available.", "")
	}
	return buildPage("Traffic Quota", sb.String())
}

func renderSnapshot(s InboundSnapshot) string {
	usedPct := 0.0
	if s.QuotaBytes > 0 {
		usedPct = float64(s.UsedBytes) / float64(s.QuotaBytes) * 100
	}
	if usedPct > 100 {
		usedPct = 100
	}
	accentColor := "#3b82f6"
	badgeBg := "#1e3a5f"
	badgeText := "ACTIVE"
	if s.Blocked {
		accentColor = "#ef4444"
		badgeBg = "#3f1f1f"
		badgeText = "BLOCKED"
	}
	return fmt.Sprintf(`<div class="card">
  <div class="card-header">
    <div class="tag-row">
      <span class="tag-name">%s</span>
      <span class="badge" style="background:%s;color:%s">%s</span>
    </div>
    <div class="usage-label">%s <span class="muted">of</span> %s</div>
  </div>
  <div class="bar-track"><div class="bar-fill" style="width:%.1f%%;background:%s"></div></div>
  <div class="pct-label" style="color:%s">%.1f%% used</div>
  <div class="stats">
    <div class="stat-item">
      <div class="stat-label">Remaining</div>
      <div class="stat-value">%s</div>
    </div>
    <div class="stat-divider"></div>
    <div class="stat-item">
      <div class="stat-label">Upload</div>
      <div class="stat-value">%s</div>
    </div>
    <div class="stat-divider"></div>
    <div class="stat-item">
      <div class="stat-label">Download</div>
      <div class="stat-value">%s</div>
    </div>
  </div>
</div>`,
		s.Tag,
		badgeBg, accentColor, badgeText,
		formatBytes(s.UsedBytes), formatBytes(s.QuotaBytes),
		usedPct, accentColor,
		accentColor, usedPct,
		formatBytes(s.RemainingBytes),
		formatBytes(s.UplinkBytes),
		formatBytes(s.DownlinkBytes),
	)
}

func buildPage(title, content string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>%s — Quota</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{
  font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;
  font-size:14px;
  background:#0a0a0a;
  color:#e2e2e2;
  min-height:100vh;
  display:flex;
  flex-direction:column;
  align-items:center;
  justify-content:center;
  padding:24px;
}
.page-title{
  font-size:11px;
  letter-spacing:0.12em;
  text-transform:uppercase;
  color:#555;
  margin-bottom:24px;
}
.cards{display:flex;flex-direction:column;gap:16px;width:100%%;max-width:420px}
.card{
  background:#141414;
  border:1px solid #222;
  border-radius:16px;
  padding:24px;
}
.card-header{margin-bottom:16px}
.tag-row{display:flex;align-items:center;justify-content:space-between;margin-bottom:6px}
.tag-name{font-size:16px;font-weight:600;color:#f0f0f0;letter-spacing:-0.01em}
.badge{
  font-size:10px;
  font-weight:700;
  letter-spacing:0.08em;
  text-transform:uppercase;
  padding:3px 8px;
  border-radius:6px;
}
.usage-label{font-size:22px;font-weight:700;color:#f0f0f0;letter-spacing:-0.02em}
.usage-label .muted{font-size:14px;font-weight:400;color:#555}
.bar-track{background:#1e1e1e;border-radius:99px;height:6px;margin:16px 0 6px}
.bar-fill{height:6px;border-radius:99px;transition:width .4s ease}
.pct-label{font-size:11px;font-weight:600;letter-spacing:0.04em;margin-bottom:20px}
.stats{display:flex;align-items:center;gap:0;border-top:1px solid #1e1e1e;padding-top:16px}
.stat-item{flex:1;text-align:center}
.stat-divider{width:1px;height:32px;background:#1e1e1e}
.stat-label{font-size:10px;letter-spacing:0.08em;text-transform:uppercase;color:#555;margin-bottom:4px}
.stat-value{font-size:14px;font-weight:600;color:#d0d0d0}
</style>
</head>
<body>
<div class="page-title">Traffic Quota</div>
<div class="cards">%s</div>
</body>
</html>`, title, content)
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
