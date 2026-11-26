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
  "history_path": "quota-history.db",
  "history_retention_days": 365,
  "reset_day": 1,
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

### Per-user quota

Multi-user inbounds (`vmess`, `vless`, `trojan`, `anytls`, `naive` and
`shadowsocks`) additionally accept `quota_bytes` and `admin` on each `users[]`
entry. The quota service discovers these automatically and tracks usage per
`(inbound tag, user name)`:

```json
{
  "type": "shadowsocks",
  "tag": "ss-in",
  "method": "2022-blake3-aes-128-gcm",
  "users": [
    {
      "name": "alice",
      "password": "...",
      "quota_bytes": "500GB"
    }
  ]
}
```

For `shadowsocks`, per-user quota applies only to the multi-user form (the
`users[]` field, with AEAD or 2022 methods); a single-password inbound has no
user identity and is not tracked. Each quota user must have a non-empty `name`,
which is used as the quota key. Set `admin` to `true` to mark a user exempt from
the quota while still tracking their usage.

A quota user (or a `direct` inbound with `quota_bytes`) can also set a service
period with `start_date` and `validity`; the portal then shows the user when
their service expires and how many days are left:

```json
{
  "name": "alice",
  "password": "...",
  "quota_bytes": "500GB",
  "start_date": "2026-10-01",
  "validity": "quarter"
}
```

`start_date` is a `YYYY-MM-DD` date in Beijing time. `validity` is one of
`month`, `quarter`, `half_year` or `year`. The period ends at 00:00 Beijing time
that many months after `start_date` (clamped to the month's last day), so the
example above is valid through 2026-12-31. Both fields must be set together. The
period is for display only: connections are not rejected once it has passed.
To renew, move `start_date` forward.

With a service period, the portal shows the daily allowance and the run-out
forecast up to expiry even without `reset_day`; with both, whichever comes
first is used.

#### cache_path

If set, usage counters are saved to the specified JSON file and restored on the next startup.

#### history_path

Enables historical traffic statistics in the specified SQLite database. Counters are sampled every minute and stored as hourly aggregates; individual connections are not stored. Targets use the domain when available, otherwise the IP address, without the port.

#### history_retention_days

Number of days to retain hourly history. Older rows are removed during flushes. `0` disables automatic cleanup.

#### reset_day

Resets every user's usage monthly on this day (`1`–`31`) at 00:00 Beijing time. Months without that day reset on their last day. The cycle start is kept in `cache_path`, so a restart after the reset day still resets usage. `0` or unset never resets.

With `reset_day` set, the portal shows the reset date, a daily budget (remaining quota spread over the days left) and a forecast of whether the quota lasts until the reset.

#### tls

TLS configuration, see [TLS](/configuration/shared/tls/#inbound).

### API

When listen fields are configured, the following endpoints are available:

* `GET /quota/v1/inbounds`
* `GET /quota/v1/inbounds/{tag}`
* `POST /quota/v1/inbounds/{tag}/reset`
* `GET /quota/v1/history`
* `GET /quota/v1/history/top`
* `GET /quota/v1/history/forecast`

History accepts Unix-second `from` and `to` values and the filters `inbound_tag`, `user_name`, `inbound_type`, `protocol`, and `target`. Set `granularity` to `hour`, `day`, or `month`.

Top queries additionally accept `dimension=user|target|inbound|protocol` and `limit`. Forecasts use the trailing 30-day average daily usage. `peak_bytes_per_second` is the highest one-minute average rate, not an instantaneous line rate.
