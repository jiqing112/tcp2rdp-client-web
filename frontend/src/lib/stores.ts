import { writable } from "svelte/store";
import { fallbackSettings, type Info, type LogLine, type ProbeResult, type Settings, type Status } from "./types";

// stores.ts is the app's memory: one copy of the server state, plus the small
// pieces of UI state (busy markers, toasts, modals) that several components
// need to see at once.

export const settings = writable<Settings>(fallbackSettings());
export const status = writable<Status | null>(null);
export const info = writable<Info | null>(null);

export const busy = writable<"" | "start" | "stop" | "probe" | "switch" | "route" | "save" | "sysproxy">("");

export interface Toast {
  id: number;
  message: string;
  tone: "ok" | "err" | "warn" | "info";
}

export const toasts = writable<Toast[]>([]);
let toastSeq = 0;

export function toast(message: string, tone: Toast["tone"] = "info", duration = 2600): void {
  const id = ++toastSeq;
  toasts.update((list) => [...list, { id, message, tone }]);
  setTimeout(() => {
    toasts.update((list) => list.filter((t) => t.id !== id));
  }, duration);
}

// banner: one dismissible line over the form. State transitions clear it, so a
// stale error never outlives the session that caused it.
export const banner = writable<{ message: string; tone: "error" | "warn" } | null>(null);

export function showBanner(message: string, tone: "error" | "warn" = "error"): void {
  banner.set({ message, tone });
}

export function hideBanner(): void {
  banner.set(null);
}

// The probe card keeps its answer until the next probe or a page reload.
export const lastProbe = writable<ProbeResult | null>(null);

// Per-node probe answers for the latency column: keyed by node id.
export interface NodeProbe {
  ms: number;
  ok: boolean;
  message: string;
}
export const nodeProbes = writable<Map<string, NodeProbe>>(new Map());
export const nodeProbeBusy = writable<Set<string>>(new Set());

// Node server → country code, for the flag chip.
export const nodeGeo = writable<Map<string, string>>(new Map());
const geoRequested = new Set<string>();

export function ensureNodeGeo(current: Settings): void {
  for (const node of current.nodes) {
    if (node.routeMode === "direct" || !node.server) continue;
    if (geoRequested.has(node.server)) continue;
    geoRequested.add(node.server);
    import("./api")
      .then(({ api }) => api.locate(node.server))
      .then((geo) => {
        if (!geo.countryCode) return;
        nodeGeo.update((m) => new Map(m).set(node.server, geo.countryCode));
      })
      .catch(() => {
        /* decoration only: a blocked lookup must not nag */
      });
  }
}

export function forgetNodeGeo(server: string): void {
  nodeGeo.update((m) => {
    const next = new Map(m);
    next.delete(server);
    return next;
  });
}

// ---- logs: the live tail ----

export const logLines = writable<LogLine[]>([]);
export const logTotal = writable<number>(0);

export function appendLogs(lines: LogLine[], reset = false): void {
  if (reset) {
    logLines.set([]);
    logTotal.set(0);
  }
  if (!lines.length) return;
  logLines.update((list) => {
    const next = [...list, ...lines];
    return next.length > 600 ? next.slice(next.length - 600) : next;
  });
  logTotal.update((n) => n + lines.length);
}

export function clearLogsLocally(): void {
  logLines.set([]);
  logTotal.set(0);
}

// ---- modals ----

export const nodeModalOpen = writable<false | "new" | "edit">(false);
export const editingNode = writable<import("./types").Node | null>(null);
export const latencyModalOpen = writable(false);
