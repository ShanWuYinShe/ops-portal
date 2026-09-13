<script>
  import { onMount } from 'svelte';
  import { api } from '../api.js';
  import { askConfirm, toast, shortTime } from '../ui.js';

  export let whoami = null;

  let tokens = [];
  let agentID = '';
  let lastPlain = '';
  let copied = false;

  async function load() {
    try {
      const r = await api('/api/portal/agent-tokens');
      tokens = r.tokens ?? [];
    } catch { /* 静默重试 */ }
  }

  async function issue() {
    if (!agentID) { toast('agent_id 必填', 'err'); return; }
    try {
      const r = await api('/api/portal/agent-tokens/issue', {
        method: 'POST', body: JSON.stringify({ agent_id: agentID }),
      });
      lastPlain = r.token;
      copied = false;
      toast(`已为 ${r.agent_id} 签发 token，仅显示一次`);
      agentID = '';
      await load();
    } catch (e) {
      toast('签发失败：' + e.message, 'err');
    }
  }

  async function revoke(t) {
    if (!(await askConfirm('吊销 Token', `吊销 ${t.agent_id} 的 token（${t.id}）？\n该 agent 下次心跳即断连。`, true))) return;
    try {
      await api('/api/portal/agent-tokens/revoke', { method: 'POST', body: JSON.stringify({ id: t.id }) });
      toast('已吊销');
      await load();
    } catch (e) {
      toast('吊销失败：' + e.message, 'err');
    }
  }

  async function copyPlain() {
    try {
      await navigator.clipboard.writeText(lastPlain);
      copied = true;
    } catch {
      toast('复制失败，请手动选择复制', 'err');
    }
  }

  onMount(load);
</script>

<div class="page-head">
  <div>
    <h1 style="font-size:19px">Agent 令牌</h1>
    <div class="sub">被管机接入凭证 · 一机一枚 · portal 只存摘要</div>
  </div>
</div>

<div class="card">
  <div class="card-head"><h2>签发新令牌</h2></div>
  <div class="card-body">
    <div class="form-row">
      <div class="field"><label>Agent ID</label>
        <input placeholder="如 agent-hotspot-01" bind:value={agentID} onkeydown={(e) => e.key === 'Enter' && issue()} />
      </div>
      <button class="btn primary" onclick={issue}>签发</button>
    </div>
    {#if lastPlain}
      <div class="sect" style="background:var(--warn-soft);border:1px solid #f1d9a1;border-radius:var(--radius-sm);padding:12px 14px">
        <div style="font-weight:600;margin-bottom:5px">请立即复制（关闭后不再显示）：</div>
        <div style="display:flex;gap:9px;align-items:center">
          <code class="mono" style="word-break:break-all;flex:1">{lastPlain}</code>
          <button class="btn sm" onclick={copyPlain}>{copied ? '已复制' : '复制'}</button>
        </div>
      </div>
    {/if}
  </div>
</div>

<div class="card sect">
  <table class="table">
    <thead><tr><th>ID</th><th>Agent</th><th>签发人</th><th>签发时间</th><th>状态</th><th></th></tr></thead>
    <tbody>
      {#each tokens as t (t.id)}
        <tr>
          <td class="mono">{t.id}</td>
          <td><b>{t.agent_id}</b></td>
          <td class="text-2">{t.created_by}</td>
          <td class="mono text-2">{shortTime(t.created_at)}</td>
          <td><span class="badge {t.revoked ? 'neutral' : 'ok'}">{t.revoked ? '已吊销' : '有效'}</span></td>
          <td class="td-actions">
            {#if !t.revoked}
              <button class="btn sm danger" onclick={() => revoke(t)}>吊销</button>
            {/if}
          </td>
        </tr>
      {:else}
        <tr><td colspan="6"><div class="empty">暂无令牌</div></td></tr>
      {/each}
    </tbody>
  </table>
</div>
