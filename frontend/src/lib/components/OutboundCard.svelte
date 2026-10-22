<script lang="ts">
  import { app } from "../state.svelte";
  import type { Status } from "../types";

  let {
    onToggle,
    onToggleTun,
    onTestTun,
  }: {
    onToggle: (on: boolean) => Promise<void>;
    onToggleTun: (on: boolean) => Promise<void>;
    onTestTun: () => Promise<void>;
  } = $props();

  const st = $derived(app.status as Status | null);
  const running = $derived(st?.state === "running");
  const socksMode = $derived(running ? st?.mode === "socks5" : app.settings.socks5);
  const supported = $derived(app.info?.systemProxySupported ?? st?.systemProxySupported ?? false);

  const bridgeAddr = $derived(st?.proxyAddr || `127.0.0.1:${app.settings.httpProxyPort || "34457"}`);
  const listen = $derived(
    st?.listen || `${app.settings.listenHost || "127.0.0.1"}:${app.settings.listenPort}`,
  );

  let switchBusy = $state(false);
  let tunBusy = $state(false);

  const addrText = $derived.by(() => {
    if (!socksMode && !st?.systemProxy) return "HTTP · 需要代理模式为 SOCKS5";
    if (st?.systemProxyActive) return `http://${bridgeAddr} · 生效中`;
    if (st?.systemProxy) return `http://${bridgeAddr} · ${running ? "等待就绪" : "已预设"}`;
    return `http://${bridgeAddr} · 未启用`;
  });

  const noteText = $derived(
    st?.systemProxyActive
      ? "已接管"
      : st?.systemProxy
        ? running
          ? "预设·等待"
          : "已预设"
        : running
          ? "未启用"
          : "未运行",
  );

  const hintText = $derived.by(() => {
    if (!supported) return "系统代理接管是 Windows 功能；其他系统请手动把代理指到上面的本地入口。";
    if (!socksMode) return "系统代理需要 SOCKS5 模式；切回 SOCKS5 后预设会自动生效。";
    if (st?.systemProxyActive) return "停止隧道或退出服务端会自动还原；原来你自己的代理设置会被记住";
    if (st?.systemProxy)
      return running ? "隧道回包并验证能承载数据后会自动接管" : "已预设：连接成功后会自动启用，不会在隧道不可用时接管";
    return running
      ? "打开时会先验证隧道能承载数据，再接管系统代理"
      : "可以先打开：连接成功后会自动启用，不会在隧道不可用时接管";
  });

  /* ---- TUN: same state ladder as the desktop workbench ---- */

  const tunUsable = $derived(Boolean(st?.tunSupported && st?.admin));
  const isWindows = $derived(app.info?.os === "windows");

  const tunAddrText = $derived.by(() => {
    if (st && !st.tunSupported) return "当前平台不支持";
    if (st && !st.admin) return isWindows ? "需要管理员权限" : "需要 root 权限";
    if (!socksMode && !st?.tunEnabled) return "需要 SOCKS5 模式";
    if (st?.tunActive) return "已接管 IPv4 · 可自检";
    if (st?.tunEnabled) return running ? "等待隧道就绪…" : "已预设 · 连通后自动启用";
    if (st?.tunMessage) return truncate(st.tunMessage, 44);
    return "未启用 · 接管 IPv4 流量";
  });

  const tunHintText = $derived.by(() => {
    if (st && !st.tunSupported) return "TUN 模式不支持当前平台。";
    if (st && !st.admin)
      return isWindows
        ? "TUN 要创建虚拟网卡：请以管理员身份运行 tcp2rdp-web，再打开这个开关。"
        : "TUN 要创建虚拟网卡：请用 sudo 运行 tcp2rdp-web，再打开这个开关。";
    if (!socksMode && !st?.tunEnabled) return "TUN 通过本地 SOCKS5 端口转发整机流量；切回 SOCKS5 后可启用。";
    if (st?.tunActive) return "已安装 0.0.0.0/1 + 128.0.0.0/1 默认路由；停止隧道或关闭开关会立即撤销。服务器与 DNS 保持直连。";
    if (st?.tunEnabled)
      return running
        ? "先验证隧道能承载数据，再创建虚拟网卡；不会在隧道不可用时接管。"
        : "已预设：连接成功后自动创建虚拟网卡并接管 IPv4 流量。";
    if (st?.tunMessage) return st.tunMessage;
    return "TUN 会把整机 IPv4 流量（含不认系统代理的程序）送入隧道。Windows 需要 tun2socks.exe + wintun.dll，Linux 需要 tun2socks 与 root。";
  });

  function truncate(value: string, max: number): string {
    return value.length <= max ? value : `${value.slice(0, max - 1)}…`;
  }

  async function flip(e: Event): Promise<void> {
    const input = e.currentTarget as HTMLInputElement;
    const on = input.checked;
    switchBusy = true;
    try {
      await onToggle(on);
    } catch {
      input.checked = !on;
    } finally {
      switchBusy = false;
    }
  }

  async function flipTun(e: Event): Promise<void> {
    const input = e.currentTarget as HTMLInputElement;
    const on = input.checked;
    tunBusy = true;
    try {
      await onToggleTun(on);
    } catch {
      input.checked = !on;
      app.settings.tunEnabled = !on;
    } finally {
      tunBusy = false;
    }
  }

  const tunPanelOpen = $derived(Boolean(st?.tunActive));
  const report = $derived(app.lastTunReport);
</script>

<section class="card outbound" aria-label="出口">
  <header class="outbound__head">
    <span class="outbound__title">出口</span>
    <span class="outbound__note">{noteText}</span>
  </header>

  <div class="outbound__row">
    <div class="outbound__text">
      <span class="outbound__label">系统代理</span>
      <span class="outbound__addr">{addrText}</span>
    </div>
    <label class="switch outbound__switch">
      <input
        type="checkbox"
        checked={st?.systemProxy ?? false}
        disabled={!supported || switchBusy || (!socksMode && !st?.systemProxy)}
        onchange={flip}
      />
      <span class="switch__track"></span>
    </label>
  </div>

  <div class="outbound__row">
    <div class="outbound__text">
      <span class="outbound__label">TUN 模式</span>
      <span class="outbound__addr" title={tunHintText}>{tunAddrText}</span>
    </div>
    <label class="switch outbound__switch">
      <input
        type="checkbox"
        checked={st?.tunEnabled ?? false}
        disabled={!tunUsable || tunBusy || app.busy === "tun" || (!socksMode && !st?.tunEnabled)}
        onchange={flipTun}
        title={tunUsable ? "把整机 IPv4 流量送进隧道" : tunHintText}
      />
      <span class="switch__track"></span>
    </label>
  </div>

  {#if tunPanelOpen}
    <div class="tun-panel">
      <div class="tun-panel__head">
        <span class="tun-panel__summary">{report?.summary ?? st?.tunMessage ?? "TUN 已启用"}</span>
        <button
          class="btn btn--ghost btn--sm"
          type="button"
          disabled={app.busy === "tun"}
          onclick={() => void onTestTun()}
        >
          {app.busy === "tun" ? "自检中…" : "运行自检"}
        </button>
      </div>
      {#if report}
        <ul class="tun-checks">
          {#each report.checks as check (check.name)}
            <li class="tun-check" data-ok={String(check.ok)} data-optional={String(check.optional)}>
              <span class="tun-check__mark">{check.ok ? "✓" : check.optional ? "○" : "✕"}</span>
              <span>
                <span class="tun-check__name">{check.name}</span>
                <span class="tun-check__detail">{check.detail}</span>
              </span>
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  {/if}

  <p class="outbound__hint">{hintText}</p>
  <p class="outbound__hint outbound__hint--sub">
    这两个本地入口只在运行 tcp2rdp-web 的那台机器上有效；局域网设备需把监听范围改为「局域网」。
  </p>
</section>
