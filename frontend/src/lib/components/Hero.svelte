<script lang="ts">
  import { app } from "../state.svelte";
  import {
    formatRate,
    formatUptime,
    latencyHelp,
    latencyLabel,
    probeEvery,
    sparkPaths,
    statusHeroMeta,
    truncateText,
  } from "../format";

  let {
    onOpenLatency,
    onHelp,
    onToggleDiagnose,
  }: {
    onOpenLatency: () => void;
    onHelp: (text: string) => void;
    onToggleDiagnose: () => void;
  } = $props();

  const st = $derived(app.status);
  const s = $derived(app.settings);
  const stateText = $derived(stateLabelOf(st ? st.state : "idle"));
  const heroMeta = $derived(st ? statusHeroMeta(st, s) : "没有正在运行的隧道");

  const mode = $derived(st?.latencyMode || s.latencyMode || "tunnel");
  const target = $derived(st?.probeTarget || s.probeTarget);
  const every = $derived(probeEvery(st?.probeInterval || s.probeInterval));
  const label = $derived(latencyLabel(mode));
  const running = $derived(st?.state === "running");
  const pongAge = $derived(st?.probeAge ?? 0);
  const probeErr = $derived(st?.probeError ?? "");

  const rttEmpty = $derived(!(st && st.rttMs > 0));

  const rttLine = $derived.by(() => {
    if (!st) return { label, empty: "" };
    if (running && !st.sampling) {
      return { label: "端口转发不采样隧道延迟", empty: "点右上角齿轮，可改用 TCP / Ping 检测" };
    }
    if (st.rttMs > 0) return { label, empty: "" };
    if (mode === "tunnel" && pongAge >= 15) {
      return { label: `已 ${pongAge}s 没有回包 · 排查`, empty: "还没有回包，见排查清单" };
    }
    if (probeErr) {
      return {
        label: `${label} · ${mode === "tunnel" ? "没有回包" : "无响应"}`,
        empty: `探测失败：${truncateText(probeErr, 64)} · 自动重试中`,
      };
    }
    if (pongAge >= 8) {
      return {
        label: `${label} · ${mode === "tunnel" ? "没有回包" : "无响应"}`,
        empty: probeErr ? `探测失败：${truncateText(probeErr, 64)}` : `正在等待 ${target} 回包…`,
      };
    }
    return {
      label,
      empty: running ? `正在检测 ${target}…` : `检测目标 ${target} · 运行后${every}采样`,
    };
  });

  const rttTitle = $derived(running ? `${label} · ${target}` : `检测方式：${label} · 目标 ${target}`);
  const stuck = $derived(Boolean(running && mode === "tunnel" && st?.sampling && pongAge >= 15));

  const spark = $derived(sparkPaths(st?.history ?? []));
  const sparkEmpty = $derived((st?.history ?? []).filter((v) => v > 0).length === 0);

  const up = $derived(formatRate(st?.sendBps ?? 0));
  const down = $derived(formatRate(st?.recvBps ?? 0));

  function stateLabelOf(state: string): string {
    const map: Record<string, string> = {
      idle: "未连接",
      starting: "连接中",
      running: "运行中",
      stopping: "停止中",
      error: "出错了",
    };
    return map[state] ?? state;
  }
</script>

<section class="card hero" aria-label="连接状态">
  <div class="hero__head">
    <span class="pill">
      <i class="pill__dot"></i>
      <span>{stateText}</span>
    </span>
    <span class="hero__meta">{heroMeta}</span>
    <button
      class="icon-btn hero__gear"
      type="button"
      title="延迟检测设置"
      aria-label="延迟检测设置"
      onclick={onOpenLatency}
    >
      <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path
          d="M7 1.7h2l.3 1.5.9.5 1.4-.6 1 1.7-1.1 1v1l1.1 1-1 1.7-1.4-.6-.9.5-.3 1.5H7l-.3-1.5-.9-.5-1.4.6-1-1.7 1.1-1v-1l-1.1-1 1-1.7 1.4.6.9-.5L7 1.7Z"
          stroke="currentColor"
          stroke-width="1.15"
          stroke-linejoin="round"
        />
        <circle cx="8" cy="8" r="1.8" stroke="currentColor" stroke-width="1.15" />
      </svg>
    </button>
  </div>

  <div class="hero__body">
    <div class="rtt">
      <div class="rtt__value">
        <span class="rtt__num" data-empty={String(rttEmpty)}>{rttEmpty ? "—" : st?.rttMs}</span>
        <span class="rtt__unit">ms</span>
      </div>
      <div class="rtt__label">
        <span
          role={stuck ? "button" : undefined}
          tabindex={stuck ? 0 : undefined}
          data-action={stuck ? "true" : undefined}
          title={stuck ? (app.diagOpen ? "收起排查清单" : "查看排查清单") : rttTitle}
          onclick={() => stuck && onToggleDiagnose()}
          onkeydown={(e) => {
            if (stuck && (e.key === "Enter" || e.key === " ")) {
              e.preventDefault();
              onToggleDiagnose();
            }
          }}
        >
          {rttLine.label}
        </span>
        <button
          class="hint-dot"
          type="button"
          aria-label={`${label}如何计算`}
          title={`${label}如何计算`}
          onclick={(e) => {
            e.stopPropagation();
            onHelp(latencyHelp(mode));
          }}
        >
          ?
        </button>
      </div>
    </div>

    <div class="spark" data-empty={String(sparkEmpty)}>
      <svg viewBox="0 0 320 62" preserveAspectRatio="none" aria-hidden="true">
        <defs>
          <linearGradient id="spark-fill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="var(--live)" stop-opacity="0.26" />
            <stop offset="100%" stop-color="var(--live)" stop-opacity="0" />
          </linearGradient>
        </defs>
        <line class="spark__grid" x1="0" y1="0.5" x2="320" y2="0.5" />
        <line class="spark__grid" x1="0" y1="31" x2="320" y2="31" opacity="0.6" />
        <path class="spark__area" d={spark.area} />
        <path class="spark__line" d={spark.line} />
      </svg>
      <div class="spark__empty">{sparkEmpty ? rttLine.empty || "开始后这里显示延迟曲线" : ""}</div>
    </div>
  </div>

  <dl class="metrics">
    <div>
      <dt>上行</dt>
      <dd>{up[0]}<span>{up[1]}</span></dd>
    </div>
    <div>
      <dt>下行</dt>
      <dd>{down[0]}<span>{down[1]}</span></dd>
    </div>
    <div>
      <dt>连接</dt>
      <dd>{st?.streams ?? 0}</dd>
    </div>
    <div>
      <dt>时长</dt>
      <dd>{formatUptime(st?.uptimeSec ?? 0)}</dd>
    </div>
  </dl>
</section>

<style>
  /* The empty text is driven by both the sample store and the label branch. */
  .spark__empty:empty {
    display: none;
  }
</style>
