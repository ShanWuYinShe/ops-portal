<script lang="ts">
	import { api, type PortalCommand } from '$lib/api';
	import { badgeCls, badgeOf, portal, roleLevel, shortTime } from '$lib/state.svelte';
	import { askConfirm, toast } from '$lib/dialogs.svelte';

	let commands = $state<PortalCommand[]>([]);
	let form = $state({ agent_id: '', action: 'restart', service: '' });
	let deploy = $state({ agent_id: '', project: '', targets: '' });

	const canOperate = $derived(roleLevel(portal.whoami?.role) >= 2);

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
		if (!form.agent_id || !form.service) {
			toast('agent 与服务名必填', 'err');
			return;
		}
		const verb = { start: '启动', stop: '停止', restart: '重启' }[form.action] ?? form.action;
		const ok = await enqueue(
			{ agent_id: form.agent_id, action: form.action, service: form.service.trim() },
			`${verb}服务`,
			`${form.agent_id} → ${verb} ${form.service}`,
			form.action !== 'start'
		);
		if (ok) form.service = '';
	}

	async function submitDeploy() {
		const targets = deploy.targets
			.split(',')
			.map((s) => s.trim())
			.filter(Boolean);
		if (!deploy.agent_id || !deploy.project || targets.length === 0) {
			toast('agent、project、服务列表均必填', 'err');
			return;
		}
		const ok = await enqueue(
			{
				agent_id: deploy.agent_id,
				action: 'deploy',
				project: deploy.project.trim(),
				targets
			},
			'发布重建',
			`在 ${deploy.agent_id} 重建 ${deploy.project} 的 [${targets.join(', ')}]。\n只重建受影响服务；构建日志可在命令历史中查看。`,
			true
		);
		if (ok) deploy.targets = '';
	}
</script>

<div class="mb-4.5">
	<h1 class="page-title">命令与发布</h1>
	<div class="page-sub">容器操作与按变更重建 · 命令历史每 8 秒刷新</div>
</div>

{#if canOperate}
	<div class="mb-4.5 grid grid-cols-1 gap-3.5 lg:grid-cols-2">
		<div class="card p-4">
			<div class="text-xs text-ink-muted">容器快捷操作</div>
			<div class="mt-2.5 flex flex-wrap items-end gap-2.5">
				<div class="field">
					<label>Agent</label>
					<select class="input" bind:value={form.agent_id}>
						<option value="">选择</option>
						{#each portal.agents as a (a.agent_id)}<option value={a.agent_id}>{a.agent_id}</option>{/each}
					</select>
				</div>
				<div class="field min-w-25 flex-none">
					<label>动作</label>
					<select class="input" bind:value={form.action}>
						<option value="restart">重启</option>
						<option value="start">启动</option>
						<option value="stop">停止</option>
					</select>
				</div>
				<div class="field">
					<label>Compose 服务名</label>
					<input class="input" placeholder="如 nginx" bind:value={form.service}
						onkeydown={(e) => e.key === 'Enter' && submitService()} />
				</div>
				<button class="btn btn-primary" onclick={submitService}>下发</button>
			</div>
		</div>
		<div class="card p-4">
			<div class="text-xs text-ink-muted">发布（只重建受影响服务）</div>
			<div class="mt-2.5 flex flex-wrap items-end gap-2.5">
				<div class="field">
					<label>Agent</label>
					<select class="input" bind:value={deploy.agent_id}>
						<option value="">选择</option>
						{#each portal.agents as a (a.agent_id)}<option value={a.agent_id}>{a.agent_id}</option>{/each}
					</select>
				</div>
				<div class="field">
					<label>Compose 项目</label>
					<input class="input" placeholder="如 hotspot" bind:value={deploy.project} />
				</div>
				<div class="field flex-[2]">
					<label>受影响服务（逗号分隔）</label>
					<input class="input" placeholder="如 backend,nginx" bind:value={deploy.targets} />
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
