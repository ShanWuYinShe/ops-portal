<script>
  import { api } from './api.js';

  export let agents = [];
  export let onChange;

  let commands = [];
  let error = '';
  let form = { agent_id: '', action: 'restart', service: '' };

  export async function load() {
    try {
      const r = await api('/api/portal/commands');
      commands = r.commands ?? [];
    } catch (e) {
      error = String(e);
    }
  }

  async function enqueue() {
    error = '';
    if (!form.agent_id || !form.service) {
      error = 'agent 与服务名必填';
      return;
    }
    if (!confirm(`确认对 ${form.agent_id} 执行 ${form.action} ${form.service}？`)) return;
    try {
      await api('/api/portal/commands/enqueue', {
        method: 'POST',
        body: JSON.stringify(form),
      });
      form.service = '';
      await load();
      onChange?.();
    } catch (e) {
      error = String(e);
    }
  }
</script>

<section>
  <h2>命令下发（启停重启）</h2>
  {#if error}<p class="error">{error}</p>{/if}
  <div class="row">
    <select bind:value={form.agent_id}>
      <option value="">选 agent</option>
      {#each agents as a}<option value={a.agent_id}>{a.agent_id}</option>{/each}
    </select>
    <select bind:value={form.action}>
      <option value="restart">restart</option>
      <option value="start">start</option>
      <option value="stop">stop</option>
    </select>
    <input placeholder="compose service 名，如 portal-nginx" bind:value={form.service} />
    <button onclick={enqueue}>入队</button>
  </div>
  <table>
    <thead><tr><th>ID</th><th>Agent</th><th>动作</th><th>状态</th><th>发起人</th><th>结果</th></tr></thead>
    <tbody>
      {#each commands as c}
        <tr>
          <td>{c.id}</td><td>{c.agent_id}</td>
          <td>{c.action} {c.service}</td><td>{c.status}</td>
          <td>{c.created_by}</td><td>{c.result}</td>
        </tr>
      {/each}
    </tbody>
  </table>
  <button onclick={load}>刷新命令</button>
</section>

<style>
  .row { display: flex; gap: 0.5rem; margin-bottom: 0.8rem; }
  table { border-collapse: collapse; width: 100%; }
  th, td { border: 1px solid #ccc; padding: 0.3rem 0.6rem; text-align: left; }
  .error { color: #b00; }
</style>
