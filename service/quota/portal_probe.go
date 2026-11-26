package quota

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
)

// The modern UI's network quality test opens a WebSocket to probePath and
// sends a packet number every 100 ms for 20 s; the portal echoes each one
// back. The portal is served through the member's tunnel, so the round trips
// measure the tunnel itself.
const (
	probePath = "/probe"
	// probeMaxDuration bounds one test: 20 s of sending plus time for late
	// replies.
	probeMaxDuration = 30 * time.Second
	// probeMaxMessage is far above a packet number; anything larger is not
	// from the page.
	probeMaxMessage = 64
)

// hijackWriter is the http.ResponseWriter websocket.Accept needs, over the
// raw connection the portal serves.
type hijackWriter struct {
	conn   net.Conn
	reader *bufio.Reader
	header http.Header
	wrote  bool
}

func (w *hijackWriter) Header() http.Header { return w.header }

func (w *hijackWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.wrote = true
	bw := bufio.NewWriter(w.conn)
	bw.WriteString("HTTP/1.1 " + strconv.Itoa(status) + " " + http.StatusText(status) + "\r\n")
	if status != http.StatusSwitchingProtocols {
		w.header.Set("Connection", "close")
	}
	w.header.Write(bw)
	bw.WriteString("\r\n")
	bw.Flush()
}

func (w *hijackWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.conn.Write(b)
}

func (w *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(w.reader, bufio.NewWriter(w.conn)), nil
}

func (h *portalOutbound) handleProbe(conn net.Conn, reader *bufio.Reader, req *http.Request) {
	w := &hijackWriter{conn: conn, reader: reader, header: http.Header{}}
	ws, err := websocket.Accept(w, req, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(probeMaxMessage)
	parent := h.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, probeMaxDuration)
	defer cancel()
	for {
		kind, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if err = ws.Write(ctx, kind, data); err != nil {
			return
		}
	}
}
