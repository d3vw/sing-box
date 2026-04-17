---
icon: material/new-box
---

!!! question "Since sing-box 1.13.0"

# Quota

Quota service tracks traffic usage for selected inbound tags and rejects new connections after the configured quota is exhausted.

### Structure

```json
{
  "type": "quota",

  ... // Listen Fields

  "inbounds": {
    "vmess-in": {
      "quota_bytes": "100GB"
    }
  },
  "cache_path": "quota.json",
  "tls": {}
}
```

### Listen Fields

See [Listen Fields](/configuration/shared/listen/) for details.

If listen fields are set, sing-box exposes the quota API under `/quota/v1`.

### Fields

#### inbounds

==Required==

A mapping object from inbound tags to quota settings.

#### inbounds.$tag.quota_bytes

==Required==

Traffic quota for the inbound tag.

The quota counts both uplink and downlink traffic.

#### cache_path

If set, usage counters are saved to the specified JSON file and restored on the next startup.

#### tls

TLS configuration, see [TLS](/configuration/shared/tls/#inbound).

### API

When listen fields are configured, the following endpoints are available:

* `GET /quota/v1/inbounds`
* `GET /quota/v1/inbounds/{tag}`
* `POST /quota/v1/inbounds/{tag}/reset`
