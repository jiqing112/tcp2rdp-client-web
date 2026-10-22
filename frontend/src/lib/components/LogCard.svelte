<script lang="ts">
  import { app } from "../state.svelte";

  let { onClear }: { onClear: () => void } = $props();

  let body: HTMLElement | undefined = $state();
  const MAX_LINES = 600;

  // Keep the view pinned to the newest line, but only when the reader is
  // already near the bottom: scrolled-up means "I am reading".
  function pin(): void {
    if (!body) return;
    const nearBottom = body.scrollHeight - body.scrollTop - body.clientHeight < 48;
    if (nearBottom) body.scrollTop = body.scrollHeight;
  }

  $effect(() => {
    void app.logLines.length;
    pin();
  });
</script>

<section class="card log">
  <header class="log__head">
    <span class="log__title">会话日志</span>
    <span class="log__count">{app.logTotal}</span>
    <button class="log__clear" type="button" onclick={onClear}>清空</button>
  </header>
  <div class="log__body" bind:this={body}>
    {#if app.logLines.length === 0}
      <div class="log__empty">
        启动隧道后，客户端日志会出现在这里。<br />
        按「日志级别」过滤，最多保留 3000 行。
      </div>
    {/if}
    <div>
      {#each app.logLines.slice(-MAX_LINES) as line (line.seq)}
        <div class="log-line" data-level={line.level}>
          <span class="log-line__time">{line.time}</span>
          <span class="log-line__text">{line.text}</span>
        </div>
      {/each}
    </div>
  </div>
</section>
