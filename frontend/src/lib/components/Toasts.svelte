<script lang="ts">
  import { app } from "../state.svelte";
</script>

<div class="toasts" aria-live="polite">
  {#each app.toasts as t (t.id)}
    <div class="toast" data-tone={t.tone}>{t.message}</div>
  {/each}
</div>
