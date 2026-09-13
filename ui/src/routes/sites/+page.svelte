<script lang="ts">
	import { api } from '$lib/api';
	import {
		agentOnline,
		badgeCls,
		badgeOf,
		portal,
		relTime,
		roleLevel
	} from '$lib/state.svelte';
	import { askConfirm, toast } from '$lib/dialogs.svelte';

	const canOperate = $derived(roleLevel(portal.whoami?.role) >= 2);

	async function act(agentId: string, service: string, action: string) {
		const verb = { start: '启动', stop: '停止', restart: '重启' }[action] ?? action;
		if (
			!(await askConfirm(
				`${verb}服务`,
				`确定在 ${agentId} 上${verb} ${service} 吗？`,
				action !== 'start'
			))
		)
			return;
		try {
			await api('/api/portal/commands/enqueue', {
				method: 'POST',
				body: JSON.stringify({ agent_id: agentId, action, service })
			});
			toast(`已下发：${verb} ${service}，agent 心跳时执行`);
		} catch (e) {
			toast('下发失败：' + (e as Error).message, 'err');
		}
	}
</script>

<div class="mb-4.5">
	<h1 class="page-title">站点与服务</h1>
	<div class="page-sub">按主机分组的容器状态与快捷操作 · 随全局刷新更新</div>
</div>

{#each portal.agents as a (a.agent_id)}
	<div class="card mt-3.5">
		<div class="card-head">
			<div class="flex items-center gap-2.5">
				<h2 class="font-mono text-base font-semibold">{a.agent_id}</h2>
				<span class="badge {badgeCls(agentOnline(a.last_seen) ? 'ok' : 'err')}">
					{agentOnline(a.last_seen) ? '在线' : '离线'}
				</span>
				<span class="text-xs text-ink-muted">{a.heartbeat?.hostname} · {relTime(a.last_seen)}</span>
			</div>
			<div>
				{#each a.heartbeat?.compose_projects ?? [] as p (p)}<span class="chip">{p}</span>{/each}
			</div>
		</div>

		{#if (a.heartbeat?.sites ?? []).length}
			<div class="flex flex-wrap gap-4 border-b border-line px-4 py-2.5">
				{#each a.heartbeat.sites as s (s.name)}
					<span class="text-[13px] text-ink-2">
						<span class="badge {badgeCls(s.dirty ? 'warn' : 'ok')} mr-1.5">{s.name}</span>
						<span class="font-mono text-xs">{s.branch}@{s.head?.slice(0, 10)}</span>
						{#if s.dirty}<span class="text-xs text-ink-muted">（脏）</span>{/if}
					</span>
				{/each}
			</div>
		{/if}

		<table class="table">
			<thead>
				<tr><th>服务</th><th>项目</th><th>状态</th><th>详情</th><th>镜像</th><th></th></tr>
			</thead>
			<tbody>
				{#each [...(a.heartbeat?.containers ?? [])].sort((x, y) => (x.project + x.service).localeCompare(y.project + y.service)) as c (c.id)}
					<tr>
						<td><b>{c.service || c.name}</b></td>
						<td class="text-ink-2">{c.project}</td>
						<td><span class="badge {badgeCls(badgeOf(c.state))}">{c.state}</span></td>
						<td class="text-ink-2">{c.status}</td>
						<td class="font-mono text-ink-2">{c.image}</td>
						<td class="text-right whitespace-nowrap">
							{#if canOperate && agentOnline(a.last_seen)}
								<button class="btn btn-sm" onclick={() => act(a.agent_id, c.service, 'restart')}>重启</button>
								<button class="btn btn-sm" onclick={() => act(a.agent_id, c.service, 'stop')}>停止</button>
							{/if}
						</td>
					</tr>
				{:else}
					<tr><td colspan="6"><div class="p-6 text-center text-[13px] text-ink-muted">该 agent 未上报容器</div></td></tr>
				{/each}
			</tbody>
		</table>
	</div>
{:else}
	<div class="card">
		<div class="card-body">
			<div class="p-8 text-center text-[13px] text-ink-muted">
				暂无 agent 接入，按 docs/agent-protocol.md 接入第一个站点。
			</div>
		</div>
	</div>
{/each}
