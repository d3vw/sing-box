# Direct Inbound Aggregate Quota Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a `direct` inbound with no per-connection user identity (e.g. a transparent UDP redirect) enforce a single aggregate traffic budget, reusing the existing per-user quota infrastructure in `service/quota` instead of building a parallel system.

**Architecture:** Reserve a sentinel `UserName` (`adapter.QuotaInboundUser = "*"`) that represents "this inbound's aggregate traffic, no individual user." `service/quota/manager.go`'s traffic-path lookups fall back to this sentinel whenever `metadata.User == ""`. The `direct` inbound gets a `quota_bytes` option and implements the existing `adapter.QuotaUserProvider` interface, reporting a single entry under that sentinel name — the existing auto-discovery, tracking, blocking, snapshot API, and admin portal listing all pick it up with no further changes.

**Tech Stack:** Go, standard `testing` package (no testify in this codebase), existing `sing-box` quota service.

**Spec:** `docs/superpowers/specs/2026-07-31-direct-inbound-quota-design.md`

---

## Task 1: `Manager` fallback for user-less inbounds

**Files:**
- Modify: `adapter/inbound.go:39-41`
- Modify: `service/quota/manager.go`
- Test: `service/quota/manager_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `service/quota/manager_test.go`:

```go
package quota

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	R "github.com/sagernet/sing-box/route/rule"
)

func TestTrafficStateFallsBackToAggregateUser(t *testing.T) {
	m := NewManager()
	m.AddUser("direct-in", adapter.QuotaUser{Name: adapter.QuotaInboundUser, QuotaBytes: 100})

	state := m.trafficState("direct-in", "")
	if state == nil {
		t.Fatal("expected aggregate state for empty user name")
	}
	if state.Key.UserName != adapter.QuotaInboundUser {
		t.Fatalf("expected key user name %q, got %q", adapter.QuotaInboundUser, state.Key.UserName)
	}

	if got := m.trafficState("direct-in", "someone-else"); got != nil {
		t.Fatalf("expected no state for unregistered named user, got %+v", got)
	}
}

func TestCheckUserBlocksOnAggregateQuota(t *testing.T) {
	m := NewManager()
	m.AddUser("direct-in", adapter.QuotaUser{Name: adapter.QuotaInboundUser, QuotaBytes: 10})

	if err := m.checkUser("direct-in", ""); err != nil {
		t.Fatalf("expected no error under quota, got %v", err)
	}

	state := m.userState("direct-in", adapter.QuotaInboundUser)
	state.Uplink.Store(10)

	err := m.checkUser("direct-in", "")
	if err == nil {
		t.Fatal("expected quota exceeded error")
	}
	if !R.IsRejected(err) {
		t.Fatalf("expected a rejected error, got %v (%T)", err, err)
	}
}

func TestCheckUserRealUserWithoutQuotaNotBlockedByAggregate(t *testing.T) {
	m := NewManager()
	m.AddUser("mix-in", adapter.QuotaUser{Name: adapter.QuotaInboundUser, QuotaBytes: 1})
	m.userState("mix-in", adapter.QuotaInboundUser).Uplink.Store(1) // aggregate already blocked

	if err := m.checkUser("mix-in", "someone"); err != nil {
		t.Fatalf("named user with no registered quota must be unaffected by the aggregate block, got %v", err)
	}
}

func TestRoutedConnectionTracksAggregateUser(t *testing.T) {
	m := NewManager()
	m.AddUser("direct-in", adapter.QuotaUser{Name: adapter.QuotaInboundUser, QuotaBytes: 1000})

	client, server := net.Pipe()
	defer client.Close()

	metadata := adapter.InboundContext{Inbound: "direct-in", User: ""}
	wrapped := m.RoutedConnection(context.Background(), server, metadata, nil, nil)
	if wrapped == server {
		t.Fatal("expected the connection to be wrapped for aggregate quota tracking")
	}

	done := make(chan struct{})
	go func() {
		buf := make([]byte, 5)
		_, _ = io.ReadFull(client, buf)
		close(done)
	}()
	if _, err := wrapped.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	<-done

	state := m.userState("direct-in", adapter.QuotaInboundUser)
	if used := state.Uplink.Load() + state.Downlink.Load(); used != 5 {
		t.Fatalf("expected 5 tracked bytes, got %d", used)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./service/quota/... -run 'TestTrafficStateFallsBackToAggregateUser|TestCheckUserBlocksOnAggregateQuota|TestCheckUserRealUserWithoutQuotaNotBlockedByAggregate|TestRoutedConnectionTracksAggregateUser' -v`

Expected: build failure — `adapter.QuotaInboundUser` and `m.trafficState` are undefined.

- [ ] **Step 3: Add the reserved sentinel constant**

In `adapter/inbound.go`, right after the `QuotaUserProvider` interface (currently lines 39-41):

```go
type QuotaUserProvider interface {
	QuotaUsers() []QuotaUser
}

// QuotaInboundUser is the reserved UserKey.UserName used to track quota for
// an entire inbound that has no per-connection user identity (metadata.User
// is empty), e.g. a direct inbound used for transparent redirection.
const QuotaInboundUser = "*"
```

- [ ] **Step 4: Add the `trafficState` fallback and switch the traffic-path call sites**

In `service/quota/manager.go`, add the import (the file already imports `"github.com/sagernet/sing-box/adapter"`, so no new import line is needed).

Add this method near `userState` (after it, around line 238):

```go
// trafficState resolves the UserState for a connection's inbound/user pair,
// falling back to the inbound's aggregate quota (adapter.QuotaInboundUser)
// when the inbound has no per-connection user identity. userState itself is
// left untouched since other callers (the HTTP API, the portal's
// already-authenticated-user lookups) pass a real, known user name and must
// not silently fall back to the aggregate entry.
func (m *Manager) trafficState(inboundTag, userName string) *UserState {
	if userName == "" {
		userName = adapter.QuotaInboundUser
	}
	return m.userState(inboundTag, userName)
}
```

Change `checkUser` (currently):

```go
func (m *Manager) checkUser(inboundTag, userName string) error {
	state := m.userState(inboundTag, userName)
	if state == nil || !state.blocked() {
		return nil
	}
	return &R.RejectedError{Cause: E.New("user quota exceeded: ", inboundTag, "/", userName)}
}
```

to:

```go
func (m *Manager) checkUser(inboundTag, userName string) error {
	state := m.trafficState(inboundTag, userName)
	if state == nil || !state.blocked() {
		return nil
	}
	return &R.RejectedError{Cause: E.New("user quota exceeded: ", inboundTag, "/", userName)}
}
```

Change `RoutedConnection` (currently):

```go
func (m *Manager) RoutedConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) net.Conn {
	state := m.userState(metadata.Inbound, metadata.User)
```

to:

```go
func (m *Manager) RoutedConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) net.Conn {
	state := m.trafficState(metadata.Inbound, metadata.User)
```

Change `RoutedPacketConnection` (currently):

```go
func (m *Manager) RoutedPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) N.PacketConn {
	state := m.userState(metadata.Inbound, metadata.User)
```

to:

```go
func (m *Manager) RoutedPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) N.PacketConn {
	state := m.trafficState(metadata.Inbound, metadata.User)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./service/quota/... -v -run 'TestTrafficStateFallsBackToAggregateUser|TestCheckUserBlocksOnAggregateQuota|TestCheckUserRealUserWithoutQuotaNotBlockedByAggregate|TestRoutedConnectionTracksAggregateUser'`

Expected: `PASS` for all four, `ok  	github.com/sagernet/sing-box/service/quota`

- [ ] **Step 6: Run the full existing quota test suite to check for regressions, and verify formatting**

Run: `go test ./service/quota/... -v && gofmt -l adapter/inbound.go service/quota/manager.go service/quota/manager_test.go`

Expected: all existing tests (including `service/quota/portal_test.go`) still pass, no failures introduced by the `userState` → `trafficState` switch; `gofmt -l` prints nothing (no files need reformatting).

- [ ] **Step 7: Commit**

```bash
git add adapter/inbound.go service/quota/manager.go service/quota/manager_test.go
git commit -m "$(cat <<'EOF'
feat(quota): add aggregate quota fallback for user-less inbounds

Manager.trafficState resolves metadata.User == "" to the reserved
adapter.QuotaInboundUser sentinel, so inbounds with no per-connection
user identity (like a direct redirect) can register a single
whole-inbound budget via the existing QuotaUserProvider mechanism.
EOF
)"
```

---

## Task 2: `direct` inbound `quota_bytes` option

**Files:**
- Modify: `option/direct.go`
- Modify: `protocol/direct/inbound.go`
- Test: `protocol/direct/quota_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `protocol/direct/quota_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./protocol/direct/... -v -run TestInboundQuotaUsers`

Expected: build failure — `Inbound.quotaBytes` field and `QuotaUsers` method don't exist yet.

- [ ] **Step 3: Add the `quota_bytes` option field**

In `option/direct.go`, add the `byteformats` import and the new field:

```go
package option

import (
	"context"

	"github.com/sagernet/sing/common/byteformats"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
)

type DirectInboundOptions struct {
	ListenOptions
	Network         NetworkList              `json:"network,omitempty"`
	OverrideAddress string                   `json:"override_address,omitempty"`
	OverridePort    uint16                   `json:"override_port,omitempty"`
	QuotaBytes      *byteformats.MemoryBytes `json:"quota_bytes,omitempty"`
}
```

- [ ] **Step 4: Wire the field and implement `QuotaUsers()` on the inbound**

In `protocol/direct/inbound.go`, add the import:

```go
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	"github.com/sagernet/sing/common/byteformats"
	M "github.com/sagernet/sing/common/metadata"
```

Add the field to the `Inbound` struct:

```go
type Inbound struct {
	inbound.Adapter
	ctx                 context.Context
	router              adapter.ConnectionRouterEx
	logger              log.ContextLogger
	listener            *listener.Listener
	udpNat              *udpnat.Service
	overrideOption      int
	overrideDestination M.Socksaddr
	quotaBytes          *byteformats.MemoryBytes
}
```

Set it in `NewInbound` (currently the struct literal is just `Adapter`/`ctx`/`router`/`logger`):

```go
	inbound := &Inbound{
		Adapter:    inbound.NewAdapter(C.TypeDirect, tag),
		ctx:        ctx,
		router:     router,
		logger:     logger,
		quotaBytes: options.QuotaBytes,
	}
```

Add the method (near `Close()`):

```go
func (i *Inbound) QuotaUsers() []adapter.QuotaUser {
	if i.quotaBytes == nil || i.quotaBytes.Value() == 0 {
		return nil
	}
	return []adapter.QuotaUser{{Name: adapter.QuotaInboundUser, QuotaBytes: int64(i.quotaBytes.Value())}}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./protocol/direct/... -v`

Expected: `PASS` for `TestInboundQuotaUsers`, `TestInboundQuotaUsersUnset`, `TestInboundQuotaUsersZero`; `ok  	github.com/sagernet/sing-box/protocol/direct`

- [ ] **Step 6: Build the affected packages to catch wiring mistakes, and verify formatting**

Run: `go build ./option/... ./protocol/direct/... ./service/quota/... ./adapter/... && gofmt -l option/direct.go protocol/direct/inbound.go protocol/direct/quota_test.go`

Expected: no output, exit code 0 (both the build and `gofmt -l` produce no output on success).

- [ ] **Step 7: Commit**

```bash
git add option/direct.go protocol/direct/inbound.go protocol/direct/quota_test.go
git commit -m "$(cat <<'EOF'
feat(direct): support quota_bytes for whole-inbound traffic budgets

direct inbounds have no per-connection user identity, so per-user
quota_bytes (as used by shadowsocks/vless/etc.) doesn't apply. This
adds a single quota_bytes option covering the inbound's aggregate
traffic, reported under the reserved adapter.QuotaInboundUser key
added in the previous commit.
EOF
)"
```

---

## Task 3: Manual end-to-end verification

**Files:**
- Create (scratch, not committed): `/tmp/claude-1000/-home-grey-Amateur-sing-box/186c9a91-6889-4e04-a353-929365305149/scratchpad/quota-direct-test.json`

This task has no automated test — it's a smoke test against the real built
binary, confirming the two units from Task 1 and Task 2 work together end to
end. Skip it if you don't have a way to run a local process bound to
`127.0.0.1` in this environment; the unit tests in Tasks 1-2 already cover
the logic in isolation.

- [ ] **Step 1: Write a minimal test config**

Create `/tmp/claude-1000/-home-grey-Amateur-sing-box/186c9a91-6889-4e04-a353-929365305149/scratchpad/quota-direct-test.json`:

```json
{
	"log": { "level": "debug", "timestamp": true },
	"inbounds": [
		{
			"type": "direct",
			"tag": "direct-in",
			"listen": "127.0.0.1",
			"listen_port": 15353,
			"network": "udp",
			"override_address": "1.1.1.1",
			"override_port": 53,
			"quota_bytes": "200B"
		}
	],
	"outbounds": [
		{ "type": "direct", "tag": "direct-out" }
	],
	"services": [
		{
			"type": "quota",
			"tag": "quota",
			"listen": "127.0.0.1",
			"listen_port": 18080
		}
	]
}
```

- [ ] **Step 2: Build the binary**

Run: `make build`

Expected: `./sing-box` produced in the repo root (this target already includes
the `with_quota` build tag by default — see `release/DEFAULT_BUILD_TAGS_OTHERS`).

- [ ] **Step 3: Run it in the background**

Run: `./sing-box run -c /tmp/claude-1000/-home-grey-Amateur-sing-box/186c9a91-6889-4e04-a353-929365305149/scratchpad/quota-direct-test.json` with `run_in_background: true`

Expected: log line confirming the `direct-in` inbound and `quota` service started, no errors.

- [ ] **Step 4: Confirm the aggregate user is registered before any traffic**

Run: `curl -s http://127.0.0.1:18080/quota/v1/users`

Expected JSON contains an entry with `"inbound_tag":"direct-in","user_name":"*","quota_bytes":200,"blocked":false`.

- [ ] **Step 5: Send a DNS query under quota and confirm it's not blocked**

Run: `printf '\x12\x34\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00\x03www\x07example\x03com\x00\x00\x01\x00\x01' | timeout 2 nc -u -w1 127.0.0.1 15353 | xxd | head -5`

Expected: a DNS response is received (non-empty output), and
`curl -s http://127.0.0.1:18080/quota/v1/users` shows `used_bytes` > 0,
`"blocked":false` (well under the 200-byte budget from one query).

- [ ] **Step 6: Push usage past the budget and confirm blocking**

Run the same `nc` query from Step 5 several more times (5-10x is enough to
cross 200 bytes of combined uplink+downlink), then:

Run: `curl -s http://127.0.0.1:18080/quota/v1/users`

Expected: the `direct-in`/`*` entry shows `"blocked":true`, `remaining_bytes: 0`.

Run the `nc` query one more time.

Expected: no response within the timeout (the connection is rejected before
reaching the upstream), and the running `sing-box` log shows a line
containing `user quota exceeded: direct-in/*`.

- [ ] **Step 7: Stop the process and clean up**

Stop the background `sing-box run` process (it does not need to be committed
— it was only for manual verification). Delete the scratch config file if
desired; it's outside the repo and won't affect `git status`.

- [ ] **Step 8: Confirm working tree is clean of anything unintended**

Run: `git status`

Expected: only the changes from Task 1 and Task 2's commits; no stray files
from this manual verification step (the config lived in the scratchpad
directory, not the repo).
