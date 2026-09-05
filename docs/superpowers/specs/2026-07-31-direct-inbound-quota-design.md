# Design: Traffic quota for user-less inbounds (starting with `direct`)

## Problem

The quota system (`service/quota`) tracks traffic per `(InboundTag, UserName)`
pair. `UserName` comes from `metadata.User`, which is only ever set by
inbounds that authenticate individual users (shadowsocks multi-user, vless,
vmess, trojan, naive, anytls) via their `QuotaUserProvider.QuotaUsers()`
implementation.

Inbounds with no user concept — e.g. a `direct` inbound used as a transparent
redirect (`network: "udp"`, `override_address`/`override_port` pointing UDP:53
at `1.0.0.1`) — always produce `metadata.User == ""`. `Manager.userState()`
explicitly returns `nil` for empty `userName` (`service/quota/manager.go:231-238`),
so no quota can ever be attached to this traffic today, no matter how it's
configured.

Goal: let an inbound with no per-connection user identity have a single
aggregate traffic budget for its entire inbound, using the existing quota
infrastructure (tracking, blocking, snapshot API, admin portal listing)
without duplicating it.

## Non-goals

- Per-user self-service portal access for anonymous inbounds. There is no
  authentication step on a `direct` inbound, so there is no "self" to log in
  as — anyone reaching the inbound would see the same aggregate page with no
  access control. Visibility for this feature is admin-only, via the existing
  JSON API (`GET /quota/v1/users`) and the admin view of the portal
  (`renderAll`), both of which already list every registered `UserState`
  with no code changes required.
- Changing `service/quota/portal.go`'s lookup logic. It resolves users from
  `metadata.User` as read from already-authenticated protocol inbounds; that
  path is out of scope.
- Rolling the config field out to every user-less inbound type (`tun`,
  `mixed`, `redirect`, `tproxy`, unauthenticated `socks`/`http`, ...) in this
  pass. The mechanism is designed to be reusable for those later with the
  same few lines of wiring, but only `direct` is implemented now.

## Design

### 1. Reserved sentinel user name

Add a constant to `adapter/inbound.go`:

```go
// QuotaInboundUser is the reserved UserKey.UserName used to track quota for
// an entire inbound that has no per-connection user identity (metadata.User
// is empty), e.g. a direct inbound used for transparent redirection.
const QuotaInboundUser = "*"
```

Convention: any `QuotaUserProvider` implementation on an inbound with no real
per-user identity reports its aggregate budget under `Name: QuotaInboundUser`.
`"*"` is treated as reserved; a real per-user protocol naming a user `"*"`
would collide with this aggregate entry. This is not validated against — it's
an extremely unlikely name choice and out of scope to guard against.

### 2. `direct` inbound config

`option/direct.go`:

```go
type DirectInboundOptions struct {
    ListenOptions
    Network         NetworkList              `json:"network,omitempty"`
    OverrideAddress string                   `json:"override_address,omitempty"`
    OverridePort    uint16                   `json:"override_port,omitempty"`
    QuotaBytes      *byteformats.MemoryBytes `json:"quota_bytes,omitempty"`
}
```

Same field name/type convention as `option.ShadowsocksUser.QuotaBytes` —
supports human-readable values like `"100GiB"`. Omitted or zero means no
quota is enforced (current behavior, unchanged).

### 3. `direct` inbound wiring

`protocol/direct/inbound.go`:

- `Inbound` struct gets a `quotaBytes int64` field, set in `NewInbound` from
  `options.QuotaBytes.Value()` if non-nil.
- `Inbound` implements `adapter.QuotaUserProvider`:

```go
func (i *Inbound) QuotaUsers() []adapter.QuotaUser {
    if i.quotaBytes <= 0 {
        return nil
    }
    return []adapter.QuotaUser{{Name: adapter.QuotaInboundUser, QuotaBytes: i.quotaBytes}}
}
```

No changes needed in `service/quota/service.go`'s `Start()` — its existing
auto-discovery loop over `QuotaUserProvider` inbounds already picks this up
and registers it as `UserKey{InboundTag: "direct-in", UserName: "*"}`.

### 4. `Manager` lookup fallback

`service/quota/manager.go` gets one small helper, used only on the live
traffic path (not by `userState()` itself, which other callers — the API
handlers, the portal's authenticated-user lookups — call directly with a
real, known-non-empty user name):

```go
// trafficState resolves the UserState for a connection's inbound/user pair,
// falling back to the inbound's aggregate quota (adapter.QuotaInboundUser)
// when the inbound has no per-connection user identity.
func (m *Manager) trafficState(inboundTag, userName string) *UserState {
    if userName == "" {
        userName = adapter.QuotaInboundUser
    }
    return m.userState(inboundTag, userName)
}
```

Three call sites switch from `m.userState(...)` to `m.trafficState(...)`:

- `checkUser` (used by both `CheckConnection` and `CheckPacketConnection`)
- `RoutedConnection`
- `RoutedPacketConnection`

Behavior preserved for existing authenticated protocols: when `userName` is
non-empty but not registered (e.g. a shadowsocks user with no `quota_bytes`
configured), `trafficState` behaves exactly as `userState` did — returns
`nil`, no blocking. The fallback to `"*"` only triggers when the inbound
never had a user identity to begin with.

Blocking semantics are unchanged: once aggregate usage reaches the
configured `quota_bytes`, `checkUser` returns the same
`R.RejectedError` used for per-user quota, for both new TCP connections and
new UDP flows through the inbound.

### 5. Visibility

No code changes required beyond what's above:

- `GET /quota/v1/users` returns `Manager.Snapshots()`, which will include a
  `{InboundTag: "direct-in", UserName: "*", ...}` entry once traffic flows.
- `GET /quota/v1/inbounds/direct-in/users/*` and the matching `POST .../reset`
  work through the existing chi route, since `"*"` is just a normal path
  segment value passed to the existing per-user handlers.
- The admin view of the quota portal (`portalOutbound.renderAll`) iterates
  all snapshots unconditionally and will render this row alongside real
  per-user rows for anyone who reaches it as an admin.
- `UserState.SubscriptionURI()` naturally returns `ok=false` for this entry
  (no `Method` configured), so no bogus `ss://` link or QR code is rendered
  for it.

## Testing

- `service/quota/manager_test.go`: add cases for `trafficState`/`checkUser`
  covering: empty `userName` resolves to `"*"`; blocking triggers once
  aggregate usage crosses `quota_bytes`; a real, non-empty `userName` with no
  registered state is still unaffected (no accidental fallback).
- `protocol/direct`: unit test that `QuotaUsers()` returns `nil` for
  `quota_bytes` unset/zero, and the expected single `QuotaInboundUser` entry
  when set.
- Manual/integration: configure a `direct` inbound with a small `quota_bytes`
  and a `quota` service, drive traffic past the limit, confirm new
  connections are rejected and `GET /quota/v1/users` reports the `"*"` entry
  as blocked.
