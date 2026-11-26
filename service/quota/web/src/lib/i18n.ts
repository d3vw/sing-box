import type { MonthDay } from "./types";

// A sentence with emphasized parts: strings stay as they are, {b} is set
// bold in the number face, and accent marks the hoverable peak hours.
export type Part = string | { b: string; accent?: boolean };

const MON = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const WD_ZH = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
const WD_EN = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const SHICHEN = "子丑寅卯辰巳午未申酉戌亥";
const chanceZh = (p: number) => Math.max(1, Math.round(p * 10));
const chanceEn = (p: number) => Math.max(10, Math.round(p * 10) * 10);
const md = (d: MonthDay) => MON[d[0] - 1] + " " + d[1];
type End = "reset" | "expiry";

const zh = {
  title: "流量配额", light: "浅色", dark: "深色", themeLabel: "主题", langLabel: "语言", menu: "显示设置",
  healthy: "正常", blocked: "已超额", remaining: "剩余", upload: "上传", download: "下载",
  used: (a: string, b: string) => "已用 " + a + " · " + b + "%",
  resetsOn: (m: number, d: number, left: number) => m + " 月 " + d + " 日重置，" + (left === 0 ? "今天" : "还有 " + left + " 天"),
  expired: "已到期",
  validTo: (d: string) => "至 " + d,
  expiredOn: (d: string) => d + " 到期",
  expiresOn: (y: number, m: number, d: number, left: number) => {
    const day = y + " 年 " + m + " 月 " + d + " 日";
    return left === 0 ? "已于 " + day + "到期" : "有效期至 " + day + "，" + (left === 1 ? "今天是最后一天" : "还剩 " + left + " 天");
  },
  tone: { ok: "够用", warn: "可能不够", err: "不够用" },
  fcLeft: (v: string, e: End) => (e === "expiry" ? "到期" : "重置") + "时约剩 " + v,
  fcChance: (p: number) => "约 " + chanceZh(p) + " 成可能提前用完",
  fcDate: (r: string) => "预计 " + r + "用完",
  dateRange: (a: MonthDay, b: MonthDay) =>
    a[0] === b[0] && a[1] === b[1] ? a[0] + " 月 " + a[1] + " 日"
      : a[0] === b[0] ? a[0] + " 月 " + a[1] + "–" + b[1] + " 日"
      : a[0] + " 月 " + a[1] + " 日–" + b[0] + " 月 " + b[1] + " 日",
  basisToggle: "查看每天可用量",
  basisLeft: (r: string, per: string, e: End): Part[] => ["照最近的用量，" + (e === "expiry" ? "到期" : "重置") + "时预计剩 ", { b: r }, "；余量平摊下来，每天可用约 ", { b: per }],
  basisRunOutBy: (r: string, per: string, e: End): Part[] => ["要是用完，多半在 ", { b: r }, "；想撑到" + (e === "expiry" ? "到期" : "重置") + "，每天最多用约 ", { b: per }],
  basisRunOut: (p: number, per: string, e: End): Part[] => {
    const w = e === "expiry" ? "到期" : "重置";
    return ["约 " + chanceZh(p) + " 成可能在" + w + "前用完；想撑到" + w + "，每天最多用约 ", { b: per }];
  },
  wd: WD_ZH, wdShort: ["日", "一", "二", "三", "四", "五", "六"], today: "今天", todayShort: "今",
  dayLabel: (m: number, d: number, w: number) => m + " 月 " + d + " 日 " + WD_ZH[w],
  dailyTitle: "每天用量", lastDays: (n: number) => "最近 " + n + " 天", statAvg: "日均", statPeak: "最多", budgetLabel: "每天可用",
  vsUsual: (p: number | null) => p == null || Math.abs(p) < 5 ? "和平时差不多" : "比平时" + (p > 0 ? "多" : "少") + " " + Math.abs(p) + "%",
  tipToday: "今天到现在", tipOver: (v: string) => "超出每天可用 " + v,
  dailyChart: (n: number, over: number, budget: boolean) => "最近 " + n + " 天每天的用量" + (budget ? "，" + (over ? "有 " + over + " 天超过每天可用量" : "都没超过每天可用量") : "") + "；用左右方向键逐天查看",
  heatTitle: "高峰时段", heatSub: (r: string, s: number): Part[] => ["最近 7 天 ", { b: r, accent: true }, " 用得最多，约占全天 " + s + "%"],
  heatChart: "最近 7 天每个时辰的用量；用方向键逐格查看",
  slot: (s: number, r: string) => SHICHEN.charAt(s) + "时 " + r, slotTick: (s: number) => SHICHEN.charAt(s),
  tipShare: (p: string) => "占当天 " + p + "%", notYet: "还没到", less: "少", more: "多", bjt: "北京时间",
  subscription: "订阅", subHint: "用客户端扫码，或复制链接", copy: "复制链接", copied: "已复制",
  server: "服务器", port: "端口", cipher: "加密方式", password: "密码", show: "显示", hide: "隐藏", mux: "多路复用", padding: "填充",
  muxHint: "多个连接共用一条通道", paddingHint: "补随机长度，流量更难被识别", hostPlaceholder: "域名或 IP", portPlaceholder: "443", passwordPlaceholder: "连接密码",
  lampIdle: "未测试", lampTesting: "测试中…", lampOk: (ms: number) => "已连通，" + ms + " ms", lampFail: "连接失败",
  why: { host: "服务器地址为空", port: "端口需为 1–65535", password: "密码为空" },
  test: "测试连接", testing: "测试中…", save: "保存", saving: "保存中…",
  toastSaveFail: "保存失败", requestFailed: "请求失败",
  retry: "重试", dismiss: "关闭提示",
  updatedAgo: (m: number) => m < 1 ? "刚刚更新" : m + " 分钟前更新", refreshFailed: "更新失败，显示的是旧数据", refreshing: "更新中…",
  nqTitle: "网络质量", nqNote: "经隧道连发 20 秒小包，看延迟稳不稳", nqLatency: "延迟", nqJitter: "抖动", nqSlow: "卡顿",
  nqP95: (v: string) => "偶尔 " + v, nqStart: "开始测试", nqAgain: "再测一次", nqRunning: (s: number) => "测试中… " + s + " 秒",
  nqIdle: "未测试", nqHint: "看隧道延迟稳不稳",
  nqAgo: (m: number) => m < 1 ? "刚刚测" : m < 60 ? m + " 分钟前测" : Math.floor(m / 60) + " 小时前测",
  nqGrade: { good: "网络良好", fair: "有些波动", poor: "网络较差" }, nqFail: "连不上测试服务",
  nqChart: "每个包的延迟；鼠标移上去逐个查看，也可以用方向键", nqTipAt: (s: string, n: number) => "第 " + s + " 秒 · 第 " + n + " 个包",
  nqLost: "没回来", nqWaiting: "等待回包…", nqLostNote: "算作卡顿", nqSlowNote: (v: string) => "卡顿，比平时慢 " + v,
  vtOut: "出站", obTitle: "出站", obNote: "选一个出站，你的流量就从它出去", obDefault: "默认", obDefaultSub: "按服务器的分流规则走", obInUse: "使用中",
  obAdd: "添加节点", obTestAll: "全部测速", obTest: "测速", obEdit: "编辑", obDelete: "删除", obFail: "失败",
  obDeleteConfirm: (n: string) => "确定删除节点「" + n + "」？", obDeleted: "节点已删除", obBackToDefault: "它正在使用，已改回默认",
  obSwitched: (n: string) => "已切换到「" + n + "」", obSwitchedDefault: "已改回默认", obSwitchFail: "切换失败", obReconnect: "现有连接已断开，会经新出站重连",
  obEmpty: "还没有节点。点「添加节点」存一个 Shadowsocks 节点，就能让流量从它出去。", obHint: "默认按服务器的分流规则走。添加 Shadowsocks 节点后，点一行就能切换。",
  obNewTitle: "添加节点", obEditTitle: "编辑节点", obCancel: "关闭", obName: "名称", obNamePh: "不填就用服务器地址",
  obImport: "导入", obImportPh: "粘贴 ss:// 链接自动填好", obImportBad: "认不出这个 ss:// 链接", obImported: "已从链接填好，检查后保存",
  obDiscard: "改动还没保存，确定放弃？", obUnsaved: "未保存", obSaved: "节点已保存", obPickHint: "点这一行就能切换过去",
  vtMain: "流量", vtConns: "连接",
  cxTitle: "实时连接", cxNote: "只显示你自己的连接，不含这个页面自身的", cxActive: "活跃", cxClosedTotal: "累计关闭", cxUp: "活跃连接上传", cxDown: "活跃连接下载",
  cxLive: "实时", cxOffline: "已断开，稍后重连", cxTabActive: (n: number | string) => "活跃 " + n, cxTabClosed: (n: number | string) => "已关闭 " + n, cxNetAll: "全部",
  cxSearch: "按域名、地址、协议或出站筛选", cxEmptyActive: "现在没有活跃连接", cxEmptyClosed: "还没有关闭的连接", cxNoMatch: "没有匹配的连接",
  cxColHost: "目标", cxColProto: "协议", cxColOut: "出站 / 规则", cxColUp: "上传", cxColDown: "下载", cxColTime: "时间",
  cxTop: "流量最多的目标", cxTopEmpty: "暂无", cxTopN: (n: number) => n + " 个连接",
  cxShown: (a: number, b: number) => a === b ? "共 " + b + " 条" : "显示 " + a + " / " + b + " 条",
  cxMore: (n: number) => "还有 " + n + " 条没列出，可以再筛选", cxKept: (n: number) => "已关闭的只保留最近 " + n + " 条，重启后清空",
  cxClose: "断开", cxCloseAll: "断开全部", cxCloseAllConfirm: "确定断开全部活跃连接？", toastConnClosed: (n: number) => "已断开 " + n + " 个连接",
  cxFor: (d: string) => "已连 " + d, cxAgo: (d: string) => d + "前关闭",
  cxDur: (s: number) => s < 60 ? s + " 秒" : s < 3600 ? Math.floor(s / 60) + " 分钟" : Math.floor(s / 3600) + " 小时 " + Math.floor(s % 3600 / 60) + " 分",
  cxCount: (n: number) => n + " 个连接",
  noQuota: "没有配置流量配额。",
  allUsers: "全部用户", reset: "重置", resetConfirm: "确定重置该用户的流量？", resetDone: (n: string) => "已重置 " + n + " 的流量",
};

export type Dict = typeof zh;

const en: Dict = {
  title: "Traffic quota", light: "Light", dark: "Dark", themeLabel: "Theme", langLabel: "Language", menu: "Display settings",
  healthy: "Online", blocked: "Over quota", remaining: "Remaining", upload: "Upload", download: "Download",
  used: (a, b) => "Used " + a + " · " + b + "%",
  resetsOn: (m, d, left) => "Resets " + md([m, d]) + ", " + (left === 0 ? "today" : left + " day" + (left === 1 ? "" : "s") + " left"),
  expired: "Expired",
  validTo: (d) => "until " + d,
  expiredOn: (d) => "expired " + d,
  expiresOn: (y, m, d, left) => {
    const day = md([m, d]) + ", " + y;
    return left === 0 ? "Expired " + day : "Valid through " + day + ", " + (left === 1 ? "last day today" : left + " days left");
  },
  tone: { ok: "On track", warn: "May run out", err: "Running out" },
  fcLeft: (v, e) => "~" + v + " left at " + (e === "expiry" ? "expiry" : "reset"),
  fcChance: (p) => "~" + chanceEn(p) + "% chance to run out early",
  fcDate: (r) => "likely out " + r,
  dateRange: (a, b) => a[0] === b[0] && a[1] === b[1] ? md(a) : a[0] === b[0] ? md(a) + "–" + b[1] : md(a) + " – " + md(b),
  basisToggle: "Show daily allowance",
  basisLeft: (r, per, e) => ["At your recent pace, expect ", { b: r }, " left at " + (e === "expiry" ? "expiry" : "reset") + "; what you have now works out to about ", { b: per }, " a day"],
  basisRunOutBy: (r, per, e) => ["If it runs out, most likely ", { b: r }, "; to last until " + (e === "expiry" ? "expiry" : "reset") + ", keep to about ", { b: per }, " a day"],
  basisRunOut: (p, per, e) => ["About " + chanceEn(p) + "% chance to run out before " + (e === "expiry" ? "expiry" : "reset") + "; to last, keep to about ", { b: per }, " a day"],
  wd: WD_EN, wdShort: WD_EN, today: "Today", todayShort: "Now",
  dayLabel: (m, d, w) => WD_EN[w] + ", " + md([m, d]),
  dailyTitle: "Daily usage", lastDays: (n) => "Last " + n + " days", statAvg: "Daily avg", statPeak: "Peak", budgetLabel: "Daily budget",
  vsUsual: (p) => p == null || Math.abs(p) < 5 ? "about usual" : Math.abs(p) + "% " + (p > 0 ? "more" : "less") + " than usual",
  tipToday: "Today so far", tipOver: (v) => v + " over budget",
  dailyChart: (n, over, budget) => "Daily usage over the last " + n + " days" + (budget ? ", " + (over ? over + " over the daily budget" : "none over the daily budget") : "") + ". Use arrow keys to step through days",
  heatTitle: "Busy hours", heatSub: (r, s) => ["Last 7 days, busiest ", { b: r, accent: true }, ", about " + s + "% of the day"],
  heatChart: "Usage per 2-hour slot over the last 7 days. Use arrow keys to step through cells",
  slot: (_s, r) => r, slotTick: (s) => String(s * 2),
  tipShare: (p) => p + "% of the day", notYet: "Not yet", less: "Less", more: "More", bjt: "Beijing time",
  subscription: "Subscription", subHint: "Scan with your client, or copy the link", copy: "Copy link", copied: "Copied",
  server: "Server", port: "Port", cipher: "Cipher", password: "Password", show: "Show", hide: "Hide", mux: "Multiplex", padding: "Padding",
  muxHint: "Shares one tunnel across connections", paddingHint: "Adds random length so traffic is harder to spot", hostPlaceholder: "Domain or IP", portPlaceholder: "443", passwordPlaceholder: "Password",
  lampIdle: "Not tested", lampTesting: "Testing…", lampOk: (ms) => "Connected, " + ms + " ms", lampFail: "Failed",
  why: { host: "Server is empty", port: "Port must be 1–65535", password: "Password is empty" },
  test: "Test", testing: "Testing…", save: "Save", saving: "Saving…",
  toastSaveFail: "Couldn't save", requestFailed: "Request failed",
  retry: "Retry", dismiss: "Dismiss",
  updatedAgo: (m) => m < 1 ? "Just updated" : "Updated " + m + " min ago", refreshFailed: "Update failed, showing old data", refreshing: "Updating…",
  nqTitle: "Network quality", nqNote: "Pings through the tunnel for 20 s to see how steady it is", nqLatency: "Latency", nqJitter: "Jitter", nqSlow: "Stalls",
  nqP95: (v) => "at times " + v, nqStart: "Start test", nqAgain: "Test again", nqRunning: (s) => "Testing… " + s + " s",
  nqIdle: "Not tested", nqHint: "See how steady the tunnel is",
  nqAgo: (m) => m < 1 ? "just now" : m < 60 ? m + " min ago" : Math.floor(m / 60) + " h ago",
  nqGrade: { good: "Steady", fair: "Some wobble", poor: "Unstable" }, nqFail: "Couldn't reach the test service",
  nqChart: "Latency of each packet. Hover to inspect, or use arrow keys", nqTipAt: (s, n) => s + " s · packet " + n,
  nqLost: "No reply", nqWaiting: "Waiting…", nqLostNote: "Counted as a stall", nqSlowNote: (v) => "Stall, " + v + " slower than usual",
  vtOut: "Outbound", obTitle: "Outbound", obNote: "Pick where your traffic goes out", obDefault: "Default", obDefaultSub: "Follows the server's routing rules", obInUse: "In use",
  obAdd: "Add node", obTestAll: "Test all", obTest: "Test", obEdit: "Edit", obDelete: "Delete", obFail: "Failed",
  obDeleteConfirm: (n) => "Delete node “" + n + "”?", obDeleted: "Node deleted", obBackToDefault: "It was in use; switched back to default",
  obSwitched: (n) => "Switched to “" + n + "”", obSwitchedDefault: "Switched back to default", obSwitchFail: "Couldn't switch", obReconnect: "Open connections were closed and reconnect through it",
  obEmpty: "No nodes yet. Add a Shadowsocks node to send your traffic out through it.", obHint: "By default the server's routing rules decide. Once you add Shadowsocks nodes, click one to switch.",
  obNewTitle: "Add node", obEditTitle: "Edit node", obCancel: "Close", obName: "Name", obNamePh: "Defaults to the server",
  obImport: "Import", obImportPh: "Paste an ss:// link to fill in", obImportBad: "Couldn't read that ss:// link", obImported: "Filled in from the link; check and save",
  obDiscard: "Discard unsaved changes?", obUnsaved: "Unsaved", obSaved: "Node saved", obPickHint: "Click its row to switch to it",
  vtMain: "Traffic", vtConns: "Connections",
  cxTitle: "Live connections", cxNote: "Only your own connections, not this page's", cxActive: "Active", cxClosedTotal: "Closed in total", cxUp: "Active upload", cxDown: "Active download",
  cxLive: "Live", cxOffline: "Disconnected, retrying", cxTabActive: (n) => "Active " + n, cxTabClosed: (n) => "Closed " + n, cxNetAll: "All",
  cxSearch: "Filter by host, address, protocol or outbound", cxEmptyActive: "No active connections", cxEmptyClosed: "No closed connections yet", cxNoMatch: "No matching connections",
  cxColHost: "Destination", cxColProto: "Protocol", cxColOut: "Outbound / rule", cxColUp: "Upload", cxColDown: "Download", cxColTime: "Time",
  cxTop: "Top destinations by traffic", cxTopEmpty: "Nothing yet", cxTopN: (n) => n + " connection" + (n === 1 ? "" : "s"),
  cxShown: (a, b) => a === b ? b + " total" : a + " of " + b,
  cxMore: (n) => n + " more not listed; filter to narrow", cxKept: (n) => "Keeps the last " + n + " closed, cleared on restart",
  cxClose: "Close", cxCloseAll: "Close all", cxCloseAllConfirm: "Close all active connections?", toastConnClosed: (n) => "Closed " + n + " connection" + (n === 1 ? "" : "s"),
  cxFor: (d) => "open " + d, cxAgo: (d) => "closed " + d + " ago",
  cxDur: (s) => s < 60 ? s + "s" : s < 3600 ? Math.floor(s / 60) + "m" : Math.floor(s / 3600) + "h " + Math.floor(s % 3600 / 60) + "m",
  cxCount: (n) => n + " conn" + (n === 1 ? "" : "s"),
  noQuota: "No quota configured.",
  allUsers: "All users", reset: "Reset", resetConfirm: "Reset this user's traffic?", resetDone: (n) => "Reset the traffic of " + n,
};

export type Lang = "zh" | "en";

export const dicts: Record<Lang, Dict> = { zh, en };
