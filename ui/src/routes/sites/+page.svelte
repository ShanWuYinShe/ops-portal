<script lang="ts">
	import { api, type SiteOverview } from '$lib/api';
	import { badgeCls, badgeOf, portal, roleLevel } from '$lib/state.svelte';
	import { askConfirm, toast } from '$lib/dialogs.svelte';

	const canOperate = $derived(roleLevel(portal.whoami?.role) >= 2);
	const isAdmin = $derived(roleLevel(portal.whoami?.role) >= 3);

	const SOURCE_LABEL: Record<SiteOverview['source'], string> = {
		agent: 'agent 上报',
		native: '原生注册',
		both: 'agent + 原生'
	};

	async function act(agentId: string, project: string, service: string, action: string) {
		const verb = { start: '启动', stop: '停止', restart: '重启' }[action] ?? action;
		if (
			!(await askConfirm(
				`${verb}服务`,
				`确定在 ${agentId} 上${verb} ${project} / ${service} 吗？`,
				action !== 'start'
			))
		)
			return;
		try {
			// project 限定 compose 项目：同一机器多站点同名 service 不再误命中。
			await api('/api/portal/commands/enqueue', {
				method: 'POST',
				body: JSON.stringify({ agent_id: agentId, action, service, project })
			});
			toast(`已下发：${verb} ${service}，agent 心跳时执行`);
		} catch (e) {
			toast('下发失败：' + (e as Error).message, 'err');
		}
	}

	function runningOf(site: SiteOverview): number {
		return site.containers.filter((c) => c.state === 'running').length;
	}

	async function unregister(site: SiteOverview) {
		if (
			!(await askConfirm(
				'注销原生注册',
				`下掉 ${site.name} 的 ops 接口注册（${site.ops_url}）？\nagent 心跳不受影响，探测结果一并清除。`,
				true
			))
		)
			return;
		try {
			await api('/api/portal/sites/unregister', {
				method: 'POST',
				body: JSON.stringify({ name: site.name })
			});
			toast(`已注销 ${site.name} 的原生注册`);
		} catch (e) {
			toast('注销失败：' + (e as Error).message, 'err');
		}
	}
</script>

<div class="mb-4.5">
	<h1 class="page-title">站点与服务</h1>
	<div class="page-sub">按网站分组的运行状态与快捷操作 · 随全局刷新更新</div>
</div>

{#if portal.sites.length === 0}
	<div class="card">
		<div class="card-body">
			<div class="p-8 text-center text-[13px] text-ink-muted">
				暂无网站接入。agent 心跳上报的 compose 项目与原生注册的网站都会出现在这里。
			</div>
		</div>
	</div>
{:else}
	{#each portal.sites as site (site.name)}
		<div class="card mt-3.5">
			<div class="card-head flex-wrap">
				<div class="flex flex-wrap items-center gap-2.5">
					<h2 class="text-base font-semibold">{site.name}</h2>
					<span class="badge {site.source === 'native' ? 'bg-warn-soft text-warn' : 'bg-primary-soft text-primary'}">
						{SOURCE_LABEL[site.source]}
					</span>
					{#each site.agents as a (a.agent_id)}
						<span class="text-xs text-ink-2">
							<span class="font-mono">{a.agent_id}</span>
							<span class="badge ml-1 {badgeCls(a.online ? 'ok' : 'err')}">
								{a.online ? '在线' : '离线'}
							</span>
						</span>
					{/each}
					{#if site.containers.length}
						<span class="text-xs text-ink-muted">
							{runningOf(site)} 运行 / {site.containers.length} 容器
						</span>
					{/if}
				</div>
				<div class="flex flex-wrap items-center gap-2.5">
					{#if site.ops_url}
						<span class="font-mono text-xs text-ink-muted" title="原生注册的 ops 接口">
							{site.ops_url}
						</span>
						{#if site.reachable === true}
							<span class="badge bg-ok-soft text-ok" title="portal 拨测 {site.ops_url}/ops/v1/health 通过">可达</span>
						{:else if site.reachable === false}
							<span class="badge bg-err-soft text-err" title="portal 拨测失败，检查接口与放行清单">不可达</span>
						{:else}
							<span class="badge" title="尚未拨测（等 service 证书签发后的下一轮）">未探测</span>
						{/if}
						{#if isAdmin}
							<button class="btn btn-sm btn-danger" onclick={() => unregister(site)}>注销注册</button>
						{/if}
					{/if}
					{#each site.git as g (g.agent_id + g.name)}
						<span class="text-xs text-ink-2">
							<span class="badge {badgeCls(g.dirty ? 'warn' : 'ok')}">{g.branch}</span>
							<span class="font-mono">@{g.head?.slice(0, 10)}</span>
							{#if g.dirty}<span class="text-ink-muted">（脏）</span>{/if}
						</span>
					{/each}
				</div>
			</div>

			{#if site.containers.length}
				<table class="table">
					<thead>
						<tr><th>服务</th><th>所在机器</th><th>状态</th><th>详情</th><th>镜像</th><th></th></tr>
					</thead>
					<tbody>
						{#each site.containers as c (c.agent_id + c.id)}
							<tr>
								<td><b>{c.service || c.name}</b></td>
								<td class="font-mono text-ink-2">{c.agent_id}</td>
								<td><span class="badge {badgeCls(badgeOf(c.state))}">{c.state}</span></td>
								<td class="text-ink-2">{c.status}</td>
								<td class="font-mono text-ink-2">{c.image}</td>
								<td class="text-right whitespace-nowrap">
									{#if canOperate && site.agents.find((a) => a.agent_id === c.agent_id)?.online}
										<button class="btn btn-sm" onclick={() => act(c.agent_id, c.project, c.service, 'restart')}>重启</button>
										<button class="btn btn-sm" onclick={() => act(c.agent_id, c.project, c.service, 'stop')}>停止</button>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			{:else}
				<div class="card-body">
					<div class="py-5 text-center text-[13px] text-ink-muted">
						{#if site.source === 'native'}
							已注册 ops 接口，但没有 agent 心跳报告该网站的运行实例。
						{:else}
							该网站暂无运行中的容器上报。
						{/if}
					</div>
				</div>
			{/if}
		</div>
	{/each}
{/if}
