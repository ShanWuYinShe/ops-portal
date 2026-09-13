<script lang="ts">
	import { api, type AgentToken } from '$lib/api';
	import { shortTime } from '$lib/state.svelte';
	import { askConfirm, toast } from '$lib/dialogs.svelte';

	let tokens = $state<AgentToken[]>([]);
	let agentID = $state('');
	let lastPlain = $state('');
	let copied = $state(false);

	async function load() {
		try {
			const r = await api<{ tokens: AgentToken[] }>('/api/portal/agent-tokens');
			tokens = r.tokens ?? [];
		} catch {
			/* 静默重试 */
		}
	}

	$effect(() => {
		load();
	});

	async function issue() {
		if (!agentID) {
			toast('agent_id 必填', 'err');
			return;
		}
		try {
			const r = await api<{ token: string; agent_id: string }>('/api/portal/agent-tokens/issue', {
				method: 'POST',
				body: JSON.stringify({ agent_id: agentID })
			});
			lastPlain = r.token;
			copied = false;
			toast(`已为 ${r.agent_id} 签发 token，仅显示一次`);
			agentID = '';
			await load();
		} catch (e) {
			toast('签发失败：' + (e as Error).message, 'err');
		}
	}

	async function revoke(t: AgentToken) {
		if (
			!(await askConfirm(
				'吊销 Token',
				`吊销 ${t.agent_id} 的 token（${t.id}）？\n该 agent 下次心跳即断连。`,
				true
			))
		)
			return;
		try {
			await api('/api/portal/agent-tokens/revoke', {
				method: 'POST',
				body: JSON.stringify({ id: t.id })
			});
			toast('已吊销');
			await load();
		} catch (e) {
			toast('吊销失败：' + (e as Error).message, 'err');
		}
	}

	async function copyPlain() {
		try {
			await navigator.clipboard.writeText(lastPlain);
			copied = true;
		} catch {
			toast('复制失败，请手动选择复制', 'err');
		}
	}
</script>

<div class="mb-4.5">
	<h1 class="page-title">Agent 令牌</h1>
	<div class="page-sub">被管机接入凭证 · 一机一枚 · portal 只存摘要</div>
</div>

<div class="card">
	<div class="card-head"><h2 class="text-base font-semibold">签发新令牌</h2></div>
	<div class="card-body">
		<div class="flex flex-wrap items-end gap-2.5">
			<div class="field">
				<label>Agent ID</label>
				<input class="input" placeholder="如 agent-hotspot-01" bind:value={agentID}
					onkeydown={(e) => e.key === 'Enter' && issue()} />
			</div>
			<button class="btn btn-primary" onclick={issue}>签发</button>
		</div>
		{#if lastPlain}
			<div class="mt-4 rounded-[7px] border border-[#f1d9a1] bg-warn-soft p-3">
				<div class="mb-1.5 font-semibold">请立即复制（关闭后不再显示）：</div>
				<div class="flex items-center gap-2.5">
					<code class="flex-1 break-all font-mono text-[12.5px]">{lastPlain}</code>
					<button class="btn btn-sm" onclick={copyPlain}>{copied ? '已复制' : '复制'}</button>
				</div>
			</div>
		{/if}
	</div>
</div>

<div class="card mt-4.5">
	<table class="table">
		<thead>
			<tr><th>ID</th><th>Agent</th><th>签发人</th><th>签发时间</th><th>状态</th><th></th></tr>
		</thead>
		<tbody>
			{#each tokens as t (t.id)}
				<tr>
					<td class="font-mono">{t.id}</td>
					<td><b>{t.agent_id}</b></td>
					<td class="text-ink-2">{t.created_by}</td>
					<td class="font-mono text-ink-2">{shortTime(t.created_at)}</td>
					<td>
						<span class="badge {t.revoked ? '' : 'bg-ok-soft text-ok'}">
							{t.revoked ? '已吊销' : '有效'}
						</span>
					</td>
					<td class="text-right">
						{#if !t.revoked}
							<button class="btn btn-sm btn-danger" onclick={() => revoke(t)}>吊销</button>
						{/if}
					</td>
				</tr>
			{:else}
				<tr><td colspan="6"><div class="p-6 text-center text-[13px] text-ink-muted">暂无令牌</div></td></tr>
			{/each}
		</tbody>
	</table>
</div>
