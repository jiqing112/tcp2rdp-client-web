<script lang="ts">
  import { app } from "../state.svelte";
  import { makeNodeId, normalizeServerInput, parseHostPort } from "../format";
  import type { Node } from "../types";

  let {
    node,
    onSave,
    onClose,
  }: {
    node: Node | null;
    onSave: (node: Node) => Promise<void>;
    onClose: () => void;
  } = $props();

  const editing = $derived(Boolean(node));

  let name = $state("");
  let server = $state("");
  let key = $state("");
  let keyShown = $state(false);
  let routeMode = $state("global");
  let ruleBasis = $state("list");
  let ruleIpPick = $state("builtin");
  let ruleIpPath = $state("");
  let ruleDomainPick = $state("builtin");
  let ruleDomainPath = $state("");
  let ruleDnsPick = $state("1.1.1.1:53");
  let ruleDnsPath = $state("");
  let error = $state("");
  let saving = $state(false);

  // Seed once per open: the modal is recreated for every edit.
  {
    const n = node;
    name = n?.name ?? "";
    server = n?.server ?? "";
    key = n?.key ?? "";
    routeMode = n?.routeMode ?? "global";
    ruleBasis = n?.ruleBasis === "ip" ? "ip" : "list";
    setPick("ip", n?.ruleIp ?? "builtin", (p, v) => ((ruleIpPick = p), (ruleIpPath = v)));
    setPick("domain", n?.ruleDomain ?? "builtin", (p, v) => ((ruleDomainPick = p), (ruleDomainPath = v)));
    setPick("dns", n?.ruleDns ?? "1.1.1.1:53", (p, v) => ((ruleDnsPick = p), (ruleDnsPath = v)));
  }

  function setPick(kind: "ip" | "domain" | "dns", value: string, assign: (pick: string, path: string) => void): void {
    const known = kind === "dns" ? ["1.1.1.1:53", "local", "off"] : ["builtin", "remote", "off"];
    if (known.includes(value)) assign(value, "");
    else assign("custom", value);
  }

  const direct = $derived(routeMode === "direct");
  const ipOnly = $derived(routeMode === "rule" && ruleBasis === "ip");

  const routeNote = $derived.by(() => {
    if (routeMode === "rule") {
      return ipOnly
        ? "分流：仅按 IP 判断，非国内 IP 一律走代理；判断由客户端完成。"
        : "分流：国内 IP / 域名直连，其余（包括全部国外 IP）走代理；判断由客户端完成。";
    }
    if (routeMode === "direct") return "直连：不建立隧道，客户端直接连接目标；只有 -L / -U 转发时有用。";
    return "全局：所有流量都经隧道转发。";
  });

  const basisNote = $derived(
    ipOnly
      ? "只按 IP 判断：域名先经规则 DNS 解析，解析结果不是国内 IP 就一律走代理（国内域名清单不再参与）。"
      : "国内清单命中的目标直连；其余目标（包括全部国外 IP）经隧道走代理。",
  );

  function readDraft(): { node: Node; error: string } {
    let srv = server.trim();
    const k = key.trim();
    const picks = { ip: ruleIpPick, domain: ruleDomainPick, dns: ruleDnsPick };
    const paths = { ip: ruleIpPath.trim(), domain: ruleDomainPath.trim(), dns: ruleDnsPath.trim() };

    let err = "";
    if (routeMode !== "direct") {
      if (!srv) {
        err = "请填写服务器地址，例如 1.2.3.4:3389";
      } else {
        srv = normalizeServerInput(srv);
        if (!parseHostPort(srv)) err = "服务器地址无效，应为 host:port（端口 1-65535）";
      }
      if (!err && !k) err = "请填写与服务端一致的密钥";
    }
    if (!err && routeMode === "rule") {
      if (ipOnly && picks.ip === "off") {
        err = "只按 IP 判断时，国内 IP 清单不能关闭";
      } else if (!ipOnly && picks.ip === "off" && picks.domain === "off") {
        err = "国内 IP 与域名清单不能同时关闭";
      } else if (picks.ip === "custom" && !paths.ip) {
        err = "请填写国内 IP 清单的本地路径或 URL";
      } else if (!ipOnly && picks.domain === "custom" && !paths.domain) {
        err = "请填写国内域名清单的本地路径或 URL";
      } else if (picks.dns === "custom" && !parseHostPort(paths.dns)) {
        err = "规则 DNS 无效，应形如 1.1.1.1:53";
      }
    }

    const out: Node = {
      id: node?.id || makeNodeId(),
      name: name.trim(),
      server: srv,
      key: k,
      routeMode,
      ruleBasis,
      ruleIp: picks.ip === "custom" ? paths.ip : picks.ip,
      ruleDomain: ipOnly ? "off" : picks.domain === "custom" ? paths.domain : picks.domain,
      ruleDns: picks.dns === "custom" ? paths.dns : picks.dns,
    };
    return { node: out, error: err };
  }

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const { node: draft, error: err } = readDraft();
    if (err) {
      error = err;
      return;
    }
    error = "";
    saving = true;
    try {
      await onSave(draft);
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
  <div class="modal__panel" role="dialog" aria-modal="true" aria-labelledby="node-modal-title">
    <header class="modal__head">
      <div class="modal__titles">
        <span class="modal__title" id="node-modal-title">{editing ? "编辑节点" : "新增节点"}</span>
        <span class="modal__sub">
          {editing ? "修改后保存；如果它正在运行，需要重新连接才生效" : "服务器、密钥与分流规则"}
        </span>
      </div>
      <button class="icon-btn" type="button" aria-label="关闭" title="关闭" onclick={onClose}>
        <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
          <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
        </svg>
      </button>
    </header>

    <form class="modal__form" autocomplete="off" novalidate onsubmit={submit}>
      <div class="modal__body">
        <label class="field">
          <span class="field__label">名称</span>
          <span class="field__control">
            <input
              class="input"
              bind:value={name}
              placeholder="例如：香港 VPS（留空自动命名）"
              spellcheck="false"
            />
          </span>
        </label>

        <label class="field" data-off={String(direct)} id="node-server-field">
          <span class="field__label">服务器<span class="field__flag">-s</span></span>
          <span class="field__control">
            <input
              class="input"
              bind:value={server}
              disabled={direct}
              placeholder="1.2.3.4:3389 或 your.domain"
              spellcheck="false"
            />
          </span>
        </label>

        <label class="field" data-off={String(direct)} id="node-key-field">
          <span class="field__label">密钥<span class="field__flag">-key</span></span>
          <span class="field__control">
            <input
              class="input"
              type={keyShown ? "text" : "password"}
              bind:value={key}
              disabled={direct}
              placeholder="与服务端 -key 一致"
              spellcheck="false"
            />
            <button
              class="btn btn--ghost btn--sm"
              type="button"
              disabled={direct}
              onclick={() => (keyShown = !keyShown)}
            >
              {keyShown ? "隐藏" : "显示"}
            </button>
          </span>
        </label>

        <div class="field field--top">
          <span class="field__label">分流<span class="field__flag">-mode</span></span>
          <span class="field__control">
            <div class="seg" role="radiogroup" aria-label="分流模式">
              <label class="seg__item">
                <input type="radio" name="node-route" value="global" bind:group={routeMode} onchange={() => {}} />
                <span>全局</span>
              </label>
              <label class="seg__item">
                <input type="radio" name="node-route" value="rule" bind:group={routeMode} onchange={() => {}} />
                <span>分流</span>
              </label>
              <label class="seg__item">
                <input type="radio" name="node-route" value="direct" bind:group={routeMode} onchange={() => {}} />
                <span>直连</span>
              </label>
            </div>
          </span>
        </div>

        <p class="modal__note">{routeNote}</p>

        {#if routeMode === "rule"}
          <div class="rule-box">
            <div class="rule-box__head">
              <span class="rule-box__title">分流规则</span>
              <span class="rule-box__hint">国外 IP 全部走代理</span>
            </div>

            <label class="field">
              <span class="field__label">判断依据</span>
              <span class="field__control">
                <select class="input" bind:value={ruleBasis}>
                  <option value="list">国内 IP + 域名清单 · 国内直连（推荐）</option>
                  <option value="ip">仅按 IP · 非国内 IP 全部走代理</option>
                </select>
              </span>
            </label>
            <p class="modal__note">{basisNote}</p>

            <label class="field">
              <span class="field__label">国内 IP</span>
              <span class="field__control">
                <select class="input" bind:value={ruleIpPick}>
                  <option value="builtin">内置清单 · 随应用发布</option>
                  <option value="remote">在线清单 · 客户端自动下载</option>
                  <option value="off">关闭这一层</option>
                  <option value="custom">自定义文件或 URL…</option>
                </select>
              </span>
            </label>
            {#if ruleIpPick === "custom"}
              <label class="field">
                <span class="field__label">IP 清单</span>
                <span class="field__control">
                  <input class="input" bind:value={ruleIpPath} placeholder="C:\path\cn-ip.txt 或 https://…" spellcheck="false" />
                </span>
              </label>
            {/if}

            {#if !ipOnly}
              <label class="field">
                <span class="field__label">国内域名</span>
                <span class="field__control">
                  <select class="input" bind:value={ruleDomainPick}>
                    <option value="builtin">内置清单 · 随应用发布</option>
                    <option value="remote">在线清单 · 客户端自动下载</option>
                    <option value="off">关闭这一层</option>
                    <option value="custom">自定义文件或 URL…</option>
                  </select>
                </span>
              </label>
              {#if ruleDomainPick === "custom"}
                <label class="field">
                  <span class="field__label">域名清单</span>
                  <span class="field__control">
                    <input
                      class="input"
                      bind:value={ruleDomainPath}
                      placeholder="C:\path\cn-domain.txt 或 https://…"
                      spellcheck="false"
                    />
                  </span>
                </label>
              {/if}
            {/if}

            <label class="field">
              <span class="field__label">规则 DNS</span>
              <span class="field__control">
                <select class="input" bind:value={ruleDnsPick}>
                  <option value="1.1.1.1:53">1.1.1.1:53 · 经隧道查询</option>
                  <option value="local">local · 服务端本机 DNS</option>
                  <option value="off">off · 不解析域名</option>
                  <option value="custom">自定义 DNS…</option>
                </select>
              </span>
            </label>
            {#if ruleDnsPick === "custom"}
              <label class="field">
                <span class="field__label">DNS 地址</span>
                <span class="field__control">
                  <input class="input" bind:value={ruleDnsPath} placeholder="8.8.8.8:53" spellcheck="false" />
                </span>
              </label>
            {/if}

            <p class="rule-box__note">
              内置清单包含约 1 万条国内 IP、11 万条国内域名；选「在线清单」时会拉取最新清单并缓存，拉取失败会尝试镜像，仍失败则回退到内置清单。
            </p>
          </div>
        {/if}

        {#if error}
          <p class="modal__error">{error}</p>
        {/if}
      </div>

      <footer class="modal__foot">
        <span class="modal__foot-hint">保存后可在节点表里点击切换</span>
        <button class="btn btn--ghost" type="button" onclick={onClose}>取消</button>
        <button class="btn btn--primary" type="submit" disabled={saving}>
          {saving ? "保存中…" : "保存节点"}
        </button>
      </footer>
    </form>
  </div>
</div>
