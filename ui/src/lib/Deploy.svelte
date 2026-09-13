<script>
  import { api } from './api.js';

  export let agents = [];
  export let onChange;

  let error = '';
  let info = '';
  let form = { agent_id: '', project: '', targets: '' };

  async function deploy() {
    error = '';
    info = '';
    const targets = form.targets.split(',').map((s) => s.trim()).filter(Boolean);
    if (!form.agent_id || !form.project || targets.length === 0) {
      error = 'agent、project、受影响服务（逗号分隔）均为必填';
      return;
    }
    if (!confirm(`确认在 ${form.agent_id} 执行 deploy ${form.project} [${targets.join(', ')}]？将重建这些服务。`)) return;
    try {
      const r = await api('/api/portal/commands/enqueue', {
        method: 'POST',
        body: JSON.stringify({ agent_id: form.agent_id, action: 'deploy', project: form.project, targets }),
      });
      info = `已入队 ${r.command.id}，agent 下次心跳取走执行，结果可在命令页查看。`;
      form.targets = '';
      onChange?.();
    } catch (e) {
      error = String(e);
    }
  }
</script>

<section>
  <h2>发布（按变更重建受影响服务）</h2>
  {#if error}<p class="error">{error}</p>{/if}
  {#if info}<p class="info">{info}</p>{/if}
  {#each agents as a}
    <details open>
      <summary>{a.agent_id} · {a.heartbeat?.hostname}</summary>
      <h3>站点版本</h3>
      {#each a.heartbeat?.sites ?? [] as s}
        <p>
          {s.name} · {s.branch}@{s.head?.slice(0, 12)}
          {#if s.dirty}<strong>（有未提交改动）</strong>{/if}
        </p>
      {:else}
        <p>未配置站点（agent 的 AGENT_SITES 为空，按 name:/path 配置后可在此看到版本）</p>
      {/each}
      <h3>容器</h3>
      <p>
        {#each a.heartbeat?.containers ?? [] as c}
          <span class="chip">{c.project}/{c.service || c.name}：{c.state}</span>
        {/each}
      </p>
    </details>
  {:else}
    <p>暂无 agent 上报。</p>
  {/each}
  <h3>发起发布</h3>
  <div class="row">
    <select bind:value={form.agent_id}>
      <option value="">选 agent</option>
      {#each agents as a}<option value={a.agent_id}>{a.agent_id}</option>{/each}
    </select>
    <input placeholder="compose project，如 hotspot" bind:value={form.project} />
    <input placeholder="受影响服务，逗号分隔，如 backend,nginx" bind:value={form.targets} class="wide" />
    <button onclick={deploy}>发布</button>
  </div>
  <p class="hint">
    纪律：只重建受影响服务（up -d --build + 服务名），不动全量；
    agent 侧按 AGENT_COMPOSE_PROJECTS 白名单 + AGENT_DEPLOY_DIRS 路径映射双重校验。
  </p>
</section>

<style>
  .row { display: flex; gap: 0.5rem; margin-bottom: 0.8rem; }
  .wide { flex: 1; }
  .chip { display: inline-block; border: 1px solid #ccc; padding: 0.1rem 0.5rem; margin: 0.1rem; }
  .error { color: #b00; }
  .info { background: #e8f5e9; padding: 0.6rem; }
  .hint { color: #666; font-size: 0.9rem; }
</style>
