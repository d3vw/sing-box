package listener

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// Connections accepted from the listener returned by ListenTCP (as transports
// such as httpupgrade/ws/grpc do) must be closed when the Listener is closed,
// otherwise they outlive a reload and keep using the old instance.
func TestListenerCloseTerminatesAcceptedConnections(t *testing.T) {
	t.Parallel()
	addr := badoption.Addr(netip.MustParseAddr("127.0.0.1"))
	l := New(Options{
		Context: context.Background(),
		Logger:  log.NewNOPFactory().Logger(),
		Network: []string{N.NetworkTCP},
		Listen:  option.ListenOptions{Listen: &addr},
	})
	tcpListener, err := l.ListenTCP()
	require.NoError(t, err)

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := tcpListener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()

	client, err := net.Dial("tcp", tcpListener.Addr().String())
	require.NoError(t, err)
	defer client.Close()

	var server net.Conn
	select {
	case server = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("accept timeout")
	}

	// A connection closed by its handler must be dropped from tracking.
	other, err := net.Dial("tcp", tcpListener.Addr().String())
	require.NoError(t, err)
	defer other.Close()
	go func() {
		conn, acceptErr := tcpListener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	select {
	case conn := <-accepted:
		require.NoError(t, conn.Close())
	case <-time.After(5 * time.Second):
		t.Fatal("accept timeout")
	}
	l.connsAccess.Lock()
	require.Len(t, l.conns, 1)
	l.connsAccess.Unlock()

	require.NoError(t, l.Close())

	require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = client.Read(make([]byte, 1))
	require.Error(t, err)
	var netErr net.Error
	if ok := asNetError(err, &netErr); ok {
		require.False(t, netErr.Timeout(), "accepted connection was not closed by Listener.Close")
	}
	_ = server
}

func asNetError(err error, target *net.Error) bool {
	ne, ok := err.(net.Error)
	if ok {
		*target = ne
	}
	return ok
}
