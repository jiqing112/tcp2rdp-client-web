<script lang="ts">
  import { app } from "../state.svelte";

  let { open, onClose }: { open: boolean; onClose: () => void } = $props();

  const st = $derived(app.status);
  const stuck = $derived(
    Boolean(
      st &&
        st.state === "running" &&
        st.latencyMode === "tunnel" &&
        st.sampling &&
        (st.probeAge ?? 0) >= 15,
    ),
  );
  const probeErr = $derived(st?.probeError ?? "");
  const target = $derived(st?.probeTarget || app.settings.probeTarget);
  const everSampled = $derived((st?.history?.length ?? 0) > 0);
  const port = $derived((st?.server ?? "").split(":").pop() || "3389");
</script>

{#if open && stuck && st}
  <section class="card diagnose" aria-live="polite">
    <div class="diagnose__head">
      <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path d="M8 2.4 14.6 13.4H1.4L8 2.4Z" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round" />
        <path d="M8 6.4v3.1" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
        <circle cx="8" cy="11.5" r="0.8" fill="currentColor" />
      </svg>
      <span>{probeErr ? "探测目标连不上" : "隧道还没有回包"}</span>
      <em>排查</em>
      <button class="diagnose__close" type="button" aria-label="收起排查" title="收起" onclick={onClose}>✕</button>
    </div>
    <p>
      {#if probeErr}
        经隧道连接探测目标失败：<code>{probeErr}</code>
      {:else if everSampled}
        已经 <b>{st.probeAge}</b> 秒没有新的回包（上一次 {st.rttMs}ms）。按顺序检查：
      {:else}
        已经等了 <b>{st.probeAge}</b> 秒，一个回包都没收到。按顺序检查：
      {/if}
    </p>
    <ol class="diagnose__steps">
      <li>服务器上的 <code>rdp2tcp -type server</code> 是否还在运行</li>
      <li>两端的 <code>-key</code> 是否一致（当前 <b>{st.keyLen}</b> 位）</li>
      <li>服务器是否放行了 <b>{port}</b> 端口（云安全组 / firewalld）</li>
      <li>「探测目标」<code>{target}</code> 是否在服务端 <code>-allow</code> 白名单里</li>
    </ol>
    <p class="diagnose__foot">
      {probeErr
        ? "如果服务器开了 -allow 白名单，探测目标必须在名单里，或者把它换成一个被允许的地址。"
        : "「测试服务器」只验证服务端握手与证书；能握手却不回数据，问题多在密钥或白名单。"}
    </p>
  </section>
{/if}
