<script lang="ts">
  import { app, showBanner, toast } from "../state.svelte";
  import { api } from "../api";

  // The workbench's own listener: scope (this machine / LAN) and port. Applied
  // live — the server rebinds without a restart, and the answer carries the
  // addresses the page should advertise.

  const host = $derived(app.info?.webHost ?? "127.0.0.1");
  const port = $derived(app.info?.webPort ?? "8080");
  const loaded = $derived(Boolean(app.info));

  let draftHost = $state("127.0.0.1");
  let draftPort = $state("8080");
  let edited = $state(false); // user typed something the server has not confirmed
  let applying = $state(false);

  // Until the user edits, the draft follows the server's copy (boot answer,
  // apply answer). After that their half-typed value is never clobbered.
  $effect(() => {
    const info = app.info;
    if (info && !edited) {
      draftHost = info.webHost;
      draftPort = info.webPort;
    }
  });

  function markEdited(): void {
    edited = true;
  }

  const dirty = $derived(loaded && (draftHost !== host || draftPort !== port));
  const lanOpen = $derived(host === "0.0.0.0" || draftHost === "0.0.0.0");

  const localURL = $derived(`http://127.0.0.1:${port}`);
  const lanURL = $derived(
    app.info?.lanIp ? `http://${app.info.lanIp}:${port}` : "本机没有局域网地址",
  );

  async function apply(): Promise<void> {
    if (applying || !loaded) return;
    const trimmed = draftPort.trim();
    if (!/^\d+$/.test(trimmed) || Number(trimmed) < 1 || Number(trimmed) > 65535) {
      showBanner(`端口无效：${draftPort}，应为 1-65535`, "warn");
      return;
    }
    applying = true;
    try {
      const info = await api.setWebListen(draftHost, trimmed);
      const portChanged = info.webPort !== app.info?.webPort;
      app.info = info;
      edited = false;
      draftHost = info.webHost;
      draftPort = info.webPort;
      if (portChanged) {
        toast(`工作台已改到 ${info.webHost}:${info.webPort}；当前页面地址已失效，请用新地址重新打开`, "warn", 10000);
      } else if (info.webHost === "0.0.0.0") {
        toast("工作台已对局域网开放，防火墙已放行该端口", "ok");
      } else {
        toast("工作台已改为仅本机访问", "ok");
      }
    } catch (err) {
      showBanner(err instanceof Error ? err.message : String(err), "warn");
    } finally {
      applying = false;
    }
  }
</script>

<section class="card outbound" aria-label="工作台">
  <header class="outbound__head">
    <span class="outbound__title">工作台</span>
    <span class="outbound__note">{host === "0.0.0.0" ? "局域网开放" : "仅本机"}</span>
  </header>

  <div class="web-row">
    <select
      class="input web-scope"
      bind:value={draftHost}
      onchange={markEdited}
      disabled={!loaded || applying}
      aria-label="监听范围"
      title="仅本机：只有这台电脑能打开工作台；局域网：同一网络内的设备都能打开"
    >
      <option value="127.0.0.1">仅本机</option>
      <option value="0.0.0.0">局域网</option>
    </select>
    <input
      class="input web-port"
      bind:value={draftPort}
      oninput={markEdited}
      disabled={!loaded || applying}
      inputmode="numeric"
      placeholder="8080"
      spellcheck="false"
      aria-label="工作台端口"
      title="改端口后需要用新地址重新打开页面"
      onkeydown={(e) => {
        if (e.key === "Enter") void apply();
      }}
    />
    <button class="btn btn--ghost btn--sm" type="button" disabled={!dirty || applying} onclick={apply}>
      {applying ? "应用中…" : "应用"}
    </button>
  </div>

  <p class="outbound__hint">本机地址：{localURL}</p>
  <p class="outbound__hint">局域网地址：{lanURL}</p>
  {#if lanOpen}
    <p class="outbound__hint outbound__hint--sub">
      {app.info?.tokenRequired
        ? "已开启访问令牌：局域网设备需用带 token 的完整地址打开。"
        : "对局域网开放前，建议以 -token 启动服务端，避免任何能连通的设备操作隧道。"}
      切换或换端口立即生效，无需重启；Windows 防火墙会自动跟随放行/撤销。
    </p>
  {:else}
    <p class="outbound__hint outbound__hint--sub">改为「局域网」后，同一网络内的设备可以用上面的地址打开工作台。</p>
  {/if}
</section>

<style>
  .web-row {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    padding: 2px var(--sp-4) 10px;
  }
  .web-scope {
    flex: none;
    width: 92px;
  }
  .web-port {
    flex: 1;
    min-width: 0;
  }
</style>
