// Mirror of the Go backend's JSON shapes. Field names are the wire format; do
// not rename them.

export interface Node {
  id: string;
  name: string;
  server: string;
  key: string;
  routeMode: string; // global / rule / direct
  ruleBasis: string; // list / ip
  ruleIp: string; // builtin / remote / off / path-or-url
  ruleDomain: string;
  ruleDns: string; // host:port / local / off
}

export interface Settings {
  nodes: Node[];
  activeNode: string;
  listenHost: string;
  listenPort: string;
  socks5: boolean;
  target: string;
  forwards: string;
  udpForwards: string;
  timeout: string;
  tlsFp: string;
  pad: string;
  padMax: string;
  tick: string;
  pin: string;
  knownHosts: string;
  insecure: boolean;
  noLog: boolean;
  logLevel: string;
  binaryPath: string;
  probeTarget: string;
  latencyMode: string; // tunnel / tcp / ping
  probeInterval: number;
  systemProxy: boolean;
  httpProxyPort: string;
}

export interface Status {
  state: string; // idle / starting / running / stopping / error
  message: string;
  mode: string; // socks5 / forward
  routeMode: string;
  nodeId: string;
  nodeName: string;
  server: string;
  serverIp: string;
  listen: string;
  target: string;
  forwards: number;
  rttMs: number;
  uptimeSec: number;
  sendBps: number;
  recvBps: number;
  streams: number;
  sendTotalB: number;
  recvTotalB: number;
  history: number[];
  startedAt: string;
  probeAge: number;
  pid: number;
  proxyAddr: string;
  systemProxy: boolean;
  systemProxyActive: boolean;
  systemProxySupported: boolean;
  proxyReady: boolean;
  sampling: boolean;
  latencyMode: string;
  probeTarget: string;
  probeInterval: number;
  probeError: string;
  keyLen: number;
  tunEnabled: boolean; // user's preference, survives idle/stop
  tunActive: boolean; // default routes are installed now
  tunMessage: string; // state or last setup error
  admin: boolean;
  tunSupported: boolean;
}

export interface TunCheck {
  name: string;
  ok: boolean;
  detail: string;
  optional: boolean;
}

export interface TunReport {
  ok: boolean;
  summary: string;
  target: string;
  at: string;
  checks: TunCheck[];
}

export interface LogLine {
  seq: number;
  time: string;
  level: string;
  text: string;
}

export interface LogBatch {
  lines: LogLine[];
  reset: boolean;
}

export interface ProbeResult {
  host: string;
  ip: string;
  port: string;
  rdp: boolean;
  tcpMs: number;
  negotiateMs: number;
  tlsMs: number;
  tlsVersion: string;
  cipher: string;
  cn: string;
  fingerprint: string;
  message: string;
}

export interface Info {
  version: string;
  platform: string;
  os: string;
  dataDir: string;
  dbPath: string;
  sessionPath: string;
  clientPath: string;
  systemProxySupported: boolean;
  webHost: string; // 工作台当前的监听范围：127.0.0.1 / 0.0.0.0
  webPort: string;
  lanIp: string; // 本机局域网地址（展示用，可能为空）
  tokenRequired: boolean;
}

export interface GeoInfo {
  ip: string;
  countryCode: string;
}

export function fallbackSettings(): Settings {
  return {
    nodes: [],
    activeNode: "",
    listenHost: "127.0.0.1",
    listenPort: "1080",
    socks5: true,
    target: "",
    forwards: "",
    udpForwards: "",
    timeout: "10",
    tlsFp: "windows",
    pad: "block",
    padMax: "512",
    tick: "0",
    pin: "",
    knownHosts: "",
    insecure: false,
    noLog: false,
    logLevel: "info",
    binaryPath: "",
    probeTarget: "www.baidu.com:443",
    latencyMode: "tunnel",
    probeInterval: 1800,
    systemProxy: false,
    httpProxyPort: "34457",
  };
}
