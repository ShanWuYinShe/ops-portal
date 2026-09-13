<script>
  import { api } from '../api.js';
  import { shortTime, badgeOf, startPolling } from '../ui.js';

  export let whoami = null;

  let entries = [];
  let filter = '';

  async function load() {
    try {
      const r = await api('/api/portal/audit');
      entries = r.entries ?? [];
    } catch { /* 静默重试 */ }
  }

  $: filtered = entries.filter((e) => {
    if (!filter) return true;
    const q = filter.toLowerCase();
    return [e.actor, e.action, e.object, e.note, e.result].some((v) => (v || '').toLowerCase().includes(q));
  });

  startPolling(load, 15000);
</script>

<div class="page-head">
  <div>
    <h1 style="font-size:19px">审计日志</h1>
    <div class="sub">谁 · 用哪张证书 · 对什么 · 做了什么 · 结果如何（最近 200 条）</div>
  </div>
  <input placeholder="搜索过滤…" bind:value={filter} style="width:220px" />
</div>

<div class="card">
  <table class="table">
    <thead><tr><th>时间</th><th>操作人</th><th>证书</th><th>动作</th><th>对象</th><th>结果</th><th>备注</th></tr></thead>
    <tbody>
      {#each filtered as e, i (e.at + e.action + i)}
        <tr>
          <td class="mono text-2">{shortTime(e.at)}</td>
          <td><b>{e.actor}</b></td>
          <td class="mono text-2">{e.cert ? e.cert.slice(0, 10) + '…' : '—'}</td>
          <td class="mono">{e.action}</td>
          <td class="text-2 mono" style="max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{e.object}</td>
          <td><span class="badge {badgeOf(e.result === 'ok' ? 'done' : 'failed')}">{e.result}</span></td>
          <td class="text-2">{e.note}</td>
        </tr>
      {:else}
        <tr><td colspan="7"><div class="empty">暂无审计记录</div></td></tr>
      {/each}
    </tbody>
  </table>
</div>
