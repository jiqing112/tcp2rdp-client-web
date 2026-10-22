<script lang="ts">
  import { app, hideBanner } from "../state.svelte";

  const banner = $derived(app.banner);
</script>

{#if banner}
  <div class="banner" class:banner--warn={banner.tone === "warn"}>
    <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <circle cx="8" cy="8" r="6.4" stroke="currentColor" stroke-width="1.3" />
      <path d="M8 4.8v3.9" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
      <circle cx="8" cy="11.1" r="0.8" fill="currentColor" />
    </svg>
    <span>{banner.message}</span>
    <button class="banner__close" type="button" aria-label="关闭提示" onclick={hideBanner}>✕</button>
  </div>
{/if}
