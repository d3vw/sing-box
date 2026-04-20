package quota

import (
	"context"
	"net"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
)

type Manager struct {
	access   sync.RWMutex
	inbounds map[string]*InboundState
}

type InboundState struct {
	Tag        string
	QuotaBytes int64
	Uplink     atomic.Int64
	Downlink   atomic.Int64
}

type InboundSnapshot struct {
	Tag            string `json:"tag"`
	UplinkBytes    int64  `json:"uplink_bytes"`
	DownlinkBytes  int64  `json:"downlink_bytes"`
	UsedBytes      int64  `json:"used_bytes"`
	QuotaBytes     int64  `json:"quota_bytes"`
	RemainingBytes int64  `json:"remaining_bytes"`
	Blocked        bool   `json:"blocked"`
}

func NewManager(inbounds map[string]option.QuotaInboundOptions) (*Manager, error) {
	manager := &Manager{inbounds: make(map[string]*InboundState, len(inbounds))}
	for tag, inboundOptions := range inbounds {
		if tag == "" {
			return nil, E.New("empty inbound tag")
		}
		if inboundOptions.QuotaBytes == nil {
			return nil, E.New("missing quota_bytes for inbound ", tag)
		}
		quotaBytes := int64(inboundOptions.QuotaBytes.Value())
		if quotaBytes <= 0 {
			return nil, E.New("invalid quota_bytes for inbound ", tag)
		}
		manager.inbounds[tag] = &InboundState{
			Tag:        tag,
			QuotaBytes: quotaBytes,
		}
	}
	return manager, nil
}

func (m *Manager) AddInbound(tag string, quotaBytes int64) {
	m.access.Lock()
	defer m.access.Unlock()
	if _, exists := m.inbounds[tag]; exists {
		return
	}
	m.inbounds[tag] = &InboundState{
		Tag:        tag,
		QuotaBytes: quotaBytes,
	}
}

func (m *Manager) CheckConnection(ctx context.Context, metadata adapter.InboundContext) error {
	return m.check(metadata.Inbound)
}

func (m *Manager) CheckPacketConnection(ctx context.Context, metadata adapter.InboundContext) error {
	return m.check(metadata.Inbound)
}

func (m *Manager) RoutedConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) net.Conn {
	state := m.state(metadata.Inbound)
	if state == nil {
		return conn
	}
	return bufio.NewInt64CounterConn(conn, []*atomic.Int64{&state.Uplink}, []*atomic.Int64{&state.Downlink})
}

func (m *Manager) RoutedPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) N.PacketConn {
	state := m.state(metadata.Inbound)
	if state == nil {
		return conn
	}
	return bufio.NewInt64CounterPacketConn(conn, []*atomic.Int64{&state.Uplink}, nil, []*atomic.Int64{&state.Downlink}, nil)
}

func (m *Manager) Snapshot(tag string) (InboundSnapshot, bool) {
	state := m.state(tag)
	if state == nil {
		return InboundSnapshot{}, false
	}
	return state.snapshot(), true
}

func (m *Manager) Snapshots() []InboundSnapshot {
	m.access.RLock()
	defer m.access.RUnlock()
	keys := make([]string, 0, len(m.inbounds))
	for tag := range m.inbounds {
		keys = append(keys, tag)
	}
	sort.Strings(keys)
	snapshots := make([]InboundSnapshot, 0, len(keys))
	for _, tag := range keys {
		snapshots = append(snapshots, m.inbounds[tag].snapshot())
	}
	return snapshots
}

func (m *Manager) Reset(tag string) bool {
	state := m.state(tag)
	if state == nil {
		return false
	}
	state.Uplink.Store(0)
	state.Downlink.Store(0)
	return true
}

func (m *Manager) state(tag string) *InboundState {
	if tag == "" {
		return nil
	}
	m.access.RLock()
	defer m.access.RUnlock()
	return m.inbounds[tag]
}

func (m *Manager) check(tag string) error {
	state := m.state(tag)
	if state == nil || !state.blocked() {
		return nil
	}
	return &R.RejectedError{Cause: E.New("inbound quota exceeded: ", tag)}
}

func (s *InboundState) snapshot() InboundSnapshot {
	uplink := s.Uplink.Load()
	downlink := s.Downlink.Load()
	used := uplink + downlink
	remaining := s.QuotaBytes - used
	if remaining < 0 {
		remaining = 0
	}
	return InboundSnapshot{
		Tag:            s.Tag,
		UplinkBytes:    uplink,
		DownlinkBytes:  downlink,
		UsedBytes:      used,
		QuotaBytes:     s.QuotaBytes,
		RemainingBytes: remaining,
		Blocked:        used >= s.QuotaBytes,
	}
}

func (s *InboundState) blocked() bool {
	return s.Uplink.Load()+s.Downlink.Load() >= s.QuotaBytes
}
