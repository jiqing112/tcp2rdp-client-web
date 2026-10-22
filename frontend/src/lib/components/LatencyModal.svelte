<script lang="ts">
  import { app, toast } from "../state.svelte";
  import { latencyHelp, latencyLabel, probeEvery } from "../format";

  let {
    onSave,
    onClose,
  }: {
    onSave: (mode: string, target: string, interval: number) => Promise<void>;
    onClose: () => void;
  } = $props();

  const st = $derived(app.status);

  let mode = $state(st?.latencyMode || app.settings.latencyMode || "tunnel");
  let target = $state(st?.probeTarget || app.settings.probeTarget || "");
  let interval = $state(String(st?.probeInterval || app.settings.probeInterval || 1800));
  let error = $state("");
  let saving = $state(false);

  const running = $derived(st?.state === "running");

  const modeNote = $derived(
    mode === "tunnel"
      ? "经隧道完成一次 SOCKS5 连接，反映整条链路；端口转发模式下不采样。"
      : mode === "tcp"
        ? "从本机直接对目标发起 TCP 握手，不经过隧道；用来判断目标本身离你多远。"
        : "系统 ping 一个 ICMP 包并等待回复，不经过隧道；部分服务器会禁用 ICMP。",
  );
  const targetNote = $derived(mode === "ping" ? "主机名或 IP，不含端口" : "host:port，例如 www.baidu.com:443");
  const footHint = $derived(
    `${running ? "保存后立即采样" : "启动时立即采样"}，之后${probeEvery(intervalFromInput())}采样一次`,
  );

  function intervalFromInput(): number {
    const n = Number.parseInt(interval, 10);
    return Number.isFinite(n) && n >= 1 && n <= 86400 ? n : 1800;
  }

  // Switching mode converts what is already typed instead of asking the user
  // to add or drop the port by hand.
  function retarget(next: string): void {
    const v = target.trim();
    if (!v) return;
    if (next === "ping") {
      const bracketed = /^\[([^\]]+)\](?::\d+)?$/.exec(v);
      if (bracketed?.[1]) {
        target = bracketed[1];
        return;
      }
      if ((v.match(/:/g)?.length ?? 0) === 1 && /:\d+$/.test(v)) {
        target = v.slice(0, v.lastIndexOf(":"));
      }
      return;
    }
    if (/^\[[^\]]+\]$/.test(v)) {
      target = `${v}:443`;
      return;
    }
    const colons = v.match(/:/g)?.length ?? 0;
    if (colons > 1) target = `[${v}]:443`;
    else if (colons === 0) target = `${v}:443`;
  }

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const t = target.trim();
    if (!t) {
      error = mode === "ping" ? "请填写要 ping 的主机名或 IP" : "请填写目标地址，例如 www.baidu.com:443";
      return;
    }
    if (/\s/.test(t)) {
      error = "探测目标不能包含空格";
      return;
    }
    const raw = interval.trim();
    const n = Number(raw);
    if (!raw || !Number.isInteger(n) || n < 1 || n > 86400) {
      error = "采样间隔应为 1–86400 的整数（秒）";
      return;
    }
    error = "";
    saving = true;
    try {
      await onSave(mode, t, n);
      app.latencyModal = false;
      toast(
        app.status?.state === "running"
          ? `已改用${latencyLabel(app.settings.latencyMode)} · ${app.settings.probeTarget}`
          : `已保存：${latencyLabel(app.settings.latencyMode)} · ${app.settings.probeTarget}`,
        "ok",
      );
    } catch (e2) {
      error = e2 instanceof Error ? e2.message : String(e2);
    } finally {
      saving = false;
    }
  }
</script>

<div class="modal">
  <div
    class="modal__backdrop"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) onClose();
    }}
  ></div>
  <div class="modal__panel modal__panel--latency" role="dialog" aria-modal="true" aria-labelledby="latency-modal-title">
    <header class="modal__head">
      <div class="modal__titles">
        <span class="modal__title" id="latency-modal-title">延迟检测</span>
        <span class="modal__sub">选择检测方式、探测目标与采样间隔</span>
      </div>
      <button class="icon-btn" type="button" aria-label="关闭" title="关闭" onclick={onClose}>
        <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
          <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
        </svg>
      </button>
    </header>

    <form class="modal__form" autocomplete="off" novalidate onsubmit={submit}>
      <div class="modal__body">
        <div class="field field--top">
          <span class="field__label">检测方式</span>
          <span class="field__control">
            <div class="seg" role="radiogroup" aria-label="延迟检测方式">
              <label class="seg__item">
                <input type="radio" name="latency-mode" value="tunnel" bind:group={mode} onchange={() => retarget("tunnel")} />
                <span>隧道</span>
              </label>
              <label class="seg__item">
                <input type="radio" name="latency-mode" value="tcp" bind:group={mode} onchange={() => retarget("tcp")} />
                <span>TCP</span>
              </label>
              <label class="seg__item">
                <input type="radio" name="latency-mode" value="ping" bind:group={mode} onchange={() => retarget("ping")} />
                <span>Ping</span>
              </label>
            </div>
          </span>
        </div>

        <label class="field">
          <span class="field__label">探测目标</span>
          <span class="field__control">
            <input
              class="input"
              bind:value={target}
              placeholder={mode === "ping" ? "www.baidu.com" : "www.baidu.com:443"}
              spellcheck="false"
            />
          </span>
          <span class="field__note">{targetNote}</span>
        </label>

        <label class="field">
          <span class="field__label">采样间隔</span>
          <span class="field__control">
            <input
              class="input"
              type="number"
              bind:value={interval}
              inputmode="numeric"
              min="1"
              max="86400"
              step="1"
              placeholder="1800"
              oninput={() => (error = "")}
            />
            <span class="suffix">秒</span>
          </span>
          <span class="field__note">默认 1800 秒；启动和保存后都会立即采样一次</span>
        </label>

        <p class="modal__note">{modeNote}</p>
        {#if error}
          <p class="modal__error">{error}</p>
        {/if}
      </div>

      <footer class="modal__foot">
        <span class="modal__foot-hint">{footHint}</span>
        <button class="btn btn--ghost" type="button" onclick={onClose}>取消</button>
        <button class="btn btn--primary" type="submit" disabled={saving}>{saving ? "保存中…" : "保存"}</button>
      </footer>
    </form>
  </div>
</div>
