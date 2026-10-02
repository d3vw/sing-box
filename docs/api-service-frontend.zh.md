# sing-box API service —— 前端对接文档

> 适用于 `"type": "api"` 这个 service（sing-box 1.14+）。它是一个 **gRPC 服务**，
> 服务名 `daemon.StartedService`，权威定义见 [`daemon/started_service.proto`](../daemon/started_service.proto)。
> 本文档基于该 proto 和 `service/api/` 下的传输桥接代码（`web_bridge.go` / `web_bridge_websocket.go`）整理。

---

## 1. 连接信息

以如下配置为例：

```json
{
  "type": "api",
  "secret": "abc",
  "listen": "127.0.0.1",
  "listen_port": 9999,
  "dashboard": { "enabled": true }
}
```

| 项 | 值 |
|----|----|
| Base URL | `http://127.0.0.1:9999`（配了 `tls` 则为 `https://`） |
| gRPC 服务名 | `daemon.StartedService` |
| RPC 路径 | `POST /daemon.StartedService/<MethodName>` |
| 鉴权 | gRPC metadata `authorization: Bearer abc`（`secret` 为空则免鉴权） |
| 内置面板 | `http://127.0.0.1:9999/dashboard/`（`dashboard.enabled` 时） |
| gRPC 反射 | **已开启** —— 可用 `grpcurl` 直接探查 |
| 健康检查 | 注册了标准 `grpc.health.v1.Health` |

> 注意：`listen_port` **没有默认值**，不填就不监听。9999 是你自己配的。

---

## 2. 选哪种传输（重要）

服务端同一个端口同时支持 3 种 gRPC 传输（`web_bridge.go:55` 按 Content-Type / Upgrade 分流）：

| 传输 | 触发条件 | 适用场景 | 能否在浏览器用 |
|------|---------|---------|--------------|
| **原生 gRPC / HTTP2** | HTTP/2 + `Content-Type: application/grpc` | Go / Node / 后端、Electron 主进程 | ❌（浏览器 fetch 无法发原生 gRPC） |
| **gRPC-Web** | `POST` + `Content-Type: application/grpc-web[-text]` | 浏览器里的 **一元调用 + 服务端流**（`Subscribe*`） | ✅ |
| **gRPC-Web over WebSocket** | `Upgrade: websocket` + `Sec-WebSocket-Protocol: grpc-websockets` | 浏览器里的**客户端流 / 双向流**（Tailscale SSH、USB） | ✅ |

**结论（做 Web 前端）：**
- 绝大多数面板功能（版本、状态、日志、分组、连接、测延迟、切节点、切模式）都是**一元或服务端流**，用 **gRPC-Web** 即可。
- 只有 `StartTailscaleSSHSession`、`ProvideUSBDevices` 这种**双向流**才需要 **WebSocket 子协议 `grpc-websockets`**（与 improbable-eng/grpc-web 的 websocket transport 线兼容）。普通代理面板用不到。

CORS 已放行（`web_bridge.go:34`）：允许 `Content-Type, Authorization, X-Grpc-Web, X-User-Agent, Grpc-Timeout`，并 expose `Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin`。跨域可直接连。

---

## 3. 推荐的前端技术选型

### 方案 A：Connect-ES（推荐，最省心）
[`@connectrpc/connect-web`](https://connectrpc.com/docs/web/getting-started) 原生支持 gRPC-Web 协议（一元 + 服务端流）。

```bash
npm i @connectrpc/connect @connectrpc/connect-web @bufbuild/protobuf
npm i -D @bufbuild/buf @bufbuild/protoc-gen-es
```

`buf.gen.yaml`：
```yaml
version: v2
plugins:
  - local: protoc-gen-es
    out: src/gen
    opt: target=ts
```

把 `daemon/started_service.proto` 拷进来，`buf generate` 生成 TS 类型，然后：

```ts
import { createGrpcWebTransport } from "@connectrpc/connect-web";
import { createClient } from "@connectrpc/connect";
import { StartedService } from "./gen/started_service_pb";

const transport = createGrpcWebTransport({
  baseUrl: "http://127.0.0.1:9999",
  interceptors: [(next) => (req) => {
    req.header.set("Authorization", "Bearer abc");
    return next(req);
  }],
});
const client = createClient(StartedService, transport);

// 一元调用
const v = await client.getVersion({});
console.log(v.version, v.apiVersion);

// 服务端流（订阅状态）
for await (const s of client.subscribeStatus({ interval: 1000n })) {
  console.log(s.uplink, s.downlink, s.memory, s.connectionsOut);
}
```

> Connect-Web **不支持客户端流/双向流**（这是 gRPC-Web 协议本身的限制）。如果你要做 Tailscale SSH / USB 这类双向流，用方案 B。

### 方案 B：improbable-eng grpc-web（支持 WebSocket 双向流）
`@improbable-eng/grpc-web` + `grpc-web-websocket-transport`，对应服务端的 `grpc-websockets` 子协议。仅在需要双向流时选它。

### 方案 C：调试用 grpcurl（反射已开）
```bash
grpcurl -plaintext -H 'authorization: Bearer abc' 127.0.0.1:9999 list daemon.StartedService
grpcurl -plaintext -H 'authorization: Bearer abc' 127.0.0.1:9999 daemon.StartedService/GetVersion
```

---

## 4. RPC 一览（按面板功能分组）

完整字段以 proto 为准，下面列常用的。流式方法返回 `stream`，客户端要持续读取。

### 基础信息 / 生命周期
| RPC | 类型 | 入参 → 出参 | 说明 |
|-----|------|------------|------|
| `GetVersion` | 一元 | `Empty` → `Version{version, apiVersion}` | 版本 + API 版本号 |
| `GetStartedAt` | 一元 | `Empty` → `StartedAt{startedAt}` | 启动时间戳 |
| `SubscribeServiceStatus` | 服务端流 | `Empty` → `ServiceStatus{status, errorMessage}` | 内核状态机：IDLE/STARTING/STARTED/STOPPING/FATAL |
| `GetDeprecatedWarnings` | 一元 | `Empty` → `DeprecatedWarnings` | 配置弃用警告 |

### 状态 / 流量（首页仪表盘）
| RPC | 类型 | 入参 → 出参 | 说明 |
|-----|------|------------|------|
| `SubscribeStatus` | 服务端流 | `SubscribeStatusRequest{interval(ms)}` → `Status` | 内存、goroutine、连接数、上下行速率/总量 |

`Status` 字段：`memory, goroutines, connectionsIn, connectionsOut, trafficAvailable, uplink, downlink, uplinkTotal, downlinkTotal`。

### 日志
| RPC | 类型 | 入参 → 出参 | 说明 |
|-----|------|------------|------|
| `SubscribeLog` | 服务端流 | `Empty` → `Log{messages[], reset}` | `reset=true` 表示清屏重发；`Message{level, message}` |
| `GetDefaultLogLevel` | 一元 | `Empty` → `DefaultLogLevel{level}` | 默认日志级别 |
| `ClearLogs` | 一元 | `Empty` → `Empty` | 清空日志缓冲 |

`LogLevel` 枚举：`PANIC=0, FATAL=1, ERROR=2, WARN=3, INFO=4, DEBUG=5, TRACE=6`。

### 出站分组 / 节点（代理选择页）
| RPC | 类型 | 入参 → 出参 | 说明 |
|-----|------|------------|------|
| `SubscribeGroups` | 服务端流 | `Empty` → `Groups{group[]}` | 分组及其节点列表（含选中项、延迟） |
| `SubscribeOutbounds` | 服务端流 | `Empty` → `OutboundList{outbounds[]}` | 所有出站（GroupItem 形态） |
| `SelectOutbound` | 一元 | `SelectOutboundRequest{groupTag, outboundTag}` | 在某分组里选节点 |
| `URLTest` | 一元 | `URLTestRequest{outboundTag}` | 对某出站/分组发起测速 |
| `SetGroupExpand` | 一元 | `SetGroupExpandRequest{groupTag, isExpand}` | 记录 UI 展开状态 |

`Group{tag, type, selectable, selected, isExpand, items[]}`；
`GroupItem{tag, type, urlTestTime, urlTestDelay}`。

### Clash 模式
| RPC | 类型 | 入参 → 出参 | 说明 |
|-----|------|------------|------|
| `GetClashModeStatus` | 一元 | `Empty` → `ClashModeStatus{modeList[], currentMode}` | 可选模式 + 当前模式 |
| `SubscribeClashMode` | 服务端流 | `Empty` → `ClashMode{mode}` | 模式变更推送 |
| `SetClashMode` | 一元 | `ClashMode{mode}` | 切换模式 |

### 连接管理
| RPC | 类型 | 入参 → 出参 | 说明 |
|-----|------|------------|------|
| `SubscribeConnections` | 服务端流 | `SubscribeConnectionsRequest{interval(ms)}` → `ConnectionEvents{events[], reset}` | 增量连接事件 |
| `CloseConnection` | 一元 | `CloseConnectionRequest{id}` | 关闭单条连接 |
| `CloseAllConnections` | 一元 | `Empty` → `Empty` | 关闭全部 |

`ConnectionEvent{type(NEW/UPDATE/CLOSED), id, connection, uplinkDelta, downlinkDelta, closedAt}`。
`Connection` 字段很全：`id, inbound, inboundType, ipVersion, network, source, destination, domain, protocol, user, createdAt, closedAt, uplink, downlink, uplinkTotal, downlinkTotal, rule, outbound, outboundType, chainList[], processInfo`。

> 增量语义：`reset=true` 时丢弃本地缓存全量重建；否则按 `event.type` 增删改，流量用 `*Delta` 累加。

### 高级 / 可选（多数面板用不到）
- `StartNetworkQualityTest` / `StartSTUNTest`：网络质量、STUN/NAT 检测（服务端流进度）。
- `SubscribeTailscaleStatus` / `StartTailscalePing` / `SetTailscaleExitNode` / `TailscaleLogout` / `StartTailscaleSSHSession`：Tailscale 集成（SSH 是**双向流**，需 WebSocket 传输）。
- `ProvideUSBDevices` / `SubscribeUSBIPServerStatus`：USB/IP 共享（双向流）。

---

## 5. 鉴权细节

- 一元 / 服务端流（gRPC-Web）：在 HTTP 头加 `Authorization: Bearer <secret>`，会被服务端当作 gRPC metadata（`daemon/server.go:49` 校验 `authorization` → `Bearer ` 前缀 → 比对 secret）。
- WebSocket 传输：浏览器握手无法自定义 header，secret 走**首帧的 in-band metadata**（`web_bridge_websocket.go:56` 解析首条二进制消息为 header）。improbable-eng transport 会自动处理。
- `secret` 为空 → 完全免鉴权（`server.go:50`）。生产务必设 secret，并尽量只 `listen` 在 `127.0.0.1`。

---

## 6. 错误处理

错误以标准 gRPC status 返回。gRPC-Web 下从响应 trailer 里读 `Grpc-Status`（数字码）和 `Grpc-Message`（已在 CORS expose）。鉴权失败为 `UNAUTHENTICATED(16)`，消息 `missing/invalid authorization`。

---

## 7. 最小落地清单

1. 把 `daemon/started_service.proto` 拷进前端工程。
2. `buf generate` 生成 TS（connect-es）。
3. `createGrpcWebTransport({ baseUrl, interceptors:[加 Authorization] })`。
4. 首屏：`GetVersion` + `GetClashModeStatus`；常驻订阅 `SubscribeStatus` / `SubscribeGroups` / `SubscribeConnections` / `SubscribeLog`。
5. 交互：`SelectOutbound` / `SetClashMode` / `URLTest` / `CloseConnection`。
