<script>
  export let agents = [];
</script>

<section>
  <h2>站点 / Agent（{agents.length}）</h2>
  {#each agents as a}
    <details open>
      <summary>
        {a.agent_id} · {a.heartbeat?.hostname} · 上报 {a.last_seen} · 待执行 {a.pending ?? 0}
      </summary>
      <h3>容器</h3>
      <table>
        <thead><tr><th>服务</th><th>项目</th><th>状态</th><th>镜像</th></tr></thead>
        <tbody>
          {#each a.heartbeat?.containers ?? [] as c}
            <tr>
              <td>{c.service || c.name}</td>
              <td>{c.project}</td>
              <td>{c.state}（{c.status}）</td>
              <td>{c.image}</td>
            </tr>
          {/each}
        </tbody>
      </table>
      <h3>站点版本</h3>
      {#each a.heartbeat?.sites ?? [] as s}
        <p>{s.name} · {s.branch}@{s.head?.slice(0, 12)}{#if s.dirty}（脏）{/if}</p>
      {:else}
        <p>未配置站点（agent 的 AGENT_SITES 为空）</p>
      {/each}
    </details>
  {:else}
    <p>暂无 agent 上报。</p>
  {/each}
</section>

<style>
  table { border-collapse: collapse; width: 100%; }
  th, td { border: 1px solid #ccc; padding: 0.3rem 0.6rem; text-align: left; }
</style>
