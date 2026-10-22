<script lang="ts">
  import { app } from "../state.svelte";
  import type { Status } from "../types";

  let { onToggle }: { onToggle: (on: boolean) => Promise<void> } = $props();

  const st = $derived(app.status as Status | null);
  const running = $derived(st?.state === "running");
  const socksMode = $derived(running ? st?.mode === "socks5" : app.settings.socks5);
  const supported = $derived(app.info?.systemProxySupported ?? st?.systemProxySupported ?? false);

  const bridgeAddr = $derived(st?.proxyAddr || `127.0.0.1:${app.settings.httpProxyPort || "34457"}`);
  const listen = $derived(
    st?.listen || `${app.settings.listenHost || "127.0.0.1"}:${app.settings.listenPort}`,
  );

  let switchBusy = $state(false);

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
      <span class="outbound__label">SOCKS5 入口</span>
      <span class="outbound__addr" title="{listen}（勾选远程 DNS 即 socks5h）">{listen}</span>
    </div>
  </div>

  <div class="outbound__row">
    <div class="outbound__text">
      <span class="outbound__label">HTTP 入口</span>
      <span class="outbound__addr">
        {socksMode ? `http://${bridgeAddr}` : "需要 SOCKS5 模式"}
      </span>
    </div>
  </div>

  <p class="outbound__hint">{hintText}</p>
  <p class="outbound__hint outbound__hint--sub">
    这两个本地入口只在运行 tcp2rdp-web 的那台机器上有效；局域网设备需把监听范围改为「局域网」。
  </p>
</section>
