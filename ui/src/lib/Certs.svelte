<script>
  import { api } from './api.js';

  let certs = [];
  let error = '';
  let form = { label: '', kind: 'human', cf_id: '', serial: '', fingerprint: '', common_name: '', note: '' };

  export async function load() {
    try {
      const r = await api('/api/portal/certs');
      certs = r.certs ?? [];
    } catch (e) {
      error = String(e);
    }
  }

  async function register() {
    error = '';
    if (!form.label || !form.serial) {
      error = 'label 与 serial 必填（从 CF 面板建证后回填）';
      return;
    }
    try {
      await api('/api/portal/certs/register', { method: 'POST', body: JSON.stringify(form) });
      form = { label: '', kind: 'human', cf_id: '', serial: '', fingerprint: '', common_name: '', note: '' };
      await load();
    } catch (e) {
      error = String(e);
    }
  }

  async function revoke(id, label) {
    if (!confirm(`吊销证书 ${label}？吊销后还需同步到 CF 面板吊销，边缘才真正生效。`)) return;
    try {
      const r = await api('/api/portal/certs/revoke', { method: 'POST', body: JSON.stringify({ id }) });
      if (r.cf_sync_required) alert('本地已吊销，请同步去 CF 面板吊销该证！');
      await load();
    } catch (e) {
      error = String(e);
    }
  }
</script>

<section>
  <h2>证书登记（本地库 + CF 同步）</h2>
  {#if error}<p class="error">{error}</p>{/if}
  <div class="grid">
    <input placeholder="label（持有人-设备，如 liyou-mbpro）" bind:value={form.label} />
    <select bind:value={form.kind}>
      <option value="human">human</option>
      <option value="agent">agent</option>
    </select>
    <input placeholder="CF 证书 id" bind:value={form.cf_id} />
    <input placeholder="serial_number（CF 返回）" bind:value={form.serial} />
    <input placeholder="fingerprint_sha256" bind:value={form.fingerprint} />
    <input placeholder="common_name" bind:value={form.common_name} />
    <input placeholder="备注" bind:value={form.note} />
    <button onclick={register}>登记</button>
  </div>
  <table>
    <thead><tr><th>Label</th><th>类型</th><th>Serial</th><th>状态</th><th>过期</th><th>操作</th></tr></thead>
    <tbody>
      {#each certs as c}
        <tr>
          <td>{c.label}</td><td>{c.kind}</td>
          <td title={c.fingerprint}>{c.serial?.slice(0, 20)}…</td>
          <td>{c.status}</td><td>{c.expires_at}</td>
          <td>{#if c.status === 'active'}<button onclick={() => revoke(c.id, c.label)}>吊销</button>{/if}</td>
        </tr>
      {/each}
    </tbody>
  </table>
  <button onclick={load}>刷新证书</button>
</section>

<style>
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0.4rem; margin-bottom: 0.8rem; }
  table { border-collapse: collapse; width: 100%; }
  th, td { border: 1px solid #ccc; padding: 0.3rem 0.6rem; text-align: left; }
  .error { color: #b00; }
</style>
