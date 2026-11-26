package quota

import (
	"net"
	"net/netip"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	N "github.com/sagernet/sing/common/network"
)

// connClosedLimit is how many closed connections each user keeps in memory;
// older ones drop off, closed_total keeps counting.
const connClosedLimit = 500

// ConnRecord describes one routed connection of a user, like the
// connections in sing-box's API.
type ConnRecord struct {
	ID       string `json:"id"`
	Network  string `json:"network"`
	Protocol string `json:"protocol,omitempty"`
	// Host is the sniffed domain, else the destination's domain or address.
	Host     string `json:"host"`
	Dest     string `json:"dest"`
	Outbound string `json:"outbound,omitempty"`
	Rule     string `json:"rule,omitempty"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
	// CreatedAt and ClosedAt are Unix milliseconds.
	CreatedAt int64 `json:"created_at"`
	ClosedAt  int64 `json:"closed_at,omitempty"`
	Active    bool  `json:"active"`
}

// ConnSnapshot is a user's connections at one moment: the active ones
// newest first, and the most recently closed ones newest first.
type ConnSnapshot struct {
	// Now is the server's clock in Unix milliseconds, to age the
	// connections by.
	Now         int64 `json:"now"`
	ActiveCount int   `json:"active_count"`
	ClosedTotal int64 `json:"closed_total"`
	// Upload and Download sum the active connections.
	Upload   int64        `json:"upload"`
	Download int64        `json:"download"`
	Active   []ConnRecord `json:"active,omitempty"`
	Closed   []ConnRecord `json:"closed,omitempty"`
}

// trackedConn is one connection in the store. Hidden connections (the
// portal's own and DNS) can still be closed with the user's other
// connections but are left out of what the user sees.
type trackedConn struct {
	record   ConnRecord
	upload   atomic.Int64
	download atomic.Int64
	hidden   bool
	key     UserKey
	store   *connStore
	closer  func() error
	once    sync.Once
}

func (c *trackedConn) snapshot() ConnRecord {
	record := c.record
	record.Upload = c.upload.Load()
	record.Download = c.download.Load()
	return record
}

// leave records the connection as closed, once.
func (c *trackedConn) leave() {
	c.once.Do(func() { c.store.leave(c) })
}

// Close closes the underlying connection, which in turn leaves the store.
func (c *trackedConn) Close() error {
	if c.closer == nil {
		c.leave()
		return nil
	}
	return c.closer()
}

type userConns struct {
	active map[*trackedConn]struct{}
	// closed is a ring of up to connClosedLimit records; next is where the
	// next one goes once it is full.
	closed      []ConnRecord
	next        int
	closedTotal int64
	subscribers map[chan struct{}]struct{}
}

type connStore struct {
	access sync.Mutex
	users  map[UserKey]*userConns
	nextID atomic.Uint64
}

func newConnStore() *connStore {
	return &connStore{users: make(map[UserKey]*userConns)}
}

func (s *connStore) user(key UserKey) *userConns {
	u := s.users[key]
	if u == nil {
		u = &userConns{active: make(map[*trackedConn]struct{}), subscribers: make(map[chan struct{}]struct{})}
		s.users[key] = u
	}
	return u
}

func (s *connStore) newConn(key UserKey, record ConnRecord, hidden bool) *trackedConn {
	record.ID = strconv.FormatUint(s.nextID.Add(1), 10)
	record.Active = true
	if record.CreatedAt == 0 {
		record.CreatedAt = time.Now().UnixMilli()
	}
	return &trackedConn{record: record, hidden: hidden, key: key, store: s}
}

func (s *connStore) join(c *trackedConn) {
	s.access.Lock()
	u := s.user(c.key)
	u.active[c] = struct{}{}
	if !c.hidden {
		u.notify()
	}
	s.access.Unlock()
}

func (s *connStore) leave(c *trackedConn) {
	s.access.Lock()
	defer s.access.Unlock()
	u := s.users[c.key]
	if u == nil {
		return
	}
	if _, ok := u.active[c]; !ok {
		return
	}
	delete(u.active, c)
	if c.hidden {
		return
	}
	u.closedTotal++
	record := c.snapshot()
	record.Active = false
	record.ClosedAt = time.Now().UnixMilli()
	if len(u.closed) < connClosedLimit {
		u.closed = append(u.closed, record)
	} else {
		u.closed[u.next] = record
		u.next = (u.next + 1) % connClosedLimit
	}
	u.notify()
}

func (u *userConns) notify() {
	for ch := range u.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// subscribe returns a channel signalled whenever one of the user's visible
// connections opens or closes, and a function to stop.
func (s *connStore) subscribe(key UserKey) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.access.Lock()
	s.user(key).subscribers[ch] = struct{}{}
	s.access.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.access.Lock()
			if u := s.users[key]; u != nil {
				delete(u.subscribers, ch)
			}
			s.access.Unlock()
		})
	}
}

func (s *connStore) activeCount(key UserKey) int {
	s.access.Lock()
	defer s.access.Unlock()
	u := s.users[key]
	if u == nil {
		return 0
	}
	n := 0
	for c := range u.active {
		if !c.hidden {
			n++
		}
	}
	return n
}

// snapshot returns the user's connections; with detail false it only
// counts them.
func (s *connStore) snapshot(key UserKey, detail bool) ConnSnapshot {
	snapshot := ConnSnapshot{Now: time.Now().UnixMilli()}
	s.access.Lock()
	u := s.users[key]
	if u == nil {
		s.access.Unlock()
		return snapshot
	}
	active := make([]*trackedConn, 0, len(u.active))
	for c := range u.active {
		if !c.hidden {
			active = append(active, c)
		}
	}
	snapshot.ActiveCount, snapshot.ClosedTotal = len(active), u.closedTotal
	if detail {
		snapshot.Closed = make([]ConnRecord, 0, len(u.closed))
		for i := len(u.closed) - 1; i >= 0; i-- {
			snapshot.Closed = append(snapshot.Closed, u.closed[(u.next+i)%len(u.closed)])
		}
	}
	s.access.Unlock()

	records := make([]ConnRecord, 0, len(active))
	for _, c := range active {
		record := c.snapshot()
		snapshot.Upload += record.Upload
		snapshot.Download += record.Download
		records = append(records, record)
	}
	if detail {
		sort.Slice(records, func(i, j int) bool {
			if records[i].CreatedAt != records[j].CreatedAt {
				return records[i].CreatedAt > records[j].CreatedAt
			}
			a, _ := strconv.ParseUint(records[i].ID, 10, 64)
			b, _ := strconv.ParseUint(records[j].ID, 10, 64)
			return a > b
		})
		snapshot.Active = records
	}
	return snapshot
}

// closeAll closes the user's connections, hidden ones included when all is
// set; it returns how many it closed.
func (s *connStore) closeAll(key UserKey, all bool) int {
	s.access.Lock()
	var list []*trackedConn
	if u := s.users[key]; u != nil {
		for c := range u.active {
			if all || !c.hidden {
				list = append(list, c)
			}
		}
	}
	s.access.Unlock()
	for _, c := range list {
		_ = c.Close()
	}
	return len(list)
}

// closeOne closes one of the user's visible connections by ID.
func (s *connStore) closeOne(key UserKey, id string) bool {
	s.access.Lock()
	var target *trackedConn
	if u := s.users[key]; u != nil {
		for c := range u.active {
			if !c.hidden && c.record.ID == id {
				target = c
				break
			}
		}
	}
	s.access.Unlock()
	if target == nil {
		return false
	}
	_ = target.Close()
	return true
}

// portalHooks lets the portal outbound tell the manager which destinations
// are the portal itself, and which outbound a member's traffic really goes
// out through.
type portalHooks struct {
	addrs    []netip.Addr
	outbound func(inboundTag, userName string) (string, bool)
}

// SetPortal registers the portal's addresses, so connections to the portal
// itself (the page, /probe, /connections) are not shown as the user's, and
// how to name a member's custom outbound.
func (m *Manager) SetPortal(addrs []netip.Addr, outbound func(inboundTag, userName string) (string, bool)) {
	m.portal.Store(&portalHooks{addrs: addrs, outbound: outbound})
}

func (m *Manager) isPortalAddr(addr netip.Addr) bool {
	hooks := m.portal.Load()
	if hooks == nil || !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	for _, pa := range hooks.addrs {
		if pa.Unmap() == addr {
			return true
		}
	}
	return false
}

// newTrackedConn describes a routed connection; it is hidden when it goes
// to the portal itself or to DNS.
func (m *Manager) newTrackedConn(state *UserState, network string, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) *trackedConn {
	hidden := m.isPortalAddr(metadata.Destination.Addr)
	record := ConnRecord{
		Network:  network,
		Protocol: metadata.Protocol,
		Host:     connHost(metadata),
		Dest:     metadata.Destination.String(),
		Rule:     "final",
	}
	if matchedRule != nil {
		record.Rule = matchedRule.String()
	}
	if matchOutbound != nil {
		record.Outbound = matchOutbound.Tag()
		switch matchOutbound.Type() {
		case C.TypeDNS:
			hidden = true
		case C.TypeQuotaPortal:
			if hooks := m.portal.Load(); hooks != nil && hooks.outbound != nil {
				if tag, ok := hooks.outbound(metadata.Inbound, metadata.User); ok {
					record.Outbound = tag
				}
			}
		}
	}
	return m.conns.newConn(state.Key, record, hidden)
}

func connHost(metadata adapter.InboundContext) string {
	if metadata.Domain != "" {
		return metadata.Domain
	}
	if metadata.Destination.Fqdn != "" {
		return metadata.Destination.Fqdn
	}
	if metadata.Destination.Addr.IsValid() {
		return metadata.Destination.Addr.Unmap().String()
	}
	return metadata.Destination.String()
}

// UserConnections returns a user's active and recently closed connections.
func (m *Manager) UserConnections(inboundTag, userName string) (ConnSnapshot, bool) {
	state := m.trafficState(inboundTag, userName)
	if state == nil {
		return ConnSnapshot{}, false
	}
	return m.conns.snapshot(state.Key, true), true
}

// CloseUserConnection closes one of a user's active connections.
func (m *Manager) CloseUserConnection(inboundTag, userName, id string) bool {
	state := m.trafficState(inboundTag, userName)
	if state == nil {
		return false
	}
	return m.conns.closeOne(state.Key, id)
}

// CloseUserActiveConnections closes a user's visible active connections and
// returns how many.
func (m *Manager) CloseUserActiveConnections(inboundTag, userName string) int {
	state := m.trafficState(inboundTag, userName)
	if state == nil {
		return 0
	}
	return m.conns.closeAll(state.Key, false)
}

// CloseUserConnections closes all of a user's connections, the portal's own
// included, so they reconnect through a changed outbound.
func (m *Manager) CloseUserConnections(inboundTag, userName string) {
	m.conns.closeAll(UserKey{InboundTag: inboundTag, UserName: userName}, true)
}

type quotaConn struct {
	net.Conn
	tracked *trackedConn
}

func (c *quotaConn) Close() error {
	c.tracked.leave()
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

type quotaPacketConn struct {
	N.PacketConn
	tracked *trackedConn
}

func (c *quotaPacketConn) Close() error {
	c.tracked.leave()
	return c.PacketConn.Close()
}

func (c *quotaPacketConn) Upstream() any {
	return c.PacketConn
}

func (c *quotaPacketConn) ReaderReplaceable() bool {
	return true
}

func (c *quotaPacketConn) WriterReplaceable() bool {
	return true
}
