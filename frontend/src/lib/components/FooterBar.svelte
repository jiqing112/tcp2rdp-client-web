<script lang="ts">
  import { app } from "../state.svelte";
  import { activeNode, formatSize, longRoute } from "../format";

  let { onStart, onProbe }: { onStart: () => void; onProbe: () => void } = $props();

  const st = $derived(app.status);
  const state = $derived(st?.state);
  const live = $derived(state === "running" || state === "stopping");
  const starting = $derived(state === "starting");
  const busy = $derived(app.busy);
  const probeBusy = $derived(busy === "probe");
  const node = $derived(activeNode(app.settings));

  const btnClass = $derived(live ? "btn btn--danger" : "btn btn--primary");
  const btnLabel = $derived(
    busy === "start" || busy === "stop" ? (live ? "正在停止…" : "正在启动…") : live ? "停止隧道" : starting ? "正在连接…" : "启动隧道",
  );
  const btnDisabled = $derived(busy !== "" || starting || state === "stopping");

  const hintHtml = $derived.by(() => {
    const esc = (t: string) =>
      t.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
    if (busy === "probe") return esc("正在测试服务器…");
    if (busy === "switch") return esc("正在切换节点…");
    if (busy === "route") return esc("正在切换分流…");
    if (busy === "save") return esc("正在保存节点…");
    if (!st) {
      return node ? esc(`当前节点「${node.name}」`) : esc("填写服务器与密钥后启动");
    }
    if (st.state === "running") {
      const up = formatSize(st.sendTotalB ?? 0);
      const down = formatSize(st.recvTotalB ?? 0);
      const listen = st.listen || `${app.settings.listenHost || "127.0.0.1"}:${app.settings.listenPort}`;
      const name = st.nodeName || node?.name || "";
      return `${esc(name)} · 本地 <b>${esc(listen)}</b> → <b>${esc(st.server || "直连")}</b> ｜ 上行 ${up} · 下行 ${down}`;
    }
    if (st.state === "error") return esc("启动失败，修正后重试");
    if (st.state === "starting") return esc("正在建立隧道…");
    if (st.state === "stopping") return esc("正在停止…");
    return node
      ? `当前节点 <b>${esc(node.name)}</b> · ${esc(longRoute(node.routeMode))} ｜ 点击节点可切换`
      : esc("还没有节点：点「新增节点」开始");
  });
</script>

<footer class="footer">
  <div class="footer__hint">{@html hintHtml}</div>
  <div class="actions">
    <button class="btn btn--ghost" type="button" disabled={busy !== ""} onclick={onProbe}>
      <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path
          d="M2 8h2.2l1.4-4 2.2 8 1.6-5.6 1.2 3 0.9-1.4H14"
          stroke="currentColor"
          stroke-width="1.4"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
      </svg>
      <span>{probeBusy ? "测试中…" : "测试当前节点"}</span>
    </button>
    <button class={btnClass} type="button" disabled={btnDisabled} onclick={onStart}>
      {#if busy === "start" || busy === "stop"}
        <span class="spinner"></span>
      {/if}
      {btnLabel}
    </button>
  </div>
</footer>
