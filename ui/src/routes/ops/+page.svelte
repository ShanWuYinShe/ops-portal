<script lang="ts">
	import { api, type PortalCommand, type SiteOverview } from '$lib/api';
	import { badgeCls, badgeOf, portal, roleLevel, shortTime } from '$lib/state.svelte';
	import { askConfirm, toast } from '$lib/dialogs.svelte';

	let commands = $state<PortalCommand[]>([]);
	// 一切操作以网站为起点：选网站 → 自动带出机器/compose 项目/服务。
	let svcSite = $state('');
	let svcTarget = $state(''); // `${agent_id}|${service}`
	let svcAction = $state<'start' | 'stop' | 'restart'>('restart');
	let deploySite = $state('');
	let deployAgent = $state('');
	let deployTargets = $state('');

	const canOperate = $derived(roleLevel(portal.whoami?.role) >= 2);
	const operableSites = $derived(portal.sites.filter((s) => s.containers.length > 0));

	const svcSiteObj = $derived(portal.sites.find((s) => s.name === svcSite));
	const deploySiteObj = $derived(portal.sites.find((s) => s.name === deploySite));

	$effect(() => {
		const load = async () => {
			try {
				const r = await api<{ commands: PortalCommand[] }>('/api/portal/commands');
				commands = r.commands ?? [];
			} catch {
				/* 轮询容错 */
			}
		};
		load();
		const t = setInterval(() => {
			if (!document.hidden) load();
		}, 8000);
		return () => clearInterval(t);
	});

	// 网站列表刷新后，已选目标可能已不存在，清掉失效选择。
	$effect(() => {
		if (svcSite && !svcSiteObj) {
			svcSite = '';
			svcTarget = '';
		}
		if (deploySite && !deploySiteObj) {
			deploySite = '';
			deployAgent = '';
			deployTargets = '';
		}
	});

	async function enqueue(
		payload: Record<string, unknown>,
		title: string,
		msg: string,
		danger: boolean
	): Promise<boolean> {
		if (!(await askConfirm(title, msg, danger))) return false;
		try {
			await api('/api/portal/commands/enqueue', {
				method: 'POST',
				body: JSON.stringify(payload)
			});
			toast('已下发，agent 心跳时执行（约 10 秒内）');
			return true;
		} catch (e) {
			toast('下发失败：' + (e as Error).message, 'err');
			return false;
		}
	}

	async function submitService() {
		const [agentId, service] = svcTarget.split('|');
		if (!agentId || !service) {
			toast('先选择网站和服务', 'err');
			return;
		}
		const verb = { start: '启动', stop: '停止', restart: '重启' }[svcAction];
		const ok = await enqueue(
			{ agent_id: agentId, action: svcAction, service },
			`${verb}服务`,
			`${agentId} → ${verb} ${service}`,
			svcAction !== 'start'
		);
		if (ok) svcTarget = '';
	}

	async function submitDeploy() {
		const targets = deployTargets
			.split(',')
			.map((s) => s.trim())
			.filter(Boolean);
		if (!deploySite || !deployAgent || targets.length === 0) {
			toast('网站、机器、服务列表均必填', 'err');
			return;
		}
		const ok = await enqueue(
			{
				agent_id: deployAgent,
				action: 'deploy',
				project: deploySite, // compose 项目名即网站名
				targets
			},
			'发布重建',
			`在 ${deployAgent} 重建 ${deploySite} 的 [${targets.join(', ')}]。\n只重建受影响服务；构建日志可在命令历史中查看。`,
			true
		);
		if (ok) deployTargets = '';
	}

	function labelOf(site: SiteOverview): string {
		const agent = site.agents.map((a) => a.agent_id).join(', ');
		return `${site.name}${agent ? `（${agent}）` : ''}`;
	}
</script>

<div class="mb-4.5">
	<h1 class="page-title">命令与发布</h1>
	<div class="page-sub">按网站选择目标 · 命令历史每 8 秒刷新</div>
</div>

{#if canOperate}
	<div class="mb-4.5 grid grid-cols-1 gap-3.5 lg:grid-cols-2">
		<div class="card p-4">
			<div class="text-xs text-ink-muted">容器快捷操作</div>
			<div class="mt-2.5 flex flex-wrap items-end gap-2.5">
				<div class="field">
					<label for="f-svcSite">网站</label>
					<select id="f-svcSite" class="input" bind:value={svcSite}>
						<option value="">选择</option>
						{#each operableSites as s (s.name)}<option value={s.name}>{labelOf(s)}</option>{/each}
					</select>
				</div>
				<div class="field min-w-25 flex-none">
					<label for="f-svcAction">动作</label>
					<select id="f-svcAction" class="input" bind:value={svcAction}>
						<option value="restart">重启</option>
						<option value="start">启动</option>
						<option value="stop">停止</option>
					</select>
				</div>
				<div class="field">
					<label for="f-svcTarget">服务</label>
					<select id="f-svcTarget" class="input" bind:value={svcTarget} disabled={!svcSiteObj}>
						<option value="">选择</option>
						{#each (svcSiteObj?.containers ?? []) as c (c.agent_id + c.id)}
							<option value="{c.agent_id}|{c.service}">
								{c.service}{svcSiteObj && svcSiteObj.agents.length > 1 ? ` @ ${c.agent_id}` : ''}
							</option>
						{/each}
					</select>
				</div>
				<button class="btn btn-primary" onclick={submitService} disabled={!svcTarget}>下发</button>
			</div>
		</div>
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
						{#each (deploySiteObj?.agents ?? []).filter((a) => a.online) as a (a.agent_id)}
							<option value={a.agent_id}>{a.agent_id}</option>
						{/each}
					</select>
				</div>
				<div class="field flex-[2]">
					<label for="f-deployTargets">受影响服务（逗号分隔）</label>
					<input id="f-deployTargets" class="input" placeholder="如 backend,nginx" bind:value={deployTargets} />
				</div>
				<button class="btn btn-primary" onclick={submitDeploy}>发布</button>
			</div>
		</div>
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
					<td class="font-mono">{c.agent_id}</td>
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
					<td
						class="max-w-80 truncate text-ink-2"
						title={c.result}
					>
						{c.result || '—'}
					</td>
				</tr>
			{:else}
				<tr><td colspan="7"><div class="p-6 text-center text-[13px] text-ink-muted">暂无命令</div></td></tr>
			{/each}
		</tbody>
	</table>
</div>
