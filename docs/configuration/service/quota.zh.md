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

#### cache_path

如果设置，使用量计数器将保存到指定 JSON 文件中，并在下次启动时恢复。

#### tls

TLS 配置，参阅 [TLS](/zh/configuration/shared/tls/#入站)。

### API

配置监听字段后，可用以下端点：

* `GET /quota/v1/inbounds`
* `GET /quota/v1/inbounds/{tag}`
* `POST /quota/v1/inbounds/{tag}/reset`
