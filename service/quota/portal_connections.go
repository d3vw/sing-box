package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// The modern UI follows the member's connections at connectionsPath. A
// plain GET returns them once as JSON; a WebSocket gets them now and again
// whenever one opens or closes, at most every connPushGap, and every
// connRefresh for the traffic of the open ones. The page sends
// {"detail":true} to get the connections themselves rather than only the
// counts.
const (
	connectionsPath      = "/connections"
	connectionsClosePath = "/connections/close"
	connPushGap          = 250 * time.Millisecond
	connRefresh          = time.Second
	connPingEvery        = 20 * time.Second
	connPingTimeout      = 10 * time.Second
	connMaxMessage       = 256
)

type connMessage struct {
	Type string `json:"type"`
	ConnSnapshot
}

type connRequest struct {
	Detail *bool `json:"detail"`
}

func (h *portalOutbound) handleConnections(conn net.Conn, reader *bufio.Reader, req *http.Request, inboundTag, userName string) {
	state := h.manager.trafficState(inboundTag, userName)
	if state == nil {
		h.writeJSON(conn, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	key := state.Key
	if !strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		h.writeJSON(conn, http.StatusOK, h.manager.conns.snapshot(key, true))
		return
	}
	w := &hijackWriter{conn: conn, reader: reader, header: http.Header{}}
	ws, err := websocket.Accept(w, req, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(connMaxMessage)
	parent := h.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	changed, unsubscribe := h.manager.conns.subscribe(key)
	defer unsubscribe()
	detailCh := make(chan bool, 1)
	go func() {
		defer cancel()
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var request connRequest
			if json.Unmarshal(data, &request) != nil || request.Detail == nil {
				continue
			}
			select {
			case <-detailCh:
			default:
			}
			detailCh <- *request.Detail
		}
	}()

	detail := false
	push := func() error {
		message := connMessage{Type: "snapshot", ConnSnapshot: h.manager.conns.snapshot(key, detail)}
		content, _ := json.Marshal(message)
		writeCtx, writeCancel := context.WithTimeout(ctx, connPingTimeout)
		defer writeCancel()
		return ws.Write(writeCtx, websocket.MessageText, content)
	}
	if push() != nil {
		return
	}
	refresh := time.NewTicker(connRefresh)
	defer refresh.Stop()
	ping := time.NewTicker(connPingEvery)
	defer ping.Stop()
	var lastPush time.Time
	var pending <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case detail = <-detailCh:
		case <-changed:
			// Coalesce bursts: push now unless one just went out, then
			// once more when the gap is up.
			if pending != nil {
				continue
			}
			if wait := connPushGap - time.Since(lastPush); wait > 0 {
				pending = time.After(wait)
				continue
			}
		case <-pending:
		case <-refresh.C:
		case <-ping.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, connPingTimeout)
			err := ws.Ping(pingCtx)
			pingCancel()
			if err != nil {
				return
			}
			continue
		}
		pending = nil
		if push() != nil {
			return
		}
		lastPush = time.Now()
		refresh.Reset(connRefresh)
	}
}

// handleCloseConnections closes one of the member's connections (id), or
// all of them (all=true), never the portal's own.
func (h *portalOutbound) handleCloseConnections(conn net.Conn, req *http.Request, inboundTag, userName string) {
	if err := req.ParseForm(); err != nil {
		h.writeJSON(conn, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if h.manager.trafficState(inboundTag, userName) == nil {
		h.writeJSON(conn, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	if req.Form.Get("all") == "true" {
		h.writeJSON(conn, http.StatusOK, map[string]int{"closed": h.manager.CloseUserActiveConnections(inboundTag, userName)})
		return
	}
	id := req.Form.Get("id")
	if id == "" {
		h.writeJSON(conn, http.StatusBadRequest, map[string]string{"error": "missing id"})
		return
	}
	if !h.manager.CloseUserConnection(inboundTag, userName, id) {
		h.writeJSON(conn, http.StatusNotFound, map[string]string{"error": "connection not found"})
		return
	}
	h.writeJSON(conn, http.StatusOK, map[string]int{"closed": 1})
}
