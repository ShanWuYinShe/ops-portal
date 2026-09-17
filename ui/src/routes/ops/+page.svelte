<script lang="ts">
	import { untrack } from 'svelte';
	import { api, type PortalCommand, type SiteOverview } from '$lib/api';
	import { badgeCls, badgeOf, portal, roleLevel, shortTime } from '$lib/state.svelte';
	import { poll } from '$lib/poll';
	import { askConfirm, toast } from '$lib/dialogs.svelte';

	let commands = $state<PortalCommand[]>([]);
	// 发布以网站为起点：选网站 → 选机器 → 勾选受影响服务。
	let deploySite = $state('');
	let deployAgent = $state('');
	let deployTargets = $state<string[]>([]);

	const canOperate = $derived(roleLevel(portal.whoami?.role) >= 2);
	const operableSites = $derived(portal.sites.filter((s) => s.containers.length > 0));
	const deploySiteObj = $derived(portal.sites.find((s) => s.name === deploySite));

	async function loadCommands() {
		try {
			const r = await api<{ commands: PortalCommand[] }>('/api/portal/commands');
			commands = r.commands ?? [];
		} catch {
			/* 轮询容错 */
		}
	}

	$effect(() => {
		poll(loadCommands, 8000);
	});

	// 网站列表刷新后，已选目标可能已不存在，清掉失效选择。
	$effect(() => {
		if (deploySite && !deploySiteObj) {
			deploySite = '';
			deployAgent = '';
			deployTargets = [];
		}
	});

	async function enqueue(payload: Record<string, unknown>, title: string, msg: string): Promise<boolean> {
		if (!(await askConfirm(title, msg, true))) return false;
		try {
			await api('/api/portal/commands/enqueue', {
				method: 'POST',
				body: JSON.stringify(payload)
			});
			toast('已下发，portal-agent 心跳时执行（约 10 秒内）');
			await loadCommands(); // 新命令立即可见于历史，不等轮询
			return true;
		} catch (e) {
			toast('下发失败：' + (e as Error).message, 'err');
			return false;
		}
	}

	// 该站在所选机器上的服务去重清单（勾选项，替代手填逗号分隔）。
	const deployServiceOptions = $derived.by(() => {
		const cs = deploySiteObj?.containers.filter((c) => c.portal_agent_id === deployAgent) ?? [];
		return [...new Set(cs.map((c) => c.service))];
	});

	// 切换网站/机器后勾选失效的清理。
	// 只随 deployServiceOptions 变化触发；deployTargets 须 untrack 读——
	// effect 内读写同一状态时 filter 恒返回新引用，会无限自触发，
	// 拖死调度队列导致本组件（乃至全站）响应冻结（whoami 已加载仍显示只读）。
	$effect(() => {
		const options = deployServiceOptions;
		untrack(() => {
			const pruned = deployTargets.filter((s) => options.includes(s));
			if (pruned.length !== deployTargets.length) deployTargets = pruned;
		});
	});

	function labelOf(site: SiteOverview): string {
		const agent = site.agents.map((a) => a.portal_agent_id).join(', ');
		return `${site.name}${agent ? `（${agent}）` : ''}`;
	}

	async function submitDeploy() {
		if (!deploySite || !deployAgent || deployTargets.length === 0) {
			toast('网站、机器、受影响服务均必选', 'err');
			return;
		}
		const ok = await enqueue(
			{
				portal_agent_id: deployAgent,
				action: 'deploy',
				project: deploySite, // compose 项目名即网站名
				targets: deployTargets
			},
			'发布重建',
			`在 ${deployAgent} 重建 ${deploySite} 的 [${deployTargets.join(', ')}]。\n只重建受影响服务；构建日志可在命令历史中查看。`
		);
		if (ok) deployTargets = [];
	}
</script>

<div class="mb-4.5">
	<h1 class="page-title">命令与发布</h1>
	<div class="page-sub">发布重建与命令历史 · 单个容器的启停重启在站点页行内操作</div>
</div>

{#if canOperate}
	<div class="card p-4">
		<div class="text-xs text-ink-muted">发布（只重建受影响服务）</div>
		<div class="mt-2.5 flex flex-wrap items-end gap-2.5">
			<div class="field">
				<label for="f-deploySite">网站</label>
				<select id="f-deploySite" class="input" bind:value={deploySite}>
					<option value="">选择</option>
					{#each operableSites as s (s.name)}<option value={s.name}>{labelOf(s)}</option>{/each}
				</select>
			</div>
			<div class="field">
				<label for="f-deployAgent">机器</label>
				<select id="f-deployAgent" class="input" bind:value={deployAgent} disabled={!deploySiteObj}>
					<option value="">选择</option>
					{#each (deploySiteObj?.agents ?? []).filter((a) => a.online) as a (a.portal_agent_id)}
						<option value={a.portal_agent_id}>{a.portal_agent_id}</option>
					{/each}
				</select>
			</div>
		</div>
		{#if deploySiteObj && deployAgent}
			<div class="mt-3">
				<div class="text-xs text-ink-muted">受影响服务（{deploySite} @ {deployAgent}）</div>
				<div class="mt-1.5 flex flex-wrap gap-x-4 gap-y-1.5">
					{#each deployServiceOptions as svc (svc)}
						<label class="flex cursor-pointer items-center gap-1.5 text-[13px]">
							<input type="checkbox" class="accent-primary" bind:group={deployTargets} value={svc} />
							<span class="font-mono">{svc}</span>
						</label>
					{/each}
				</div>
			</div>
			<div class="mt-3">
				<button class="btn btn-primary" onclick={submitDeploy} disabled={deployTargets.length === 0}>
					发布{deployTargets.length ? `（${deployTargets.length} 个服务）` : ''}
				</button>
			</div>
		{/if}
	</div>
{:else}
	<div class="card"><div class="card-body text-[13px] text-ink-muted">当前角色为只读，无法下发命令。</div></div>
{/if}

<div class="card mt-4.5">
	<div class="card-head">
		<h2 class="text-base font-semibold">命令历史</h2>
		<span class="text-xs text-ink-muted">{commands.length} 条（最多保留 100）</span>
	</div>
	<table class="table">
		<thead>
			<tr><th>时间</th><th>ID</th><th>Agent</th><th>动作</th><th>状态</th><th>发起人</th><th>结果</th></tr>
		</thead>
		<tbody>
			{#each commands as c (c.id)}
				<tr>
					<td class="font-mono text-ink-2">{shortTime(c.created_at)}</td>
					<td class="font-mono">{c.id.slice(-6)}</td>
					<td class="font-mono">{c.portal_agent_id}</td>
					<td>
						{c.action}
						{#if c.action === 'deploy'}
							<span class="text-ink-2">{c.project} [{(c.targets ?? []).join(', ')}]</span>
						{:else if c.service}
							<span class="text-ink-2">{c.service}</span>
						{/if}
					</td>
					<td><span class="badge {badgeCls(badgeOf(c.status))}">{c.status}</span></td>
					<td class="text-ink-2">{c.created_by}</td>
					<td class="max-w-80 truncate text-ink-2" title={c.result}>{c.result || '—'}</td>
				</tr>
			{:else}
				<tr><td colspan="7"><div class="p-6 text-center text-[13px] text-ink-muted">暂无命令</div></td></tr>
			{/each}
		</tbody>
	</table>
</div>
