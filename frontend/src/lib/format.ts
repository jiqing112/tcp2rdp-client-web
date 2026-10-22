import type { Node, Settings, Status } from "./types";

// format.ts is the arithmetic the labels need: durations read as mm:ss, byte
// rates pick their own unit, and route choices map to their Chinese names.

export const stateLabel: Record<string, string> = {
  idle: "未连接",
  starting: "连接中",
  running: "运行中",
  stopping: "停止中",
  error: "出错了",
};

export const routeChoices: { value: string; label: string }[] = [
  { value: "global", label: "全局" },
  { value: "rule:list", label: "分流" },
  { value: "rule:ip", label: "分流·IP" },
  { value: "direct", label: "直连" },
];

export const routeNames: Record<string, string> = {
  global: "全局代理",
  rule: "规则分流",
  direct: "全部直连",
};

export function routeChoice(node: Node): string {
  if (node.routeMode === "rule") return node.ruleBasis === "ip" ? "rule:ip" : "rule:list";
  return node.routeMode === "direct" ? "direct" : "global";
}

export function routeChoiceLabel(value: string): string {
  return routeChoices.find((c) => c.value === value)?.label ?? "全局";
}

export function parseRouteChoice(value: string): { mode: string; basis: string } {
  switch (value) {
    case "rule:ip":
      return { mode: "rule", basis: "ip" };
    case "rule:list":
      return { mode: "rule", basis: "list" };
    case "direct":
      return { mode: "direct", basis: "list" };
    default:
      return { mode: "global", basis: "list" };
  }
}

export function longRoute(mode: string): string {
  return routeNames[mode] ?? "全局代理";
}

export function isLiveState(state: string | undefined | null): boolean {
  return state === "running" || state === "starting" || state === "stopping";
}

export function activeNode(s: Settings): Node | null {
  if (s.nodes.length === 0) return null;
  return s.nodes.find((n) => n.id === s.activeNode) ?? s.nodes[0] ?? null;
}

export const latencyLabels: Record<string, string> = {
  tunnel: "隧道延迟",
  tcp: "TCP 延迟",
  ping: "Ping 延迟",
};

export function latencyLabel(mode: string | undefined): string {
  return latencyLabels[mode ?? ""] ?? "隧道延迟";
}

export function probeEvery(seconds: number | undefined): string {
  const s = Math.max(1, Math.round(seconds || 1800));
  if (s >= 3600 && s % 3600 === 0) return `每 ${s / 3600} 小时`;
  if (s >= 60 && s % 60 === 0) return `每 ${s / 60} 分钟`;
  return `每 ${s} 秒`;
}

export function latencyHelp(mode: string): string {
  if (mode === "tcp") {
    return "TCP 延迟：本机直接连接目标地址并完成 TCP 握手（不经过隧道）的耗时。它反映目标本身离你多远；隧道延迟通常会更高。";
  }
  if (mode === "ping") {
    return "Ping 延迟：系统 ping 发一个 ICMP 回显请求并等待回复的往返时间，不经过隧道。云服务器常禁用 ICMP，超时不一定代表目标不可用。";
  }
  return "隧道延迟：经隧道完成一次 SOCKS5 连接（含 TCP 握手）的耗时。约为 2×本机↔服务器往返 + 服务器↔目标往返，再加隧道握手指令与填充开销；不是 ICMP ping。";
}

export function formatUptime(total: number): string {
  const s = Math.max(0, Math.floor(total));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  return h > 0 ? `${pad(h)}:${pad(m)}:${pad(sec)}` : `${pad(m)}:${pad(sec)}`;
}

export function formatRate(bytesPerSecond: number): [string, string] {
  if (bytesPerSecond >= 1024 * 1024) return [(bytesPerSecond / 1024 / 1024).toFixed(1), "MB/s"];
  if (bytesPerSecond >= 1024) return [(bytesPerSecond / 1024).toFixed(1), "KB/s"];
  return [String(Math.round(bytesPerSecond)), "B/s"];
}

export function formatSize(bytes: number): string {
  if (bytes >= 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`;
  if (bytes >= 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${bytes} B`;
}

export function truncateText(value: string, max: number): string {
  return value.length <= max ? value : `${value.slice(0, max - 1)}…`;
}

/** Mirrors the client's normalizeServer just enough to be useful before the
 * node is saved; the server still does the authoritative pass. */
export function normalizeServerInput(raw: string): string {
  let s = raw.trim();
  s = s.replace(/^(?:rdp|tls|tcp|https?):\/\//i, "");
  s = s.replace(/^\/\//, "");
  s = s.replace(/\/+$/, "");
  if (!s) return "";
  if (s.startsWith("[")) {
    if (!/^\[[^\]]+\](?::\d+)?$/.test(s)) return s;
    return s.includes("]:") ? s : `${s}:3389`;
  }
  const colons = (s.match(/:/g) ?? []).length;
  if (colons > 1) return `[${s}]:3389`;
  if (colons === 1) return s;
  return `${s}:3389`;
}

export function parseHostPort(value: string): { host: string; port: number } | null {
  const s = value.trim();
  if (!s) return null;
  let host = "";
  let port = "";
  if (s.startsWith("[")) {
    const m = /^\[([^\]]+)\]:(\d+)$/.exec(s);
    if (!m) return null;
    host = m[1] ?? "";
    port = m[2] ?? "";
  } else {
    const at = s.lastIndexOf(":");
    if (at <= 0) return null;
    host = s.slice(0, at);
    port = s.slice(at + 1);
  }
  if (!host || host.includes(":") || !/^\d+$/.test(port)) return null;
  const n = Number(port);
  if (!Number.isInteger(n) || n < 1 || n > 65535) return null;
  return { host, port: n };
}

export function makeNodeId(): string {
  return `node-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`;
}

/** The newest samples sit at the right edge; returns the two SVG paths. */
export function sparkPaths(values: number[], W = 320, H = 62, PAD = 4): { line: string; area: string } {
  const samples = values.filter((v) => v > 0);
  if (samples.length === 0) return { line: "", area: "" };
  const SLOTS = 90;
  const step = W / (SLOTS - 1);
  const max = Math.max(40, ...samples) * 1.15;
  const y = (v: number) => H - PAD - (Math.min(v, max) / max) * (H - PAD * 2);
  const points: [number, number][] = samples.map((v, i) => [W - (samples.length - 1 - i) * step, y(v)]);
  if (points.length === 1) points.unshift([W - 1.5, points[0]?.[1] ?? 0]);
  const line = points.map(([x, yy], i) => `${i === 0 ? "M" : "L"}${x.toFixed(1)},${yy.toFixed(1)}`).join(" ");
  let area = "";
  const first = points[0];
  const last = points[points.length - 1];
  if (points.length > 12 && first && last) {
    area =
      `M${first[0].toFixed(1)},${H}` +
      points.map(([x, yy]) => `L${x.toFixed(1)},${yy.toFixed(1)}`).join(" ") +
      `L${last[0].toFixed(1)},${H}Z`;
  }
  return { line, area };
}

export function statusHeroMeta(next: Status, s: Settings): string {
  const route = longRoute(next.routeMode);
  if (isLiveState(next.state)) {
    const name = next.nodeName || activeNode(s)?.name || "";
    const server = next.server ? (next.serverIp ? `${next.serverIp}（${next.server}）` : next.server) : "";
    const forward = next.mode === "forward" ? "端口转发" : "";
    return [name, server, route, forward].filter(Boolean).join(" · ");
  }
  if (next.state === "error") return next.message || "启动失败";
  const node = activeNode(s);
  return node ? `当前节点：${node.name} · ${longRoute(node.routeMode)}` : "还没有节点，先新增一个";
}

const regionNames = (() => {
  try {
    return new Intl.DisplayNames(["zh-CN"], { type: "region" });
  } catch {
    return null;
  }
})();

export function countryLabel(code: string): string {
  const cc = code.toUpperCase();
  try {
    return regionNames?.of(cc) || cc;
  } catch {
    return cc;
  }
}
