<script lang="ts">
	import { api, type CertRecord, type PortalCommand } from '$lib/api';
	import { agentOnline, badgeCls, portal, relTime } from '$lib/state.svelte';

	let certs = $state<CertRecord[]>([]);
	let commands = $state<PortalCommand[]>([]);

	const online = $derived(portal.agents.filter((a) => agentOnline(a.last_seen)));
	const containers = $derived(portal.agents.flatMap((a) => a.heartbeat?.containers ?? []));
	const running = $derived(containers.filter((c) => c.state === 'running'));
	const pending = $derived(commands.filter((c) => c.status === 'queued' || c.status === 'acked'));
	const activeCerts = $derived(certs.filter((c) => c.status === 'active'));
	const sitesWithRuntime = $derived(portal.sites.filter((s) => s.containers.length > 0));

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
		<div class="text-xs text-ink-muted">网站</div>
		<div class="mt-0.5 text-2xl font-bold">
			{sitesWithRuntime.length}<span class="text-sm text-ink-muted"> / {portal.sites.length}</span>
		</div>
		<div class="mt-0.5 text-xs text-ink-2">有运行实例 / 已接入</div>
	</div>
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
		<div class="mt-0.5 text-xs text-ink-2">已登记设备/portal-agent</div>
	</div>
</div>

<div class="card">
	{#if portal.sites.length === 0}
		<div class="card-body">
			<div class="p-8 text-center text-[13px] text-ink-muted">
				暂无网站接入。按 docs/portal-agent-protocol.md 在被管机起 portal-agent 或注册原生接口即可接入。
			</div>
		</div>
	{:else}
		<table class="table">
			<thead>
				<tr><th>网站</th><th>来源</th><th>部署机器</th><th>容器</th><th>版本</th><th>ops 接口</th></tr>
			</thead>
			<tbody>
				{#each portal.sites as s (s.name)}
					<tr>
						<td><b>{s.name}</b></td>
						<td class="text-ink-2">{s.source === 'native' ? '原生注册' : s.source === 'both' ? 'portal-agent + 原生' : 'portal-agent'}</td>
						<td>
							{#each s.agents as a (a.portal_agent_id)}
								<span class="badge mr-1 {badgeCls(a.online ? 'ok' : 'err')}">{a.portal_agent_id}</span>
							{/each}
						</td>
						<td>
							{s.containers.filter((c) => c.state === 'running').length} 运行 / {s.containers.length} 总数
						</td>
						<td>
							{#each s.git as g (g.portal_agent_id + g.name)}
								<span class="font-mono text-[12.5px] text-ink-2">
									<span class="badge mr-1 {badgeCls(g.dirty ? 'warn' : 'ok')}">{g.branch}</span>@{g.head?.slice(0, 10)}
								</span>
							{/each}
						</td>
						<td class="font-mono text-xs text-ink-2">{s.ops_url || '—'}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{/if}
</div>

<div class="card mt-4.5">
	<div class="card-head">
		<h2 class="text-base font-semibold">Agent 主机</h2>
		<span class="text-xs text-ink-muted">心跳来源</span>
	</div>
	<div class="card-body">
		{#if portal.agents.length === 0}
			<div class="text-center text-[13px] text-ink-muted">暂无 portal-agent 接入</div>
		{:else}
			{#each portal.agents as a (a.portal_agent_id)}
				<div class="flex items-center gap-2.5 py-1">
					<span class="badge {badgeCls(agentOnline(a.last_seen) ? 'ok' : 'err')}">
						{agentOnline(a.last_seen) ? '在线' : '离线'}
					</span>
					<span class="font-mono text-[13px]">{a.portal_agent_id}</span>
					<span class="text-xs text-ink-2">{a.heartbeat?.hostname}</span>
					<span class="text-xs text-ink-muted">{relTime(a.last_seen)}</span>
				</div>
			{/each}
		{/if}
	</div>
</div>
