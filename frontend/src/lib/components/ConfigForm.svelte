<script lang="ts">
  import { app } from "../state.svelte";
  import type { Settings } from "../types";

  // The form owns its draft: the user types without the server's normalized
  // values jumping under the caret. Changes bubble up debounced; the draft is
  // reset from the store only when the session state changes the lock.

  let { locked, onChange }: { locked: boolean; onChange: (draft: Settings) => void } = $props();

  let draft = $state<Settings>($state.snapshot(app.settings) as Settings);
  let initialized = false;

  $effect(() => {
    // Adopt the server's copy once at boot (and never again while typing):
    // the boot response arrives after this component mounts.
    if (!initialized && app.booted) {
      draft = $state.snapshot(app.settings) as Settings;
      initialized = true;
    }
  });

  function emit(): void {
    onChange($state.snapshot(draft) as Settings);
  }

  const socks5 = $derived(draft.socks5);
  const padIsRand = $derived(draft.pad === "rand");
  const lanWarning = $derived(draft.listenHost === "0.0.0.0");

  // Fields that only make sense in one mode grey out instead of vanishing.
  let groupForwardsOpen = $state(false);
  let groupDisguiseOpen = $state(false);
  let groupAdvOpen = $state(false);

  $effect(() => {
    // Something configured in the forwards box: do not leave it collapsed.
    if ((draft.forwards.trim() || draft.udpForwards.trim()) && !groupForwardsOpen) {
      groupForwardsOpen = true;
    }
  });

  const binaryTitle = $derived(
    draft.binaryPath || "自动查找：程序目录 → bin 目录 → resources 目录 → PATH",
  );
</script>

<form class="config" autocomplete="off" novalidate onsubmit={(e) => e.preventDefault()}>
  <section class="card params" aria-label="通用参数">
    <header class="params__head">
      <span class="params__title">通用参数</span>
      <span class="params__note">{locked ? "运行中只读 · 停止隧道后可修改" : "所有节点共用 · 运行中只读"}</span>
    </header>
    <div class="params__body">
      <div class="fields-row">
        <label class="field">
          <span class="field__label">代理模式<span class="field__flag">-socks</span></span>
          <span class="field__control">
            <select
              class="input"
              id="proxyMode"
              name="proxyMode"
              value={socks5 ? "socks5" : "forward"}
              disabled={locked}
              onchange={(e) => {
                draft.socks5 = (e.currentTarget as HTMLSelectElement).value === "socks5";
                emit();
              }}
            >
              <option value="socks5">SOCKS5 本地代理</option>
              <option value="forward">端口转发</option>
            </select>
          </span>
        </label>
        <label class="field">
          <span class="field__label">本地端口<span class="field__flag">-l</span></span>
          <span class="field__control">
            <select
              class="prefix prefix--select"
              bind:value={draft.listenHost}
              disabled={locked}
              aria-label="监听范围"
              title="仅本机：只有这台电脑能使用；局域网：同一网络内的设备都能连接"
              onchange={emit}
            >
              <option value="127.0.0.1">仅本机</option>
              <option value="0.0.0.0">局域网</option>
            </select>
            <input
              class="input"
              bind:value={draft.listenPort}
              oninput={emit}
              disabled={locked}
              inputmode="numeric"
              placeholder="1080"
              spellcheck="false"
            />
          </span>
        </label>
      </div>
      <div class="fields-row">
        <label class="field" data-off={String(!socks5)}>
          <span class="field__label">HTTP 代理<span class="field__flag">本地</span></span>
          <span class="field__control">
            <span class="prefix">127.0.0.1:</span>
            <input
              class="input"
              bind:value={draft.httpProxyPort}
              oninput={emit}
              disabled={locked || !socks5}
              inputmode="numeric"
              placeholder={socks5 ? "34457" : "socks5 模式下才用"}
              spellcheck="false"
            />
          </span>
        </label>
        <label class="field">
          <span class="field__label">超时<span class="field__flag">-timeout</span></span>
          <span class="field__control">
            <input
              class="input"
              bind:value={draft.timeout}
              oninput={emit}
              disabled={locked}
              inputmode="numeric"
              placeholder="10"
              spellcheck="false"
            />
            <span class="suffix">秒</span>
          </span>
        </label>
      </div>
      <label class="field" data-off={String(socks5)}>
        <span class="field__label">转发目标<span class="field__flag">-t</span></span>
        <span class="field__control">
          <input
            class="input"
            bind:value={draft.target}
            oninput={emit}
            disabled={locked || socks5}
            placeholder={socks5 ? "socks5 模式下不需要" : "127.0.0.1:3389"}
            spellcheck="false"
          />
        </span>
        <span class="field__note">
          {socks5 ? "SOCKS5 模式下由客户端按请求转发" : "所有请求转发到这一个目标地址"}
        </span>
      </label>
      {#if lanWarning}
        <p class="params__warn">
          局域网内任何设备都能使用这个代理，且无需认证；建议用防火墙限制来源 IP。
          启动时 Windows 会自动放行该端口（可能弹出一次管理员授权，取消则局域网设备无法连入）。
        </p>
      {/if}
    </div>
  </section>

  <section class="card form" aria-label="高级设置">
    <section class="group" data-open={String(groupForwardsOpen)}>
      <button
        class="group__head"
        type="button"
        aria-expanded={groupForwardsOpen}
        onclick={() => (groupForwardsOpen = !groupForwardsOpen)}
      >
        <span class="group__title">端口转发</span>
        <span class="group__hint">-L / -U，每行一条</span>
        <svg class="group__chevron" viewBox="0 0 16 16" fill="none" aria-hidden="true">
          <path
            d="M4 6.2 8 10l4-3.8"
            stroke="currentColor"
            stroke-width="1.4"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
        </svg>
      </button>
      <div class="group__body">
        <label class="field field--top">
          <span class="field__label">TCP 转发<span class="field__flag">-L</span></span>
          <span class="field__control">
            <textarea
              class="input textarea"
              rows="3"
              bind:value={draft.forwards}
              oninput={emit}
              disabled={locked}
              spellcheck="false"
              placeholder={"8080:127.0.0.1:80\n13389:10.0.0.5:3389"}
            ></textarea>
          </span>
        </label>
        <label class="field field--top">
          <span class="field__label">UDP 转发<span class="field__flag">-U</span></span>
          <span class="field__control">
            <textarea
              class="input textarea"
              rows="2"
              bind:value={draft.udpForwards}
              oninput={emit}
              disabled={locked}
              spellcheck="false"
              placeholder="5353:8.8.8.8:53"
            ></textarea>
          </span>
        </label>
      </div>
    </section>

    <section class="group" data-open={String(groupDisguiseOpen)}>
      <button
        class="group__head"
        type="button"
        aria-expanded={groupDisguiseOpen}
        onclick={() => (groupDisguiseOpen = !groupDisguiseOpen)}
      >
        <span class="group__title">伪装与调优</span>
        <span class="group__hint">TLS 指纹、填充与发送合并</span>
        <svg class="group__chevron" viewBox="0 0 16 16" fill="none" aria-hidden="true">
          <path
            d="M4 6.2 8 10l4-3.8"
            stroke="currentColor"
            stroke-width="1.4"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
        </svg>
      </button>
      <div class="group__body">
        <div class="fields-row">
          <label class="field">
            <span class="field__label">TLS 指纹<span class="field__flag">-tls-fp</span></span>
            <span class="field__control">
              <select class="input" bind:value={draft.tlsFp} onchange={emit} disabled={locked}>
                <option value="windows">windows · SChannel</option>
                <option value="go">go · Go 原生</option>
                <option value="chrome">chrome</option>
                <option value="firefox">firefox</option>
                <option value="edge">edge</option>
              </select>
            </span>
          </label>
          <label class="field">
            <span class="field__label">填充<span class="field__flag">-pad</span></span>
            <span class="field__control">
              <select class="input" bind:value={draft.pad} onchange={emit} disabled={locked}>
                <option value="block">block · 补齐 64B</option>
                <option value="full">full · 定长 1500</option>
                <option value="rand">rand · 随机填充</option>
                <option value="off">off · 不填充</option>
              </select>
            </span>
          </label>
        </div>
        <div class="fields-row">
          <label class="field" data-off={String(!padIsRand)}>
            <span class="field__label">随机上限<span class="field__flag">-pad-max</span></span>
            <span class="field__control">
              <input
                class="input"
                bind:value={draft.padMax}
                oninput={emit}
                disabled={locked || !padIsRand}
                inputmode="numeric"
                placeholder={padIsRand ? "512" : "只在 rand 模式用"}
                spellcheck="false"
              />
              <span class="suffix">字节</span>
            </span>
          </label>
          <label class="field">
            <span class="field__label">发送合并<span class="field__flag">-tick</span></span>
            <span class="field__control">
              <input
                class="input"
                bind:value={draft.tick}
                oninput={emit}
                disabled={locked}
                inputmode="numeric"
                placeholder="0 = 关闭"
                spellcheck="false"
              />
              <span class="suffix">毫秒</span>
            </span>
          </label>
        </div>
        <label class="field">
          <span class="field__label">证书指纹<span class="field__flag">-pin</span></span>
          <span class="field__control">
            <input
              class="input"
              bind:value={draft.pin}
              oninput={emit}
              disabled={locked}
              placeholder="留空则首次记录（TOFU）"
              spellcheck="false"
            />
          </span>
        </label>
        <div class="field">
          <span class="field__label">跳过校验<span class="field__flag">-insecure</span></span>
          <span class="field__control">
            <label class="switch">
              <input type="checkbox" bind:checked={draft.insecure} onchange={emit} disabled={locked} />
              <span class="switch__track"></span>
              <span class="switch__text">不校验服务端证书（仅调试）</span>
            </label>
          </span>
        </div>
      </div>
    </section>

    <section class="group" data-open={String(groupAdvOpen)} id="group-advanced">
      <button
        class="group__head"
        type="button"
        aria-expanded={groupAdvOpen}
        onclick={() => (groupAdvOpen = !groupAdvOpen)}
      >
        <span class="group__title">日志与高级</span>
        <span class="group__hint">客户端路径与日志</span>
        <svg class="group__chevron" viewBox="0 0 16 16" fill="none" aria-hidden="true">
          <path
            d="M4 6.2 8 10l4-3.8"
            stroke="currentColor"
            stroke-width="1.4"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
        </svg>
      </button>
      <div class="group__body">
        <div class="field">
          <span class="field__label">客户端<span class="field__flag">exe</span></span>
          <span class="field__control">
            <input
              class="input"
              bind:value={draft.binaryPath}
              oninput={emit}
              disabled={locked}
              placeholder={app.info?.clientPath || "自动查找 rdp2tcp"}
              title={binaryTitle}
              spellcheck="false"
            />
          </span>
        </div>
        <div class="field">
          <span class="field__label">数据文件<span class="field__flag">sqlite</span></span>
          <span class="field__control">
            <input class="input" value={app.info?.dbPath ?? ""} readonly spellcheck="false" title={app.info?.dbPath ?? ""} />
          </span>
        </div>
        <div class="field">
          <span class="field__label">关闭日志<span class="field__flag">-nolog</span></span>
          <span class="field__control">
            <label class="switch">
              <input type="checkbox" bind:checked={draft.noLog} onchange={emit} disabled={locked} />
              <span class="switch__track"></span>
              <span class="switch__text">不记录本次会话日志</span>
            </label>
          </span>
        </div>
        <div class="fields-row">
          <label class="field">
            <span class="field__label">日志级别<span class="field__flag">本地</span></span>
            <span class="field__control">
              <select class="input" bind:value={draft.logLevel} onchange={emit} disabled={locked}>
                <option value="debug">debug · 含统计</option>
                <option value="info">info</option>
                <option value="warn">warn</option>
                <option value="error">error</option>
              </select>
            </span>
          </label>
        </div>
        <label class="field">
          <span class="field__label">已知主机<span class="field__flag">-known</span></span>
          <span class="field__control">
            <input
              class="input"
              bind:value={draft.knownHosts}
              oninput={emit}
              disabled={locked}
              placeholder="默认 ~/.rdp2tcp_known_hosts"
              spellcheck="false"
            />
          </span>
        </label>
      </div>
    </section>
  </section>
</form>
