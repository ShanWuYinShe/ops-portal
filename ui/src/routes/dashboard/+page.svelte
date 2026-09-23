<script lang="ts">
	import { badgeCls, portal, relTime } from '$lib/state.svelte';
	import SiteHealthBadge from '$lib/SiteHealthBadge.svelte';

	// 仪表盘只做摘要与跳转入口：站点运行详情在 /sites，操作在 /ops。
	// 勿在此重复 sites 的表格/操作——数据源同为全局 portal 状态（layout 轮询）。
	// agent 在线态用后端返回的 online 字段（与告警看门狗同口径），前端不自算。

	const online = $derived(portal.agents.filter((a) => a.online));
	const containers = $derived(portal.agents.flatMap((a) => a.heartbeat?.containers ?? []));
	const running = $derived(containers.filter((c) => c.state === 'running'));
	const sitesWithRuntime = $derived(portal.sites.filter((s) => s.containers.length > 0));

	// 主机徽标 hover 补充真实主机名：多台 agent 同 id 命名风格时（agent-server-01…）
	// 仅靠 id 无法对应到具体机器，hostname 提供第二标识。
	const agentHostname = (id: string) =>
		portal.agents.find((a) => a.portal_agent_id === id)?.heartbeat?.hostname;
</script>

<div class="mb-4.5">
	<h1 class="page-title">仪表盘</h1>
	<div class="page-sub">全部站点实时概览 · 随全局刷新更新</div>
</div>

<div class="mb-4.5 grid grid-cols-[repeat(auto-fit,minmax(170px,1fr))] gap-3.5">
	<a class="card p-4 transition-colors hover:border-primary" href="/sites">
		<div class="text-xs text-ink-muted">网站</div>
		<div class="mt-0.5 text-2xl font-bold">
			{sitesWithRuntime.length}<span class="text-sm text-ink-muted"> / {portal.sites.length}</span>
		</div>
		<div class="mt-0.5 text-xs text-ink-2">有运行实例 / 已接入</div>
	</a>
	<a class="card p-4 transition-colors hover:border-primary" href="/sites">
		<div class="text-xs text-ink-muted">Agent 在线</div>
		<div class="mt-0.5 text-2xl font-bold">
			{online.length}<span class="text-sm text-ink-muted"> / {portal.agents.length}</span>
		</div>
		<div class="mt-0.5 text-xs text-ink-2">被管主机</div>
	</a>
	<a class="card p-4 transition-colors hover:border-primary" href="/sites">
		<div class="text-xs text-ink-muted">容器运行</div>
		<div class="mt-0.5 text-2xl font-bold">
			{running.length}<span class="text-sm text-ink-muted"> / {containers.length}</span>
		</div>
		<div class="mt-0.5 text-xs text-ink-2">全部 compose 服务</div>
	</a>
	<a class="card p-4 transition-colors hover:border-primary" href="/ops">
		<div class="text-xs text-ink-muted">发布与命令</div>
		<div class="mt-0.5 text-2xl font-bold">→</div>
		<div class="mt-0.5 text-xs text-ink-2">deploy / 重启 / 历史查询</div>
	</a>
	<a class="card p-4 transition-colors hover:border-primary" href="/certs">
		<div class="text-xs text-ink-muted">用户与设备</div>
		<div class="mt-0.5 text-2xl font-bold">→</div>
		<div class="mt-0.5 text-xs text-ink-2">证书登记 / 登录绑定</div>
	</a>
</div>

<div class="card">
	<div class="card-head"><h2 class="text-base font-semibold">站点健康一览</h2></div>
	{#if portal.sites.length === 0}
		<div class="card-body">
			<div class="p-8 text-center text-[13px] text-ink-muted">
				暂无网站接入。按 docs/portal-agent-protocol.md 在被管机起 portal-agent 或注册原生接口即可接入。
			</div>
		</div>
	{:else}
		<table class="table">
			<tbody>
				{#each portal.sites as s (s.name)}
					<tr>
						<td class="w-px whitespace-nowrap">
							<a class="hover:text-primary" href="/sites"><b>{s.name}</b></a>
							{#if s.ops_url}<div class="text-xs text-ink-muted">{s.ops_url}</div>{/if}
						</td>
						<td><SiteHealthBadge site={s} /></td>
						<td>
							{#each s.agents as a (a.portal_agent_id)}
								<span
									class="badge mr-1 {badgeCls(a.online ? 'ok' : 'err')}"
									title={agentHostname(a.portal_agent_id) || ''}
								>
									{a.portal_agent_id}
								</span>
							{/each}
						</td>
						<td class="text-ink-2">
							{#if s.containers.length}
								{s.containers.filter((c) => c.state === 'running').length} 运行 / {s.containers.length} 容器
							{/if}
						</td>
						<td class="text-right">
							{#each s.git as g (g.portal_agent_id + g.name)}
								<span
									class="font-mono text-[12.5px] text-ink-2"
									title="{g.path} · {g.branch}@{g.head}"
								>
									<span class="badge mr-1 {badgeCls(g.dirty ? 'warn' : 'ok')}">{g.branch}</span>@{g.head?.slice(0, 10)}
								</span>
							{:else}
								<span
									class="text-xs text-ink-muted"
									title="agent 未上报该站 git 版本：检查被管机 PORTAL_AGENT_SITES 里该站路径是否为 git checkout 且 agent（uid 1001）可读；采集失败的条目会被静默跳过"
								>
									未上报 git
								</span>
							{/each}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{/if}
</div>

<div class="card mt-4.5">
	<div class="card-head">
		<h2 class="text-base font-semibold">Agent 主机</h2>
		<span class="text-xs text-ink-muted">心跳来源 · 掉线时审计记 alert.agent_offline</span>
	</div>
	<div class="card-body">
		{#if portal.agents.length === 0}
			<div class="text-center text-[13px] text-ink-muted">暂无 portal-agent 接入</div>
		{:else}
			{#each portal.agents as a (a.portal_agent_id)}
				<div class="flex items-center gap-2.5 py-1">
					<span class="badge {badgeCls(a.online ? 'ok' : 'err')}">
						{a.online ? '在线' : '离线'}
					</span>
					<span class="font-mono text-[13px]">{a.portal_agent_id}</span>
					<span class="text-xs text-ink-2">{a.heartbeat?.hostname}</span>
					<span class="text-xs text-ink-muted">{relTime(a.last_seen)}</span>
				</div>
			{/each}
		{/if}
	</div>
</div>
