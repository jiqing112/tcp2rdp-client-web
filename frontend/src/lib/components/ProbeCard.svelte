<script lang="ts">
  import { app, toast } from "../state.svelte";
  import { scheduleSave } from "../actions";
  import { isLiveState } from "../format";

  const r = $derived(app.lastProbe);
  const live = $derived(isLiveState(app.status?.state));

  function pinFingerprint(): void {
    if (!r?.fingerprint) return;
    // Fill -pin and persist; the advanced group opens so the value is visible.
    app.settings = { ...app.settings, pin: r.fingerprint };
    scheduleSave(app.settings);
    toast(
      live ? "指纹已保存，下次启动生效" : "已填入 -pin：以后指纹变了会直接拒绝连接",
      "ok",
    );
    const adv = document.getElementById("group-advanced");
    if (adv) {
      adv.dataset.open = "true";
      adv.querySelector(".group__head")?.setAttribute("aria-expanded", "true");
    }
  }

  const rows = $derived(
    r
      ? ([
          ["地址", `${r.ip}:${r.port}`],
          ["TCP 连接", `${r.tcpMs} ms`],
          ["RDP 协商", r.rdp ? `${r.negotiateMs} ms` : "不像 RDP 服务端"],
          ["TLS 握手", r.tlsMs > 0 ? `${r.tlsMs} ms · ${r.tlsVersion}` : "—"],
          ["证书 CN", r.cn || "—"],
          ["证书指纹", r.fingerprint || "—"],
        ] as [string, string][])
      : [],
  );
</script>

{#if r}
  <section class="card summary" aria-label="服务器探测结果">
    <div class="summary__head">
      <span class="summary__title">服务器探测</span>
      <span class="summary__note">只握手，不认证</span>
    </div>
    <dl class="summary__list">
      {#each rows as [k, v] (k)}
        <div>
          <dt>{k}</dt>
          <dd title={v}>{v}</dd>
        </div>
      {/each}
    </dl>
    <div class="summary__tags">
      <span class="tag">{r.cipher || "无 TLS"}</span>
      {#if r.fingerprint}
        <button class="btn btn--ghost btn--sm" type="button" onclick={pinFingerprint}>
          用这个指纹固定服务端
        </button>
      {/if}
    </div>
  </section>
{/if}
