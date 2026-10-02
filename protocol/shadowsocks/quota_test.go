package shadowsocks

import (
	"encoding/json"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/byteformats"
)

func mustMemoryBytes(t *testing.T, value string) *byteformats.MemoryBytes {
	t.Helper()
	var bytes byteformats.MemoryBytes
	if err := json.Unmarshal([]byte(`"`+value+`"`), &bytes); err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return &bytes
}

func TestMultiInboundQuotaUsers(t *testing.T) {
	t.Parallel()
	inbound := &MultiInbound{
		users: []option.ShadowsocksUser{
			{Name: "alice", QuotaBytes: mustMemoryBytes(t, "500MB")},
			{Name: "bob", Admin: true},
			{Name: "carol", QuotaBytes: mustMemoryBytes(t, "1GB"), Admin: true},
			{Name: "dave"}, // no quota, not admin -> excluded
		},
	}

	got := inbound.QuotaUsers()

	want := []adapter.QuotaUser{
		{Name: "alice", QuotaBytes: 500 * 1024 * 1024},
		{Name: "bob", QuotaBytes: 0, Admin: true},
		{Name: "carol", QuotaBytes: 1024 * 1024 * 1024, Admin: true},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d quota users, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("quota user %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMultiInboundQuotaUsersEmpty(t *testing.T) {
	t.Parallel()
	inbound := &MultiInbound{
		users: []option.ShadowsocksUser{
			{Name: "alice"},
			{Name: "bob"},
		},
	}
	if got := inbound.QuotaUsers(); got != nil {
		t.Fatalf("expected no quota users, got %+v", got)
	}
}

// Ensure the inbound is discoverable by the quota service.
var _ adapter.QuotaUserProvider = (*MultiInbound)(nil)
