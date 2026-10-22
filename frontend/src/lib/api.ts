import type {
  GeoInfo,
  Info,
  LogBatch,
  LogLine,
  ProbeResult,
  Settings,
  Status,
  TunReport,
} from "./types";

// api is the thin HTTP layer between the page and the Go server. Every call
// that changes something answers with the fresh state, so the page never has
// to guess what the server did.

// The server may require a shared token (-token). The page picks it up from
// the URL it was opened with (?token=…), remembers it, and attaches it to
// every call afterwards.
let authToken = "";
try {
  const url = new URL(window.location.href);
  const fromUrl = url.searchParams.get("token");
  if (fromUrl) {
    authToken = fromUrl;
    localStorage.setItem("tcp2rdp.token", fromUrl);
    url.searchParams.delete("token");
    window.history.replaceState(null, "", url.pathname + (url.searchParams.toString() ? `?${url.searchParams}` : ""));
  } else {
    authToken = localStorage.getItem("tcp2rdp.token") ?? "";
  }
} catch {
  /* storage may be blocked; the URL token still works for this load */
}

function withToken(path: string): string {
  if (!authToken) return path;
  return path + (path.includes("?") ? "&" : "?") + "token=" + encodeURIComponent(authToken);
}

class ApiError extends Error {
  constructor(message: string) {
    super(message);
  }
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(withToken(path), {
      headers: { "Content-Type": "application/json" },
      ...init,
    });
  } catch {
    throw new ApiError("连不上服务端，请确认 tcp2rdp-web 还在运行");
  }
  const text = await res.text();
  let body: any = null;
  try {
    body = text ? JSON.parse(text) : null;
  } catch {
    /* a proxy error page is not JSON; fall through to the status check */
  }
  if (!res.ok) {
    const message = body?.error ?? `请求失败（HTTP ${res.status}）`;
    // start/stop answers carry the moved-along status alongside the error.
    const err = new ApiError(message) as ApiError & { status?: Status };
    if (body?.status) err.status = body.status as Status;
    throw err;
  }
  return body as T;
}

export interface Boot {
  info: Info;
  settings: Settings;
  status: Status;
  logs: LogLine[];
}

export const api = {
  boot: () => call<Boot>("/api/boot"),

  saveSettings: (s: Settings) =>
    call<Settings>("/api/settings", { method: "POST", body: JSON.stringify(s) }),

  start: (s: Settings) =>
    call<Status>("/api/start", { method: "POST", body: JSON.stringify(s) }),

  stop: () => call<Status>("/api/stop", { method: "POST", body: "{}" }),

  selectNode: (id: string) =>
    call<{ status: Status; settings: Settings }>("/api/nodes/select", {
      method: "POST",
      body: JSON.stringify({ id }),
    }),

  setNodeRoute: (id: string, routeMode: string, ruleBasis: string) =>
    call<{ status: Status; settings: Settings }>("/api/nodes/route", {
      method: "POST",
      body: JSON.stringify({ id, routeMode, ruleBasis }),
    }),

  probe: (server: string) =>
    call<ProbeResult>("/api/probe", {
      method: "POST",
      body: JSON.stringify({ server }),
    }),

  setLatency: (mode: string, target: string, interval: number) =>
    call<{ status: Status; settings: Settings }>("/api/latency", {
      method: "POST",
      body: JSON.stringify({ mode, target, interval }),
    }),

  setSystemProxy: (on: boolean) =>
    call<Status>("/api/sysproxy", {
      method: "POST",
      body: JSON.stringify({ on }),
    }),

  setTun: (on: boolean) =>
    call<Status>("/api/tun", {
      method: "POST",
      body: JSON.stringify({ on }),
    }),

  testTun: () => call<TunReport>("/api/tun/test", { method: "POST", body: "{}" }),

  clearLogs: () => call<{ ok: boolean }>("/api/logs", { method: "DELETE" }),

  locate: (server: string) =>
    call<GeoInfo>(`/api/locate?server=${encodeURIComponent(server)}`),

  // setWebListen moves the workbench's own listener; the answer is the fresh
  // Info with the new addresses. A changed port makes the current URL stale.
  setWebListen: (host: string, port: string) =>
    call<Info>("/api/weblisten", {
      method: "POST",
      body: JSON.stringify({ host, port }),
    }),

  status: () => call<Status>("/api/status"),
};

// openEvents wires the live SSE stream. It returns a close function; the page
// reopens with backoff if the connection drops.
export function openEvents(
  onStatus: (s: Status) => void,
  onLogs: (b: LogBatch) => void,
): () => void {
  const es = new EventSource(withToken("/api/events"));
  es.addEventListener("status", (ev) => {
    try {
      onStatus(JSON.parse((ev as MessageEvent).data));
    } catch {
      /* a partial frame is re-sent wholesale by the server */
    }
  });
  es.addEventListener("logs", (ev) => {
    try {
      onLogs(JSON.parse((ev as MessageEvent).data));
    } catch {
      /* ignore */
    }
  });
  return () => es.close();
}
