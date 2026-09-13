<script>
  import { api } from '../api.js';
  import { askConfirm, toast, shortTime, badgeOf, roleLevel, startPolling } from '../ui.js';

  export let agents = [];
  export let whoami = null;
  export let onChanged = () => {};

  let commands = [];
  let form = { agent_id: '', action: 'restart', service: '' };
  let deploy = { agent_id: '', project: '', targets: '' };

  const canOperate = () => roleLevel(whoami?.role) >= 2;

  async function load() {
    try {
      const r = await api('/api/portal/commands');
      commands = r.commands ?? [];
    } catch { /* 静默重试 */ }
  }

  async function enqueue(payload, confirmTitle, confirmMsg, danger) {
    if (!(await askConfirm(confirmTitle, confirmMsg, danger))) return false;
    try {
      await api('/api/portal/commands/enqueue', { method: 'POST', body: JSON.stringify(payload) });
      toast('已下发，agent 心跳时执行（约 10 秒内）');
      await load();
      onChanged();
      return true;
    } catch (e) {
      toast('下发失败：' + e.message, 'err');
      return false;
    }
  }

  async function submitService() {
    if (!form.agent_id || !form.service) { toast('agent 与服务名必填', 'err'); return; }
    const verb = { start: '启动', stop: '停止', restart: '重启' }[form.action];
    const ok = await enqueue(
      { agent_id: form.agent_id, action: form.action, service: form.service.trim() },
      `${verb}服务`, `${form.agent_id} → ${verb} ${form.service}`, form.action !== 'start'
    );
    if (ok) form.service = '';
  }

  async function submitDeploy() {
    const targets = deploy.targets.split(',').map((s) => s.trim()).filter(Boolean);
    if (!deploy.agent_id || !deploy.project || targets.length === 0) {
      toast('agent、project、服务列表均必填', 'err'); return;
    }
    const ok = await enqueue(
      { agent_id: deploy.agent_id, action: 'deploy', project: deploy.project.trim(), targets },
      '发布重建',
      `在 ${deploy.agent_id} 重建 ${deploy.project} 的 [${targets.join(', ')}]。\n只重建受影响服务；构建日志可在命令历史中查看。`,
      true
    );
    if (ok) deploy.targets = '';
  }

  startPolling(load, 8000);
</script>

<div class="page-head">
  <div>
    <h1 style="font-size:19px">命令与发布</h1>
    <div class="sub">容器操作与按变更重建 · 命令历史每 8 秒刷新</div>
  </div>
</div>

{#if canOperate()}
  <div class="stats" style="grid-template-columns:1fr 1fr">
    <div class="card stat">
      <div class="k">容器快捷操作</div>
      <div class="form-row" style="margin-top:9px">
        <div class="field"><label>Agent</label>
          <select bind:value={form.agent_id}>
            <option value="">选择</option>
            {#each agents as a}<option value={a.agent_id}>{a.agent_id}</option>{/each}
          </select>
        </div>
        <div class="field" style="flex:0;min-width:100px"><label>动作</label>
          <select bind:value={form.action}>
            <option value="restart">重启</option>
            <option value="start">启动</option>
            <option value="stop">停止</option>
          </select>
        </div>
        <div class="field"><label>Compose 服务名</label>
          <input placeholder="如 nginx" bind:value={form.service} onkeydown={(e) => e.key === 'Enter' && submitService()} />
        </div>
        <button class="btn primary" onclick={submitService}>下发</button>
      </div>
    </div>
    <div class="card stat">
      <div class="k">发布（只重建受影响服务）</div>
      <div class="form-row" style="margin-top:9px">
        <div class="field"><label>Agent</label>
          <select bind:value={deploy.agent_id}>
            <option value="">选择</option>
            {#each agents as a}<option value={a.agent_id}>{a.agent_id}</option>{/each}
          </select>
        </div>
        <div class="field"><label>Compose 项目</label>
          <input placeholder="如 hotspot" bind:value={deploy.project} />
        </div>
        <div class="field" style="flex:2"><label>受影响服务（逗号分隔）</label>
          <input placeholder="如 backend,nginx" bind:value={deploy.targets} />
        </div>
        <button class="btn primary" onclick={submitDeploy}>发布</button>
      </div>
    </div>
  </div>
{:else}
  <div class="card"><div class="card-body hint">当前角色为只读，无法下发命令。</div></div>
{/if}

<div class="card sect">
  <div class="card-head">
    <h2>命令历史</h2>
    <button class="btn sm" onclick={load}>刷新</button>
  </div>
  <table class="table">
    <thead><tr><th>时间</th><th>ID</th><th>Agent</th><th>动作</th><th>状态</th><th>发起人</th><th>结果</th></tr></thead>
    <tbody>
      {#each commands as c (c.id)}
        <tr>
          <td class="mono text-2">{shortTime(c.created_at)}</td>
          <td class="mono">{c.id.slice(-6)}</td>
          <td class="mono">{c.agent_id}</td>
          <td>
            {c.action}
            {#if c.action === 'deploy'}
              <span class="text-2">{c.project} [{(c.targets ?? []).join(', ')}]</span>
            {:else if c.service}
              <span class="text-2">{c.service}</span>
            {/if}
          </td>
          <td><span class="badge {badgeOf(c.status)}">{c.status}</span></td>
          <td class="text-2">{c.created_by}</td>
          <td class="text-2" style="max-width:320px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title={c.result}>{c.result || '—'}</td>
        </tr>
      {:else}
        <tr><td colspan="7"><div class="empty">暂无命令</div></td></tr>
      {/each}
    </tbody>
  </table>
</div>
