<script lang="ts">
  import { onMount } from "svelte";
  import { api, openEvents } from "./lib/api";
  import {
    applyStatus,
    boot,
    changeNodeRoute,
    clearLogs,
    deleteNode,
    probeCurrent,
    probeNode,
    saveLatency,
    saveNode,
    scheduleSave,
    setSystemProxy,
    switchNode,
    toggleRun,
  } from "./lib/actions";
  import { app, appendLogs, toast } from "./lib/state.svelte";
  import { isLiveState } from "./lib/format";
  import Titlebar from "./lib/components/Titlebar.svelte";
  import Hero from "./lib/components/Hero.svelte";
  import Banner from "./lib/components/Banner.svelte";
  import NodesCard from "./lib/components/NodesCard.svelte";
  import ConfigForm from "./lib/components/ConfigForm.svelte";
  import Diagnose from "./lib/components/Diagnose.svelte";
  import ProbeCard from "./lib/components/ProbeCard.svelte";
  import OutboundCard from "./lib/components/OutboundCard.svelte";
  import WebCard from "./lib/components/WebCard.svelte";
  import LogCard from "./lib/components/LogCard.svelte";
  import FooterBar from "./lib/components/FooterBar.svelte";
  import NodeModal from "./lib/components/NodeModal.svelte";
  import LatencyModal from "./lib/components/LatencyModal.svelte";
  import Toasts from "./lib/components/Toasts.svelte";

  let scrolled = $state(false);

  const stuck = $derived.by(() => {
    const st = app.status;
    if (!st) return false;
    return st.state === "running" && st.latencyMode === "tunnel" && st.sampling && (st.probeAge ?? 0) >= 15;
  });

  const configLocked = $derived(isLiveState(app.status?.state));
  const diagVisible = $derived(app.diagOpen && stuck);

  onMount(() => {
    void boot();
    const close = openEvents(applyStatus, (batch) => {
      appendLogs(batch.lines ?? [], Boolean(batch.reset));
    });
    // A tab that slept through the server going away gets one fresh snapshot
    // when it wakes; the SSE stream itself reconnects on its own.
    const resume = window.setInterval(() => {
      void api.status().then(applyStatus).catch(() => undefined);
    }, 30000);
    return () => {
      window.clearInterval(resume);
      close();
    };
  });

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      if (app.latencyModal) {
        app.latencyModal = false;
        return;
      }
      if (app.nodeModal) {
        app.nodeModal = false;
        return;
      }
      if (app.banner) app.banner = null;
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
      e.preventDefault();
      void toggleRun();
    }
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="app" data-state={app.status?.state ?? "idle"} data-scrolled={scrolled}>
  <Titlebar />

  <main class="content">
    <div
      class="pane"
      onscroll={(e) => {
        scrolled = (e.currentTarget as HTMLElement).scrollTop > 2;
      }}
    >
      <Hero
        onOpenLatency={() => (app.latencyModal = true)}
        onHelp={(text: string) => toast(text, "info", 9000)}
        onToggleDiagnose={() => {
          if (stuck) app.diagOpen = !app.diagOpen;
        }}
      />
      <Banner />
      <NodesCard
        onSwitch={switchNode}
        onRoute={changeNodeRoute}
        onProbe={probeNode}
        onEdit={(n) => {
          app.editingNode = n;
          app.nodeModal = "edit";
        }}
        onDelete={deleteNode}
        onAdd={() => {
          app.editingNode = null;
          app.nodeModal = "new";
        }}
      />
      <ConfigForm locked={configLocked} onChange={scheduleSave} />
      <Diagnose open={diagVisible} onClose={() => (app.diagOpen = false)} />
      <ProbeCard />
    </div>

    <aside class="pane pane--aside">
      <WebCard />
      <OutboundCard onToggle={setSystemProxy} />
      <LogCard onClear={clearLogs} />
    </aside>
  </main>

  <FooterBar onStart={toggleRun} onProbe={probeCurrent} />

  {#if app.nodeModal}
    <NodeModal
      node={app.editingNode}
      onSave={saveNode}
      onClose={() => {
        app.nodeModal = false;
        app.editingNode = null;
      }}
    />
  {/if}
  {#if app.latencyModal}
    <LatencyModal onSave={saveLatency} onClose={() => (app.latencyModal = false)} />
  {/if}

  <Toasts />
</div>

<style>
  :global(html, body) {
    height: 100%;
    margin: 0;
  }
  :global(body) {
    overflow: hidden;
  }
</style>
