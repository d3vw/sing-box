package direct

import (
	"encoding/json"
	"testing"

	"github.com/sagernet/sing-box/adapter"
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

func TestInboundQuotaUsers(t *testing.T) {
	t.Parallel()
	inbound := &Inbound{quotaBytes: mustMemoryBytes(t, "500MB")}

	got := inbound.QuotaUsers()
	want := []adapter.QuotaUser{{Name: adapter.QuotaInboundUser, QuotaBytes: 500 * 1024 * 1024}}

	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestInboundQuotaUsersUnset(t *testing.T) {
	t.Parallel()
	inbound := &Inbound{}
	if got := inbound.QuotaUsers(); got != nil {
		t.Fatalf("expected no quota users, got %+v", got)
	}
}

func TestInboundQuotaUsersZero(t *testing.T) {
	t.Parallel()
	inbound := &Inbound{quotaBytes: mustMemoryBytes(t, "0B")}
	if got := inbound.QuotaUsers(); got != nil {
		t.Fatalf("expected no quota users for a zero quota, got %+v", got)
	}
}

// Ensure the inbound is discoverable by the quota service.
var _ adapter.QuotaUserProvider = (*Inbound)(nil)
