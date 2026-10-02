---
icon: material/new-box
---

!!! question "自 sing-box 1.13.0 起"

# Quota

Quota 服务用于统计指定入站标签的流量使用情况，并在配额耗尽后拒绝新的连接。

### 结构

```json
{
  "type": "quota",

  ... // 监听字段

  "inbounds": {
    "vmess-in": {
      "quota_bytes": "100GB"
    }
  },
  "cache_path": "quota.json",
  "history_path": "quota-history.db",
  "history_retention_days": 365,
  "tls": {}
}
```

### 监听字段

参阅 [监听字段](/zh/configuration/shared/listen/) 了解详情。

如果配置了监听字段，sing-box 会在 `/quota/v1` 下提供 quota API。

### 字段

#### inbounds

==必填==

从入站标签到配额设置的映射对象。

#### inbounds.$tag.quota_bytes

==必填==

对应入站标签的流量配额。

该配额会同时统计上行和下行流量。

### 用户级别配额

多用户入站（`vmess`、`vless`、`trojan`、`anytls`、`naive` 和 `shadowsocks`）还可以在每个 `users[]` 条目上设置 `quota_bytes` 和 `admin`。Quota 服务会自动发现这些配置，并按 `(入站标签, 用户名)` 统计用量：

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

对于 `shadowsocks`，用户级别配额仅适用于多用户形式（即 `users[]` 字段，配合 AEAD 或 2022 加密方法）；单密码入站没有用户身份，不会被统计。每个配额用户必须有非空的 `name`，它将作为配额的键。将 `admin` 设为 `true` 可使该用户不受配额限制，但仍会统计其用量。

#### cache_path

如果设置，使用量计数器将保存到指定 JSON 文件中，并在下次启动时恢复。

#### history_path

启用历史流量统计并将按小时聚合的数据保存到指定 SQLite 数据库。服务每分钟采样一次当前计数器；数据库只保存小时聚合结果，不保存单条连接记录。目标优先记录域名，否则记录 IP，并且不记录端口。

#### history_retention_days

历史数据保留天数。服务在写入历史数据时自动清理更早的小时记录。设为 `0` 时不自动清理。

#### tls

TLS 配置，参阅 [TLS](/zh/configuration/shared/tls/#入站)。

### API

配置监听字段后，可用以下端点：

* `GET /quota/v1/inbounds`
* `GET /quota/v1/inbounds/{tag}`
* `POST /quota/v1/inbounds/{tag}/reset`
* `GET /quota/v1/history`
* `GET /quota/v1/history/top`
* `GET /quota/v1/history/forecast`

历史接口接受 Unix 秒格式的 `from`、`to`，以及 `inbound_tag`、`user_name`、`inbound_type`、`protocol`、`target` 过滤参数。`history` 的 `granularity` 可为 `hour`、`day` 或 `month`。

Top 接口额外接受 `dimension=user|target|inbound|protocol` 和 `limit`。预测接口使用最近 30 天的平均每日流量估算配额耗尽时间。`peak_bytes_per_second` 是一分钟采样窗口中的最高平均字节速率，并非瞬时线速。
