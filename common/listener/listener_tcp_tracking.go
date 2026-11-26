package listener

import (
	"net"
	"os"
	"sync"
	"syscall"
)

// trackingListener registers every accepted connection with the owning
// Listener so that Listener.Close can terminate them.
//
// Without this, closing an inbound (e.g. during a SIGHUP reload) only stops
// accepting new connections: already-accepted connections — including those
// hijacked by HTTP-based transports and long-lived multiplex sessions — keep
// running against the old instance's router indefinitely.
type trackingListener struct {
	net.Listener
	owner *Listener
}

func (t *trackingListener) Accept() (net.Conn, error) {
	for {
		conn, err := t.Listener.Accept()
		if err != nil {
			return nil, err
		}
		tracked := &trackedInboundConn{Conn: conn, owner: t.owner}
		t.owner.connsAccess.Lock()
		if t.owner.shutdown.Load() {
			t.owner.connsAccess.Unlock()
			_ = conn.Close()
			continue
		}
		t.owner.conns[tracked] = struct{}{}
		t.owner.connsAccess.Unlock()
		return tracked, nil
	}
}

type trackedInboundConn struct {
	net.Conn
	owner     *Listener
	closeOnce sync.Once
	closeErr  error
}

func (c *trackedInboundConn) Close() error {
	c.closeOnce.Do(func() {
		c.owner.connsAccess.Lock()
		delete(c.owner.conns, c)
		c.owner.connsAccess.Unlock()
		c.closeErr = c.Conn.Close()
	})
	return c.closeErr
}

func (c *trackedInboundConn) SyscallConn() (syscall.RawConn, error) {
	syscallConn, isSyscallConn := c.Conn.(syscall.Conn)
	if !isSyscallConn {
		return nil, os.ErrInvalid
	}
	return syscallConn.SyscallConn()
}

func (c *trackedInboundConn) Upstream() any {
	return c.Conn
}

func (c *trackedInboundConn) ReaderReplaceable() bool {
	return true
}

func (c *trackedInboundConn) WriterReplaceable() bool {
	return true
}
