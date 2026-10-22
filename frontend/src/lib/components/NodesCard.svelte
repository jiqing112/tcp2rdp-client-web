<script lang="ts">
  import { app, ensureNodeGeo } from "../state.svelte";
  import { countryLabel, isLiveState, latencyLabel, routeChoice, routeChoices } from "../format";

  let {
    onSwitch,
    onRoute,
    onProbe,
    onEdit,
    onDelete,
    onAdd,
  }: {
    onSwitch: (id: string) => void;
    onRoute: (id: string, value: string) => void;
    onProbe: (node: import("../types").Node) => void;
    onEdit: (node: import("../types").Node) => void;
    onDelete: (id: string) => void;
    onAdd: () => void;
  } = $props();

  // A second click on the trash inside a few seconds confirms the delete.
  let pendingDelete = $state("");
  let deleteTimer: ReturnType<typeof setTimeout> | undefined;

  const nodes = $derived(app.settings.nodes);
  const live = $derived(isLiveState(app.status?.state));
  const activeId = $derived(app.status?.nodeId || app.settings.activeNode);

  const ICON_PROBE =
    '<svg viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M2 8h2.2l1.4-4 2.2 8 1.6-5.6 1.2 3 0.9-1.4H14" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>';
  const ICON_EDIT =
    '<svg viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="m10.9 2.6 2.5 2.5-7.1 7.1-3 .5.5-3 7.1-7.1Z" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"/><path d="m9.6 3.9 2.5 2.5" stroke="currentColor" stroke-width="1.3"/></svg>';
  const ICON_TRASH =
    '<svg viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M3.2 4.4h9.6M6.2 4.4V3.1h3.6v1.3M4.4 4.4l.6 8.5h6l.6-8.5M6.6 6.7v3.9M9.4 6.7v3.9" stroke="currentColor" stroke-width="1.25" stroke-linecap="round" stroke-linejoin="round"/></svg>';

  const nodesHint = $derived(
    nodes.length === 0 ? "还没有节点" : live ? "运行中：点击其他节点会自动重连" : "点击行切换当前节点",
  );

  function geoOf(node: import("../types").Node): string {
    return node.routeMode === "direct" ? "" : (app.nodeGeo.get(node.server) ?? "");
  }

  function serverText(node: import("../types").Node): string {
    return node.routeMode === "direct" ? "直连 · 不经过服务器" : node.server || "未填写服务器";
  }

  function latencyCell(node: import("../types").Node): {
    text: string;
    title: string;
    tone: string;
  } {
    const id = node.id;
    const st = app.status;
    if (app.nodeProbeBusy.has(id)) {
      return { text: "…", title: "正在测试服务器", tone: "wait" };
    }
    if (id === activeId && st?.state === "running") {
      if (st.sampling) {
        if (st.rttMs > 0) {
          return {
            text: `${st.rttMs} ms`,
            title: `${latencyLabel(st.latencyMode)} · ${st.probeTarget || app.settings.probeTarget}`,
            tone: "live",
          };
        }
        return {
          text: st.probeError ? "失败" : "…",
          title: st.probeError || `等待${latencyLabel(st.latencyMode)}回包`,
          tone: st.probeError ? "error" : "wait",
        };
      }
      return { text: "—", title: "端口转发模式不采样隧道延迟；可在延迟设置里改用 TCP / Ping", tone: "muted" };
    }
    const probe = app.nodeProbes.get(id);
    if (probe) {
      return probe.ok
        ? { text: `${probe.ms} ms`, title: probe.message, tone: "probe" }
        : { text: "失败", title: probe.message, tone: "error" };
    }
    if (node.routeMode === "direct") {
      return { text: "—", title: "直连节点不经过服务器", tone: "muted" };
    }
    return { text: "—", title: "点右侧按钮测试服务器", tone: "muted" };
  }

  function handleDelete(node: import("../types").Node): void {
    if (live && node.id === activeId) {
      return;
    }
    if (pendingDelete !== node.id) {
      pendingDelete = node.id;
      clearTimeout(deleteTimer);
      deleteTimer = setTimeout(() => {
        pendingDelete = "";
      }, 3200);
      return;
    }
    clearTimeout(deleteTimer);
    pendingDelete = "";
    onDelete(node.id);
  }

  function rowKeydown(e: KeyboardEvent, node: import("../types").Node): void {
    if (e.key !== "Enter" && e.key !== " ") return;
    if ((e.target as HTMLElement).closest("button,select,input,textarea")) return;
    e.preventDefault();
    onSwitch(node.id);
  }

  $effect(() => {
    // New rows appear after settings change; ask once for their flags.
    if (app.booted) ensureNodeGeo();
  });
</script>

<section class="card nodes" aria-label="节点">
  <header class="nodes__head">
    <div class="nodes__titles">
      <span class="nodes__title">节点</span>
      <span class="nodes__hint">{nodesHint}</span>
    </div>
    <button class="btn btn--ghost btn--sm" type="button" onclick={onAdd}>
      <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path d="M8 3.2v9.6M3.2 8h9.6" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
      </svg>
      新增节点
    </button>
  </header>
  <div class="nodes__list">
    {#if nodes.length === 0}
      <div class="nodes__empty">
        <span>还没有节点。填写服务器与密钥，或者添加一个直连节点。</span>
        <button class="btn btn--ghost btn--sm" type="button" onclick={onAdd}>新增第一个节点</button>
      </div>
    {:else}
      {#each nodes as node (node.id)}
        {@const isActive = node.id === activeId}
        {@const cell = latencyCell(node)}
        {@const geo = geoOf(node)}
        {@const confirming = pendingDelete === node.id}
        <div
          class="node-row"
          data-active={String(isActive)}
          data-live={String(live && isActive)}
          role="button"
          tabindex="0"
          aria-label={`切换到 ${node.name}`}
          onclick={(e) => {
            if ((e.target as HTMLElement).closest(".route-select")) return;
            if ((e.target as HTMLElement).closest("[data-act]")) return;
            onSwitch(node.id);
          }}
          onkeydown={(e) => rowKeydown(e, node)}
        >
          <span class="node-row__pick" aria-hidden="true"></span>
          <div class="node-row__id">
            <span class="node-row__name">{node.name}</span>
            <span
              class="node-row__server"
              title={geo ? `${countryLabel(geo)} · ${serverText(node)}` : serverText(node)}
            >
              {#if geo}
                <img
                  class="geo-flag"
                  src="flags/{geo.toLowerCase()}.svg"
                  alt=""
                  onerror={(e) => {
                    const img = e.currentTarget as HTMLImageElement;
                    const chip = document.createElement("span");
                    chip.className = "geo-flag-code";
                    chip.textContent = geo.toUpperCase();
                    img.replaceWith(chip);
                  }}
                />
              {/if}{serverText(node)}
            </span>
          </div>
          <select
            class="route-badge route-select"
            data-route={node.routeMode}
            aria-label={`切换「${node.name}」的分流模式`}
            title={live && isActive ? "运行中：切换后隧道会自动重连" : "切换分流模式（全局 / 分流 / 分流·IP / 直连）"}
            disabled={app.busy !== ""}
            value={routeChoice(node)}
            onchange={(e) => onRoute(node.id, (e.currentTarget as HTMLSelectElement).value)}
          >
            {#each routeChoices as choice (choice.value)}
              <option value={choice.value}>{choice.label}</option>
            {/each}
          </select>
          <span class="node-row__latency" data-tone={cell.tone} title={cell.title}>{cell.text}</span>
          <div class="node-row__acts">
            <button
              class="node-act"
              data-act="probe"
              type="button"
              title={node.routeMode === "direct" ? "直连节点没有服务器可测" : "测试服务器握手延迟"}
              aria-label="测试服务器握手延迟"
              disabled={app.nodeProbeBusy.has(node.id) || node.routeMode === "direct"}
              onclick={() => onProbe(node)}
            >
              {@html ICON_PROBE}
            </button>
            <button
              class="node-act"
              data-act="edit"
              type="button"
              title={live && isActive ? "先停止隧道，再修改当前节点" : "编辑节点"}
              aria-label="编辑节点"
              disabled={live && isActive}
              onclick={() => onEdit(node)}
            >
              {@html ICON_EDIT}
            </button>
            <button
              class="node-act"
              class:node-act--confirm={confirming}
              data-act="delete"
              type="button"
              title={confirming ? "再点一次确认删除" : "删除节点"}
              aria-label="删除节点"
              disabled={live && isActive}
              onclick={() => handleDelete(node)}
            >
              {#if confirming}
                <span class="node-act__text">确认删除</span>
              {:else}
                {@html ICON_TRASH}
              {/if}
            </button>
          </div>
        </div>
      {/each}
    {/if}
  </div>
</section>

<style>
  /* The confirm label needs text sizing the icon button does not have. */
  .node-act__text {
    font-size: 11px;
    font-weight: 550;
    line-height: 1;
  }
</style>
