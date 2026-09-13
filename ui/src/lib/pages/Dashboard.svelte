<script>
  import { api } from '../api.js';
  import { agentOnline, relTime, startPolling } from '../ui.js';

  export let agents = [];
  export let whoami = null;

  let certs = [];
  let commands = [];
  let loaded = false;

  $: online = agents.filter((a) => agentOnline(a.last_seen));
  $: containers = agents.flatMap((a) => a.heartbeat?.containers ?? []);
  $: running = containers.filter((c) => c.state === 'running');
  $: pending = commands.filter((c) => c.status === 'queued' || c.status === 'acked');
  $: activeCerts = certs.filter((c) => c.status === 'active');

  async function load() {
    try {
      const [c, cmd] = await Promise.all([
        api('/api/portal/certs'),
        api('/api/portal/commands'),
      ]);
      certs = c.certs ?? [];
      commands = cmd.commands ?? [];
      loaded = true;
    } catch { /* 角色不足或网络抖动时静默，主数据由 App 轮询 */ loaded = true; }
  }

  startPolling(load, 10000);
</script>

<div class="page-head">
  <div>
    <h1 style="font-size:19px">仪表盘</h1>
    <div class="sub">全部站点实时概览 · 每 10 秒自动刷新</div>
  </div>
</div>

<div class="stats">
  <div class="card stat"><div class="k">Agent 在线</div><div class="v">{online.length}<span class="muted" style="font-size:14px"> / {agents.length}</span></div><div class="s">被管主机</div></div>
  <div class="card stat"><div class="k">容器运行</div><div class="v">{running.length}<span class="muted" style="font-size:14px"> / {containers.length}</span></div><div class="s">全部 compose 服务</div></div>
  <div class="card stat"><div class="k">待执行命令</div><div class="v">{pending.length}</div><div class="s">队列中的操作</div></div>
  <div class="card stat"><div class="k">有效证书</div><div class="v">{activeCerts.length}</div><div class="s">已登记设备/agent</div></div>
</div>

<div class="card">
  <div class="card-head"><h2>被管主机</h2></div>
  {#if agents.length === 0}
    <div class="card-body"><div class="empty">暂无 agent 接入。按 docs/agent-protocol.md 在被管机起 agent 即可接入。</div></div>
  {:else}
    <table class="table">
      <thead><tr><th>Agent</th><th>主机</th><th>状态</th><th>上次心跳</th><th>容器</th><th>Compose 项目</th></tr></thead>
      <tbody>
        {#each agents as a (a.agent_id)}
          <tr>
            <td class="mono">{a.agent_id}</td>
            <td>{a.heartbeat?.hostname || '—'}</td>
            <td><span class="badge {agentOnline(a.last_seen) ? 'ok' : 'err'}">{agentOnline(a.last_seen) ? '在线' : '离线'}</span></td>
            <td>{relTime(a.last_seen)}</td>
            <td>{(a.heartbeat?.containers ?? []).filter((c) => c.state === 'running').length} 运行 / {(a.heartbeat?.containers ?? []).length} 总数</td>
            <td>{#each a.heartbeat?.compose_projects ?? [] as p}<span class="chip">{p}</span>{/each}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<div class="card sect">
  <div class="card-head"><h2>站点版本</h2><span class="hint">来自各 agent 的 AGENT_SITES 上报</span></div>
  <div class="card-body">
    {#each agents as a (a.agent_id)}
      {#each a.heartbeat?.sites ?? [] as s (a.agent_id + s.name)}
        <div style="display:flex;gap:10px;align-items:center;padding:4px 0">
          <span class="badge {s.dirty ? 'warn' : 'ok'}">{s.name}</span>
          <span class="mono text-2">{s.branch}@{s.head?.slice(0, 10)}</span>
          {#if s.dirty}<span class="hint">有未提交改动</span>{/if}
        </div>
      {:else}
        <div class="empty">暂无站点版本上报</div>
      {/each}
    {/each}
  </div>
</div>
