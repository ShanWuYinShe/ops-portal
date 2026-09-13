<script lang="ts">
	import { api, type CertRecord, type PortalCommand } from '$lib/api';
	import { agentOnline, badgeCls, badgeOf, portal, relTime } from '$lib/state.svelte';

	let certs = $state<CertRecord[]>([]);
	let commands = $state<PortalCommand[]>([]);

	const online = $derived(portal.agents.filter((a) => agentOnline(a.last_seen)));
	const containers = $derived(portal.agents.flatMap((a) => a.heartbeat?.containers ?? []));
	const running = $derived(containers.filter((c) => c.state === 'running'));
	const pending = $derived(commands.filter((c) => c.status === 'queued' || c.status === 'acked'));
	const activeCerts = $derived(certs.filter((c) => c.status === 'active'));

	$effect(() => {
		const load = async () => {
			try {
				const [c, cmd] = await Promise.all([
					api<{ certs: CertRecord[] }>('/api/portal/certs'),
					api<{ commands: PortalCommand[] }>('/api/portal/commands')
				]);
				certs = c.certs ?? [];
				commands = cmd.commands ?? [];
			} catch {
				/* 轮询容错 */
			}
		};
		load();
		const t = setInterval(() => {
			if (!document.hidden) load();
		}, 10000);
		return () => clearInterval(t);
	});
</script>

<div class="mb-4.5 flex items-center justify-between">
	<div>
		<h1 class="page-title">仪表盘</h1>
		<div class="page-sub">全部站点实时概览 · 每 10 秒自动刷新</div>
	</div>
</div>

<div class="mb-4.5 grid grid-cols-[repeat(auto-fit,minmax(170px,1fr))] gap-3.5">
	<div class="card p-4">
		<div class="text-xs text-ink-muted">Agent 在线</div>
		<div class="mt-0.5 text-2xl font-bold">
			{online.length}<span class="text-sm text-ink-muted"> / {portal.agents.length}</span>
		</div>
		<div class="mt-0.5 text-xs text-ink-2">被管主机</div>
	</div>
	<div class="card p-4">
		<div class="text-xs text-ink-muted">容器运行</div>
		<div class="mt-0.5 text-2xl font-bold">
			{running.length}<span class="text-sm text-ink-muted"> / {containers.length}</span>
		</div>
		<div class="mt-0.5 text-xs text-ink-2">全部 compose 服务</div>
	</div>
	<div class="card p-4">
		<div class="text-xs text-ink-muted">待执行命令</div>
		<div class="mt-0.5 text-2xl font-bold">{pending.length}</div>
		<div class="mt-0.5 text-xs text-ink-2">队列中的操作</div>
	</div>
	<div class="card p-4">
		<div class="text-xs text-ink-muted">有效证书</div>
		<div class="mt-0.5 text-2xl font-bold">{activeCerts.length}</div>
		<div class="mt-0.5 text-xs text-ink-2">已登记设备/agent</div>
	</div>
</div>

<div class="card">
	{#if portal.agents.length === 0}
		<div class="card-body">
			<div class="p-8 text-center text-[13px] text-ink-muted">
				暂无 agent 接入。按 docs/agent-protocol.md 在被管机起 agent 即可接入。
			</div>
		</div>
	{:else}
		<table class="table">
			<thead>
				<tr><th>Agent</th><th>主机</th><th>状态</th><th>上次心跳</th><th>容器</th><th>Compose 项目</th></tr>
			</thead>
			<tbody>
				{#each portal.agents as a (a.agent_id)}
					<tr>
						<td class="font-mono">{a.agent_id}</td>
						<td>{a.heartbeat?.hostname || '—'}</td>
						<td>
							<span class="badge {badgeCls(agentOnline(a.last_seen) ? 'ok' : 'err')}">
								{agentOnline(a.last_seen) ? '在线' : '离线'}
							</span>
						</td>
						<td>{relTime(a.last_seen)}</td>
						<td>
							{(a.heartbeat?.containers ?? []).filter((c) => c.state === 'running').length}
							运行 / {(a.heartbeat?.containers ?? []).length} 总数
						</td>
						<td>
							{#each a.heartbeat?.compose_projects ?? [] as p (p)}<span class="chip">{p}</span>{/each}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{/if}
</div>

<div class="card mt-4.5">
	<div class="card-head">
		<h2 class="text-base font-semibold">站点版本</h2>
		<span class="text-xs text-ink-muted">来自各 agent 的 AGENT_SITES 上报</span>
	</div>
	<div class="card-body">
		{#each portal.agents as a (a.agent_id)}
			{#each a.heartbeat?.sites ?? [] as s (a.agent_id + s.name)}
				<div class="flex items-center gap-2.5 py-1">
					<span class="badge {badgeCls(s.dirty ? 'warn' : 'ok')}">{s.name}</span>
					<span class="font-mono text-[12.5px] text-ink-2">{s.branch}@{s.head?.slice(0, 10)}</span>
					{#if s.dirty}<span class="text-xs text-ink-muted">有未提交改动</span>{/if}
				</div>
			{:else}
				<div class="text-center text-[13px] text-ink-muted">暂无站点版本上报</div>
			{/each}
		{/each}
	</div>
</div>
