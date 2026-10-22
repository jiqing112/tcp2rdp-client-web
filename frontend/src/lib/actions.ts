// actions.ts: the verbs the page performs. They live outside the components
// because several controls trigger the same ones (two probe buttons, three
// paths into "start"), and because each one folds the server's answer back
// into the shared state the same way.

import { api } from "./api";
import {
  app,
  appendLogs,
  clearLogsLocally,
  ensureNodeGeo,
  forgetNodeGeo,
  hideBanner,
  showBanner,
  toast,
} from "./state.svelte";
import { activeNode, isLiveState, parseRouteChoice, routeChoiceLabel } from "./format";
import type { Node, ProbeResult, Settings, Status } from "./types";

let lastState = "";
let saveTimer: ReturnType<typeof setTimeout> | undefined;

export function applyStatus(next: Status): void {
  const prev = lastState;
  lastState = next.state;
  app.settings = {
    ...app.settings,
    systemProxy: next.systemProxy,
  };
  if (next.state === "idle" && next.nodeId && app.settings.nodes.some((n) => n.id === next.nodeId)) {
    app.settings = { ...app.settings, activeNode: next.nodeId };
  }
  if (next.state === "error" && next.message) showBanner(next.message);
  else if (prev && prev !== next.state) hideBanner();
  app.status = next;
}

export async function boot(): Promise<void> {
  try {
    const data = await api.boot();
    app.info = data.info;
    app.settings = data.settings;
    app.status = data.status;
    lastState = data.status.state;
    appendLogs(data.logs);
    ensureNodeGeo();
    if (!data.info.clientPath) {
      showBanner(
        "没有找到 rdp2tcp 客户端：把它放到服务端程序旁边（或 bin/ 目录），或在「日志与高级」里填写完整路径。",
        "warn",
      );
    }
    if (data.settings.nodes.length === 0) {
      app.editingNode = null;
      app.nodeModal = "new";
    }
    app.booted = true;
  } catch (err) {
    showBanner(err instanceof Error ? err.message : String(err));
  }
}

// scheduleSave mirrors the desktop's debounce: every keystroke lands in the
// database half a second after the typing stops, and validation errors wait
// for the start attempt instead of nagging mid-sentence.
export function scheduleSave(draft: Settings): void {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(async () => {
    try {
      app.settings = await api.saveSettings(draft);
    } catch {
      /* saving is best effort: a bad field must not nag while typing */
    }
  }, 500);
}

export async function toggleRun(): Promise<void> {
  if (app.busy || app.nodeModal || app.latencyModal) return;
  const st = app.status;
  const live = st?.state === "running" || st?.state === "stopping";
  if (!live && app.settings.nodes.length === 0) {
    showBanner("还没有节点：先新增一个节点", "warn");
    app.editingNode = null;
    app.nodeModal = "new";
    return;
  }
  app.busy = live ? "stop" : "start";
  hideBanner();
  try {
    if (live) {
      applyStatus(await api.stop());
      toast("隧道已停止");
    } else {
      const saved = await api.saveSettings(app.settings);
      app.settings = saved;
      applyStatus(await api.start(saved));
      toast("隧道已启动，正在建立连接", "ok");
      if (saved.noLog) toast("已按设置关闭会话日志");
    }
  } catch (err) {
    const e = err as Error & { status?: Status };
    if (e.status) applyStatus(e.status);
    showBanner(e.message);
  } finally {
    app.busy = "";
  }
}

function recordProbe(nodeId: string, r: ProbeResult): void {
  app.nodeProbes.set(nodeId, {
    ms: r.tcpMs,
    ok: true,
    message: `TCP ${r.tcpMs} ms · TLS ${r.tlsMs} ms · ${r.ip}:${r.port}`,
  });
}

export async function probeCurrent(): Promise<void> {
  if (app.busy) return;
  const node = activeNode(app.settings);
  if (!node) {
    showBanner("还没有节点：先新增一个节点", "warn");
    app.editingNode = null;
    app.nodeModal = "new";
    return;
  }
  if (node.routeMode === "direct") {
    showBanner("直连节点没有服务器可测试", "warn");
    return;
  }
  if (!node.server) {
    showBanner("当前节点还没有填写服务器地址", "warn");
    app.editingNode = node;
    app.nodeModal = "edit";
    return;
  }
  app.busy = "probe";
  try {
    const result = await api.probe(node.server);
    app.lastProbe = result;
    recordProbe(node.id, result);
    toast(`${result.ip} · ${result.message}`, "ok");
  } catch (err) {
    app.lastProbe = null;
    showBanner(err instanceof Error ? err.message : String(err), "warn");
  } finally {
    app.busy = "";
  }
}

export async function probeNode(node: Node): Promise<void> {
  if (app.nodeProbeBusy.has(node.id) || app.busy) return;
  if (node.routeMode === "direct") {
    toast("直连节点没有服务器可测", "info");
    return;
  }
  if (!node.server) {
    toast("这个节点还没有填写服务器", "warn");
    return;
  }
  app.nodeProbeBusy.add(node.id);
  try {
    const result = await api.probe(node.server);
    recordProbe(node.id, result);
    toast(`${node.name}：TCP ${result.tcpMs} ms`, "ok");
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    app.nodeProbes.set(node.id, { ms: 0, ok: false, message });
    toast(`${node.name}：${message}`, "err");
  } finally {
    app.nodeProbeBusy.delete(node.id);
  }
}

export async function saveNode(node: Node): Promise<void> {
  const s = app.settings;
  const adding = !s.nodes.some((n) => n.id === node.id);
  const wasEmpty = s.nodes.length === 0;
  const live = isLiveState(app.status?.state);
  const nodes = [...s.nodes];
  const index = nodes.findIndex((n) => n.id === node.id);
  if (index >= 0) nodes[index] = node;
  else nodes.push(node);
  let activeNode = s.activeNode;
  if (adding && !live && (wasEmpty || !activeNode)) activeNode = node.id;
  app.settings = await api.saveSettings({ ...s, nodes, activeNode });
  app.nodeProbes.delete(node.id);
  app.nodeModal = false;
  app.editingNode = null;
  toast(index >= 0 ? "节点已更新" : "节点已添加", "ok");
  ensureNodeGeo();
}

export async function switchNode(id: string): Promise<void> {
  if (app.busy) return;
  const node = app.settings.nodes.find((n) => n.id === id);
  if (!node) return;
  const current = app.status?.nodeId || app.settings.activeNode;
  if (id === current) {
    if (!isLiveState(app.status?.state)) toast(`当前已是「${node.name}」`);
    return;
  }
  const restart = app.status?.state === "running" || app.status?.state === "starting";
  app.busy = "switch";
  try {
    // Persist any half-edited shared setting first: SelectNode reloads settings
    // on the server, so unsaved values would otherwise be lost.
    app.settings = await api.saveSettings(app.settings);
    const res = await api.selectNode(id);
    app.settings = res.settings;
    applyStatus(res.status);
    ensureNodeGeo();
    toast(restart ? `已切换到「${node.name}」，隧道正在重连` : `已选择「${node.name}」`, "ok");
  } catch (err) {
    showBanner(err instanceof Error ? err.message : String(err));
    try {
      app.settings = await api.saveSettings(app.settings);
    } catch {
      /* keep the local copy */
    }
  } finally {
    app.busy = "";
  }
}

export async function changeNodeRoute(id: string, value: string): Promise<void> {
  if (app.busy) return;
  const node = app.settings.nodes.find((n) => n.id === id);
  if (!node) return;
  const { mode, basis } = parseRouteChoice(value);
  if (node.routeMode === mode && (mode !== "rule" || node.ruleBasis === basis)) return;

  const live = isLiveState(app.status?.state);
  const active = id === (app.status?.nodeId || app.settings.activeNode);
  app.busy = "route";
  try {
    app.settings = await api.saveSettings(app.settings);
    const res = await api.setNodeRoute(id, mode, basis);
    app.settings = res.settings;
    applyStatus(res.status);
    const label = routeChoiceLabel(value);
    toast(live && active ? `已改为${label}，隧道正在重连` : `「${node.name}」已改为${label}`, "ok");
  } catch (err) {
    showBanner(err instanceof Error ? err.message : String(err));
  } finally {
    app.busy = "";
  }
}

export async function deleteNode(id: string): Promise<void> {
  if (app.busy) return;
  app.busy = "save";
  try {
    const s = app.settings;
    const node = s.nodes.find((n) => n.id === id);
    const nodes = s.nodes.filter((n) => n.id !== id);
    let activeNode = s.activeNode;
    if (activeNode === id) activeNode = nodes[0]?.id ?? "";
    app.settings = await api.saveSettings({ ...s, nodes, activeNode });
    app.nodeProbes.delete(id);
    if (node?.server) forgetNodeGeo(node.server);
    toast(`已删除「${node?.name ?? id}」`, "info");
  } catch (err) {
    showBanner(err instanceof Error ? err.message : String(err));
  } finally {
    app.busy = "";
  }
}

export async function saveLatency(mode: string, target: string, interval: number): Promise<void> {
  const res = await api.setLatency(mode, target, interval);
  app.settings = res.settings;
  applyStatus(res.status);
}

// setSystemProxy never leaves the switch lying: on error it rethrows so the
// caller can flip the checkbox back.
export async function setSystemProxy(on: boolean): Promise<void> {
  try {
    applyStatus(await api.setSystemProxy(on));
    toast(
      on
        ? app.status?.systemProxyActive
          ? "系统代理已指向本机隧道"
          : app.status?.state === "running"
            ? "已预设：隧道就绪后自动启用"
            : "已预设：连接成功后自动启用"
        : "系统代理已关闭",
      on ? "ok" : "info",
    );
  } catch (err) {
    showBanner(err instanceof Error ? err.message : String(err), "warn");
    throw err;
  }
}

export async function clearLogs(): Promise<void> {
  clearLogsLocally();
  try {
    await api.clearLogs();
  } catch {
    /* local clear already happened */
  }
}
