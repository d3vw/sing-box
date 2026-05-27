package quota

import (
	"context"
	"fmt"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
)

type UserKey struct {
	InboundTag string
	UserName   string
}

func (k UserKey) String() string {
	return fmt.Sprintf("%s:%s", k.InboundTag, k.UserName)
}

type UserState struct {
	Key            UserKey
	QuotaBytes     int64
	Admin          bool
	Uplink         atomic.Int64
	Downlink       atomic.Int64
	RateLimitRead  int64
	RateLimitWrite int64
	readLimiter    *TokenBucketLimiter
	writeLimiter   *TokenBucketLimiter
}

type UserSnapshot struct {
	InboundTag     string `json:"inbound_tag"`
	UserName       string `json:"user_name"`
	UplinkBytes    int64  `json:"uplink_bytes"`
	DownlinkBytes  int64  `json:"downlink_bytes"`
	UsedBytes      int64  `json:"used_bytes"`
	QuotaBytes     int64  `json:"quota_bytes"`
	RemainingBytes int64  `json:"remaining_bytes"`
	Blocked        bool   `json:"blocked"`
	RateLimitRead  int64  `json:"rate_limit_read"`
	RateLimitWrite int64  `json:"rate_limit_write"`
}

type Manager struct {
	access      sync.RWMutex
	users       map[UserKey]*UserState
	connsAccess sync.Mutex
	conns       map[UserKey][]net.Conn
}

func NewManager() *Manager {
	return &Manager{
		users: make(map[UserKey]*UserState),
		conns: make(map[UserKey][]net.Conn),
	}
}

func (m *Manager) addConn(key UserKey, conn net.Conn) {
	m.connsAccess.Lock()
	defer m.connsAccess.Unlock()
	m.conns[key] = append(m.conns[key], conn)
}

func (m *Manager) removeConn(key UserKey, conn net.Conn) {
	m.connsAccess.Lock()
	defer m.connsAccess.Unlock()
	list := m.conns[key]
	for i, c := range list {
		if c == conn {
			m.conns[key] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(m.conns[key]) == 0 {
		delete(m.conns, key)
	}
}

func (m *Manager) CloseUserConnections(inboundTag, userName string) {
	key := UserKey{InboundTag: inboundTag, UserName: userName}
	m.connsAccess.Lock()
	list := m.conns[key]
	delete(m.conns, key)
	m.connsAccess.Unlock()

	for _, c := range list {
		_ = c.Close()
	}
}

type TokenBucketLimiter struct {
	rate       float64
	burst      float64
	tokens     float64
	lastUpdate time.Time
	mu         sync.Mutex
}

func NewTokenBucketLimiter(rateVal int64) *TokenBucketLimiter {
	return &TokenBucketLimiter{
		rate:       float64(rateVal),
		burst:      float64(rateVal) * 10,
		tokens:     float64(rateVal) * 10,
		lastUpdate: time.Now(),
	}
}

func (l *TokenBucketLimiter) WaitN(ctx context.Context, n int) error {
	for {
		l.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(l.lastUpdate).Seconds()
		l.tokens += elapsed * l.rate
		l.lastUpdate = now
		
		burstLimit := l.burst
		if float64(n) > burstLimit {
			burstLimit = float64(n)
		}
		if l.tokens > burstLimit {
			l.tokens = burstLimit
		}

		if l.tokens >= float64(n) {
			l.tokens -= float64(n)
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type rateLimitConn struct {
	net.Conn
	readLimiter  *TokenBucketLimiter
	writeLimiter *TokenBucketLimiter
	ctx          context.Context
}

func (c *rateLimitConn) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if err != nil {
		return n, err
	}
	if c.readLimiter != nil && n > 0 {
		err = c.readLimiter.WaitN(c.ctx, n)
	}
	return n, err
}

func (c *rateLimitConn) Write(b []byte) (n int, err error) {
	if c.writeLimiter == nil {
		return c.Conn.Write(b)
	}
	var written int
	for len(b) > 0 {
		chunkSize := len(b)
		if chunkSize > 65536 {
			chunkSize = 65536
		}
		err = c.writeLimiter.WaitN(c.ctx, chunkSize)
		if err != nil {
			return written, err
		}
		n, err = c.Conn.Write(b[:chunkSize])
		written += n
		if err != nil {
			return written, err
		}
		b = b[n:]
	}
	return written, nil
}

type quotaConn struct {
	net.Conn
	manager *Manager
	key     UserKey
}

func (c *quotaConn) Close() error {
	c.manager.removeConn(c.key, c.Conn)
	return c.Conn.Close()
}

func (c *quotaConn) Upstream() any {
	return c.Conn
}

func (c *quotaConn) ReaderReplaceable() bool {
	return true
}

func (c *quotaConn) WriterReplaceable() bool {
	return true
}

func (m *Manager) AddUser(inboundTag, userName string, quotaBytes int64, admin bool) {
	key := UserKey{InboundTag: inboundTag, UserName: userName}
	m.access.Lock()
	defer m.access.Unlock()
	if _, exists := m.users[key]; exists {
		return
	}
	m.users[key] = &UserState{
		Key:        key,
		QuotaBytes: quotaBytes,
		Admin:      admin,
	}
}

func (m *Manager) SetUserRateLimit(inboundTag, userName string, rateLimitRead, rateLimitWrite int64) bool {
	state := m.userState(inboundTag, userName)
	if state == nil {
		return false
	}
	state.RateLimitRead = rateLimitRead
	state.RateLimitWrite = rateLimitWrite
	if rateLimitRead > 0 {
		state.readLimiter = NewTokenBucketLimiter(rateLimitRead)
	} else {
		state.readLimiter = nil
	}
	if rateLimitWrite > 0 {
		state.writeLimiter = NewTokenBucketLimiter(rateLimitWrite)
	} else {
		state.writeLimiter = nil
	}
	return true
}

func (m *Manager) CheckConnection(ctx context.Context, metadata adapter.InboundContext) error {
	return m.checkUser(metadata.Inbound, metadata.User)
}

func (m *Manager) CheckPacketConnection(ctx context.Context, metadata adapter.InboundContext) error {
	return m.checkUser(metadata.Inbound, metadata.User)
}

func (m *Manager) RoutedConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) net.Conn {
	state := m.userState(metadata.Inbound, metadata.User)
	if state == nil {
		return conn
	}
	if state.readLimiter != nil || state.writeLimiter != nil {
		conn = &rateLimitConn{
			Conn:         conn,
			readLimiter:  state.readLimiter,
			writeLimiter: state.writeLimiter,
			ctx:          ctx,
		}
	}
	key := UserKey{InboundTag: metadata.Inbound, UserName: metadata.User}
	m.addConn(key, conn)
	wrapped := &quotaConn{
		Conn:    conn,
		manager: m,
		key:     key,
	}
	return bufio.NewInt64CounterConn(wrapped, []*atomic.Int64{&state.Uplink}, []*atomic.Int64{&state.Downlink})
}

func (m *Manager) RoutedPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) N.PacketConn {
	state := m.userState(metadata.Inbound, metadata.User)
	if state == nil {
		return conn
	}
	return bufio.NewInt64CounterPacketConn(conn, []*atomic.Int64{&state.Uplink}, nil, []*atomic.Int64{&state.Downlink}, nil)
}

func (m *Manager) Snapshot(inboundTag, userName string) (UserSnapshot, bool) {
	state := m.userState(inboundTag, userName)
	if state == nil {
		return UserSnapshot{}, false
	}
	return state.snapshot(), true
}

func (m *Manager) Snapshots() []UserSnapshot {
	m.access.RLock()
	keys := make([]UserKey, 0, len(m.users))
	for k := range m.users {
		keys = append(keys, k)
	}
	m.access.RUnlock()
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].InboundTag != keys[j].InboundTag {
			return keys[i].InboundTag < keys[j].InboundTag
		}
		return keys[i].UserName < keys[j].UserName
	})
	snapshots := make([]UserSnapshot, 0, len(keys))
	for _, k := range keys {
		state := m.userState(k.InboundTag, k.UserName)
		if state != nil {
			snapshots = append(snapshots, state.snapshot())
		}
	}
	return snapshots
}

func (m *Manager) Reset(inboundTag, userName string) bool {
	state := m.userState(inboundTag, userName)
	if state == nil {
		return false
	}
	state.Uplink.Store(0)
	state.Downlink.Store(0)
	return true
}

func (m *Manager) userState(inboundTag, userName string) *UserState {
	if inboundTag == "" || userName == "" {
		return nil
	}
	m.access.RLock()
	defer m.access.RUnlock()
	return m.users[UserKey{InboundTag: inboundTag, UserName: userName}]
}

func (m *Manager) checkUser(inboundTag, userName string) error {
	state := m.userState(inboundTag, userName)
	if state == nil || !state.blocked() {
		return nil
	}
	return &R.RejectedError{Cause: E.New("user quota exceeded: ", inboundTag, "/", userName)}
}

func (s *UserState) snapshot() UserSnapshot {
	uplink := s.Uplink.Load()
	downlink := s.Downlink.Load()
	used := uplink + downlink
	remaining := s.QuotaBytes - used
	if remaining < 0 {
		remaining = 0
	}
	return UserSnapshot{
		InboundTag:     s.Key.InboundTag,
		UserName:       s.Key.UserName,
		UplinkBytes:    uplink,
		DownlinkBytes:  downlink,
		UsedBytes:      used,
		QuotaBytes:     s.QuotaBytes,
		RemainingBytes: remaining,
		Blocked:        used >= s.QuotaBytes,
		RateLimitRead:  s.RateLimitRead,
		RateLimitWrite: s.RateLimitWrite,
	}
}

func (s *UserState) blocked() bool {
	return s.Uplink.Load()+s.Downlink.Load() >= s.QuotaBytes
}
