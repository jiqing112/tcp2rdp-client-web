<script lang="ts">
  import { toast } from "../state.svelte";

  type ThemeMode = "auto" | "light" | "dark";
  const THEME_KEY = "tcp2rdp.theme";

  let mode = $state<ThemeMode>("auto");

  const sunIcon =
    '<circle cx="8" cy="8" r="3.1" stroke="currentColor" stroke-width="1.3"/><path d="M8 1.2v1.4M8 13.4v1.4M14.8 8h-1.4M2.6 8H1.2M12.8 3.2l-1 1M4.2 11.8l-1 1M12.8 12.8l-1-1M4.2 4.2l-1-1" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/>';
  const moonIcon =
    '<path d="M13.4 9.6A5.8 5.8 0 0 1 6.4 2.6a5.9 5.9 0 1 0 7 7Z" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"/>';
  const autoIcon =
    '<circle cx="8" cy="8" r="6.3" stroke="currentColor" stroke-width="1.3"/><path d="M8 1.7v12.6a6.3 6.3 0 0 0 0-12.6Z" fill="currentColor"/>';

  const icon = $derived(mode === "auto" ? autoIcon : mode === "light" ? sunIcon : moonIcon);
  const title = $derived(`外观：${mode === "auto" ? "跟随系统" : mode === "light" ? "浅色" : "深色"}（点击切换）`);

  function apply(next: ThemeMode): void {
    mode = next;
    if (next === "auto") document.documentElement.removeAttribute("data-theme");
    else document.documentElement.dataset.theme = next;
    try {
      localStorage.setItem(THEME_KEY, next);
    } catch {
      /* a locked-down browser may refuse storage */
    }
  }

  function cycle(): void {
    const order: ThemeMode[] = ["auto", "light", "dark"];
    const next = order[(order.indexOf(mode) + 1) % order.length] ?? "auto";
    apply(next);
    toast(`外观已切换为${next === "auto" ? "跟随系统" : next === "light" ? "浅色" : "深色"}`);
  }

  try {
    const saved = localStorage.getItem(THEME_KEY);
    if (saved === "light" || saved === "dark" || saved === "auto") mode = saved;
  } catch {
    /* ignore */
  }
  if (mode !== "auto") document.documentElement.dataset.theme = mode;
</script>

<header class="titlebar">
  <svg class="mark" viewBox="0 0 20 20" fill="none" aria-hidden="true">
    <rect x="2.2" y="3.6" width="15.6" height="10.2" rx="1.6" stroke="currentColor" stroke-width="1.4" />
    <path
      d="M7 10h6M9 7.8 6.8 10 9 12.2M11 7.8 13.2 10 11 12.2"
      stroke="currentColor"
      stroke-width="1.3"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
    <path d="M10 13.8v1.8M7.4 16.2h5.2" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
  </svg>
  <span class="brand__name">tcp2rdp</span>
  <span class="brand__sub">工作台</span>
  <span class="titlebar__spacer"></span>
  <div class="titlebar__tools">
    <button class="icon-btn" type="button" title={title} aria-label={title} onclick={cycle}>
      <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">{@html icon}</svg>
    </button>
  </div>
</header>
