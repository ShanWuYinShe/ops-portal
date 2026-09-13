<script>
  import { api } from './api.js';

  let entries = [];
  let error = '';

  export async function load() {
    try {
      const r = await api('/api/portal/audit');
      entries = r.entries ?? [];
    } catch (e) {
      error = String(e);
    }
  }
</script>

<section>
  <h2>审计（最近 200 条）</h2>
  {#if error}<p class="error">{error}</p>{/if}
  <table>
    <thead><tr><th>时间</th><th>操作人</th><th>动作</th><th>对象</th><th>结果</th><th>备注</th></tr></thead>
    <tbody>
      {#each entries as e}
        <tr>
          <td>{e.at}</td><td>{e.actor}</td><td>{e.action}</td>
          <td>{e.object}</td><td>{e.result}</td><td>{e.note}</td>
        </tr>
      {/each}
    </tbody>
  </table>
  <button onclick={load}>刷新审计</button>
</section>

<style>
  table { border-collapse: collapse; width: 100%; }
  th, td { border: 1px solid #ccc; padding: 0.3rem 0.6rem; text-align: left; }
  .error { color: #b00; }
</style>
