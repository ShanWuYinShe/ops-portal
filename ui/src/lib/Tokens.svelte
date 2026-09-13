<script>
  import { api } from './api.js';

  let tokens = [];
  let error = '';
  let lastPlain = '';
  let agentID = '';

  export async function load() {
    try {
      const r = await api('/api/portal/agent-tokens');
      tokens = r.tokens ?? [];
    } catch (e) {
      error = String(e);
    }
  }

  async function issue() {
    error = '';
    lastPlain = '';
    if (!agentID) {
      error = 'agent_id 必填';
      return;
    }
    try {
      const r = await api('/api/portal/agent-tokens/issue', {
        method: 'POST',
        body: JSON.stringify({ agent_id: agentID }),
      });
      lastPlain = r.token;
      agentID = '';
      await load();
    } catch (e) {
      error = String(e);
    }
  }

  async function revoke(id) {
    if (!confirm(`吊销 token ${id}？该 agent 下次心跳即断连。`)) return;
    try {
      await api('/api/portal/agent-tokens/revoke', { method: 'POST', body: JSON.stringify({ id }) });
      await load();
    } catch (e) {
      error = String(e);
    }
  }
</script>

<section>
  <h2>Agent Token 签发（一次性显示）</h2>
  {#if error}<p class="error">{error}</p>{/if}
  {#if lastPlain}
    <p class="plain">新 token（只显示一次，立即复制到被管机 env）：<code>{lastPlain}</code></p>
  {/if}
  <div class="row">
    <input placeholder="agent_id，如 agent-workwork-01" bind:value={agentID} />
    <button onclick={issue}>签发</button>
  </div>
  <table>
    <thead><tr><th>ID</th><th>Agent</th><th>签发人</th><th>时间</th><th>已吊销</th><th>操作</th></tr></thead>
    <tbody>
      {#each tokens as t}
        <tr>
          <td>{t.id}</td><td>{t.agent_id}</td><td>{t.created_by}</td>
          <td>{t.created_at}</td><td>{t.revoked ? '是' : '否'}</td>
          <td>{#if !t.revoked}<button onclick={() => revoke(t.id)}>吊销</button>{/if}</td>
        </tr>
      {/each}
    </tbody>
  </table>
  <button onclick={load}>刷新 Token</button>
</section>

<style>
  .row { display: flex; gap: 0.5rem; margin-bottom: 0.8rem; }
  table { border-collapse: collapse; width: 100%; }
  th, td { border: 1px solid #ccc; padding: 0.3rem 0.6rem; text-align: left; }
  .error { color: #b00; }
  .plain { background: #fff8e1; padding: 0.6rem; }
  code { word-break: break-all; }
</style>
