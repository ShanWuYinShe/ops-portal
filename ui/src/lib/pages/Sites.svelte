<script>
  import { api } from '../api.js';
  import { askConfirm, toast, agentOnline, relTime, badgeOf, roleLevel } from '../ui.js';

  export let agents = [];
  export let whoami = null;
  export let onChanged = () => {};

  const canOperate = () => roleLevel(whoami?.role) >= 2;

  async function act(agentId, service, action) {
    const verb = { start: '启动', stop: '停止', restart: '重启' }[action];
    if (!(await askConfirm(`${verb}服务`, `确定在 ${agentId} 上${verb} ${service} 吗？`, action !== 'start'))) return;
    try {
      await api('/api/portal/commands/enqueue', {
        method: 'POST',
        body: JSON.stringify({ agent_id: agentId, action, service }),
      });
      toast(`已下发：${verb} ${service}，agent 心跳时执行`);
      onChanged();
    } catch (e) {
      toast('下发失败：' + e.message, 'err');
    }
  }

  startPolling(() => {}, 15000);
</script>

<div class="page-head">
  <div>
    <h1 style="font-size:19px">站点与服务</h1>
    <div class="sub">按主机分组的容器状态与快捷操作 · 每 15 秒自动刷新</div>
  </div>
</div>

{#each agents as a (a.agent_id)}
  <div class="card sect" style="margin-top:14px">
    <div class="card-head">
      <div style="display:flex;align-items:center;gap:10px">
        <h2 class="mono">{a.agent_id}</h2>
        <span class="badge {agentOnline(a.last_seen) ? 'ok' : 'err'}">{agentOnline(a.last_seen) ? '在线' : '离线'}</span>
        <span class="hint">{a.heartbeat?.hostname} · {relTime(a.last_seen)}</span>
      </div>
      <div>
        {#each a.heartbeat?.compose_projects ?? [] as p}<span class="chip">{p}</span>{/each}
      </div>
    </div>

    {#if (a.heartbeat?.sites ?? []).length}
      <div class="card-body" style="padding:10px 16px;border-bottom:1px solid var(--border);display:flex;gap:18px;flex-wrap:wrap">
        {#each a.heartbeat.sites as s (s.name)}
          <span class="text-2">
            <span class="badge {s.dirty ? 'warn' : 'ok'}" style="margin-right:6px">{s.name}</span>
            <span class="mono" style="font-size:12px">{s.branch}@{s.head?.slice(0, 10)}</span>
            {#if s.dirty}<span class="hint">（脏）</span>{/if}
          </span>
        {/each}
      </div>
    {/if}

    <table class="table">
      <thead><tr><th>服务</th><th>项目</th><th>状态</th><th>详情</th><th>镜像</th><th></th></tr></thead>
      <tbody>
        {#each [...(a.heartbeat?.containers ?? [])].sort((x, y) => (x.project + x.service).localeCompare(y.project + y.service)) as c (c.id)}
          <tr>
            <td><b>{c.service || c.name}</b></td>
            <td class="text-2">{c.project}</td>
            <td><span class="badge {badgeOf(c.state)}">{c.state}</span></td>
            <td class="text-2">{c.status}</td>
            <td class="mono text-2">{c.image}</td>
            <td class="td-actions">
              {#if canOperate() && agentOnline(a.last_seen)}
                <button class="btn sm" onclick={() => act(a.agent_id, c.service, 'restart')}>重启</button>
                <button class="btn sm" onclick={() => act(a.agent_id, c.service, 'stop')}>停止</button>
              {/if}
            </td>
          </tr>
        {:else}
          <tr><td colspan="6"><div class="empty">该 agent 未上报容器</div></td></tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else}
  <div class="card"><div class="card-body"><div class="empty">暂无 agent 接入，按 docs/agent-protocol.md 接入第一个站点。</div></div></div>
{/each}
