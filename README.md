# sing-box

The universal proxy platform.

## Quick Start

This fork is intended to stay close to upstream while adding custom features.

Recommended install flow:

1. Install the official sing-box release first:

   ```bash
   curl -fsSL https://sing-box.app/install.sh | sh -s -- --beta
   ```

2. Replace the upstream `sing-box` binary with the custom binary from this fork.

That keeps the setup simple and makes upgrades easier: you keep the same config and only swap the executable.

If you want to install the custom binary from the latest release, use:

```bash
TAG=$(curl -fsSL https://api.github.com/repos/d3vw/sing-box/releases/latest | jq -r .tag_name)
curl -L -o sing-box-linux-amd64.tar.gz "https://github.com/d3vw/sing-box/releases/download/${TAG}/sing-box-linux-amd64.tar.gz"
tar -xzf sing-box-linux-amd64.tar.gz
sudo install -m 755 sing-box-linux-amd64 /usr/bin/sing-box
```

If you already know the tag, replace `${TAG}` with the version you want.

## Downloads

- Custom Linux amd64 builds are published from the `custom` branch.
- Release artifacts are attached to GitHub Releases.

## Custom Features

### Inbound Quota (`quota` service)

Tracks per-inbound traffic usage and blocks connections when a quota is exceeded.

#### Server-side configuration

Set `quota_bytes` directly on any inbound — the quota service discovers them automatically:

```json
{
  "inbounds": [
    {
      "type": "shadowsocks",
      "tag": "alice",
      "quota_bytes": "500GB",
      "...": "..."
    },
    {
      "type": "shadowsocks",
      "tag": "bob",
      "quota_bytes": "200GB",
      "...": "..."
    }
  ],
  "services": [
    {
      "type": "quota",
      "tag": "quota",
      "cache_path": "/var/lib/sing-box/quota.cache"
    }
  ],
  "outbounds": [
    {
      "type": "quota-portal",
      "tag": "quota-portal"
    }
  ],
  "route": {
    "rules": [
      {
        "ip_cidr": ["203.0.113.1/32"],
        "outbound": "quota-portal"
      }
    ]
  }
}
```

You can also define quotas in the service itself (explicit map takes precedence, `default_quota_bytes` applies to entries without `quota_bytes`):

```json
{
  "type": "quota",
  "tag": "quota",
  "default_quota_bytes": "200GB",
  "cache_path": "/var/lib/sing-box/quota.cache",
  "inbounds": {
    "alice": { "quota_bytes": "500GB" },
    "bob": {}
  }
}
```

- `quota_bytes` on inbound — human-readable values like `100GB`, `1TB`; the quota service picks these up at startup
- `cache_path` — persists traffic counters across restarts; omit to start fresh each time
- `quota-portal` outbound — intercepts TCP connections to the configured IP and returns an HTML page showing the quota status for the connecting inbound
- `203.0.113.1` is a documentation-only reserved IP (RFC 5737) that will never be reached on the public internet; clients visiting it through the proxy will see the quota page

When quota is exceeded, new connections from that inbound are rejected. Connections destined for the portal IP are always allowed through, so the quota page remains accessible even after the quota runs out.

#### HTTP API

If `listen` / `listen_port` are set on the service, a JSON API is available:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/quota/v1/inbounds` | List all inbounds with usage |
| `GET` | `/quota/v1/inbounds/{tag}` | Get a single inbound |
| `POST` | `/quota/v1/inbounds/{tag}/reset` | Reset counters for an inbound |

## Documentation

- Upstream docs: https://sing-box.sagernet.org
- For this fork, read the release notes and workflow outputs in the repository.
