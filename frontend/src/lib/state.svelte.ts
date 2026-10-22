import { SvelteMap, SvelteSet } from "svelte/reactivity";
import { api } from "./api";
import { fallbackSettings, type Info, type LogLine, type Node, type ProbeResult, type Settings, type Status } from "./types";

// state.svelte.ts is the app's memory: one copy of the server state plus the
// small pieces of UI state that several components need to see at once. It is
// a $state object on purpose: every component reading a property re-renders
// when that property changes, with no subscriptions to wire up.

export type Busy = "" | "start" | "stop" | "probe" | "switch" | "route" | "save" | "sysproxy";

export interface Toast {
  id: number;
  message: string;
  tone: "ok" | "err" | "warn" | "info";
}

export interface NodeProbe {
  ms: number;
  ok: boolean;
  message: string;
}

export const app = $state({
  booted: false,
  settings: fallbackSettings() as Settings,
  status: null as Status | null,
  info: null as Info | null,

  busy: "" as Busy,
  banner: null as { message: string; tone: "error" | "warn" } | null,
  lastProbe: null as ProbeResult | null,

  nodeModal: false as false | "new" | "edit",
  editingNode: null as Node | null,
  latencyModal: false,

  diagOpen: false,

  toasts: [] as Toast[],
  logLines: [] as LogLine[],
  logTotal: 0,

  nodeProbes: new SvelteMap<string, NodeProbe>(),
  nodeProbeBusy: new SvelteSet<string>(),
  nodeGeo: new SvelteMap<string, string>(),
});

let toastSeq = 0;
export function toast(message: string, tone: Toast["tone"] = "info", duration = 2600): void {
  const id = ++toastSeq;
  app.toasts.push({ id, message, tone });
  setTimeout(() => {
    const i = app.toasts.findIndex((t) => t.id === id);
    if (i >= 0) app.toasts.splice(i, 1);
  }, duration);
}

export function showBanner(message: string, tone: "error" | "warn" = "error"): void {
  app.banner = { message, tone };
}

export function hideBanner(): void {
  app.banner = null;
}

let logSeqWatermark = 0;

export function appendLogs(lines: LogLine[], reset = false): void {
  if (reset) {
    app.logLines = [];
    app.logTotal = 0;
    logSeqWatermark = 0;
  }
  if (!lines.length) return;
  // The server's send watermark is per-hub, not per-tab: a snapshot can race a
  // push and deliver a few lines twice. Keys in the list must stay unique.
  const fresh = lines.filter((l) => l.seq > logSeqWatermark);
  if (!fresh.length) return;
  app.logLines.push(...fresh);
  logSeqWatermark = fresh[fresh.length - 1].seq;
  if (app.logLines.length > 600) {
    app.logLines = app.logLines.slice(app.logLines.length - 600);
  }
  app.logTotal += fresh.length;
}

export function clearLogsLocally(): void {
  app.logLines = [];
  app.logTotal = 0;
  logSeqWatermark = 0;
}

// Geo lookups belong to the node's server, not the row: two rows can point at
// the same host and must share one answer.
const geoRequested = new Set<string>();

export function ensureNodeGeo(): void {
  for (const node of app.settings.nodes) {
    if (node.routeMode === "direct" || !node.server) continue;
    if (app.nodeGeo.has(node.server) || geoRequested.has(node.server)) continue;
    geoRequested.add(node.server);
    api
      .locate(node.server)
      .then((geo) => {
        if (geo.countryCode) app.nodeGeo.set(node.server, geo.countryCode);
      })
      .catch(() => {
        /* decoration only: a blocked lookup must not nag */
      });
  }
}

export function forgetNodeGeo(server: string): void {
  app.nodeGeo.delete(server);
}
