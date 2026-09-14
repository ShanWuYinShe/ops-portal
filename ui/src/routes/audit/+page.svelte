<script lang="ts">
	import { api, type AuditEntry } from '$lib/api';
	import { badgeCls, badgeOf, shortTime } from '$lib/state.svelte';
	import { askConfirm, toast } from '$lib/dialogs.svelte';

	let entries = $state<AuditEntry[]>([]);
	let filter = $state('');

	$effect(() => {
		const load = async () => {
			try {
				const r = await api<{ entries: AuditEntry[] }>('/api/portal/audit');
				entries = r.entries ?? [];
			} catch {
				/* 轮询容错 */
			}
		};
		load();
		const t = setInterval(() => {
			if (!document.hidden) load();
		}, 15000);
		return () => clearInterval(t);
	});

	const filtered = $derived(
		entries.filter((e) => {
			if (!filter) return true;
			const q = filter.toLowerCase();
			return [e.actor, e.action, e.object, e.note, e.result].some((v) =>
				(v || '').toLowerCase().includes(q)
			);
		})
	);

	async function backup() {
		if (!(await askConfirm('备份数据库', '在线快照 portal.db 到 data/backups/（只留最新 14 份）？', false)))
			return;
		try {
			const r = await api<{ backup: string }>('/api/portal/backup', { method: 'POST' });
			toast(`已备份：${r.backup}`);
		} catch (e) {
			toast('备份失败：' + (e as Error).message, 'err');
		}
	}
</script>

<div class="mb-4.5 flex items-center justify-between">
	<div>
		<h1 class="page-title">审计日志</h1>
		<div class="page-sub">谁 · 用哪张证书 · 对什么 · 做了什么 · 结果如何（最近 200 条）</div>
	</div>
	<div class="flex items-center gap-2.5">
		<input class="input w-55" placeholder="搜索过滤…" bind:value={filter} />
		<button class="btn" onclick={backup}>备份数据库</button>
	</div>
</div>

<div class="card">
	<table class="table">
		<thead>
			<tr><th>时间</th><th>操作人</th><th>证书</th><th>动作</th><th>对象</th><th>结果</th><th>备注</th></tr>
		</thead>
		<tbody>
			{#each filtered as e, i (e.at + e.action + i)}
				<tr>
					<td class="font-mono text-ink-2">{shortTime(e.at)}</td>
					<td><b>{e.actor}</b></td>
					<td class="font-mono text-ink-2">{e.cert ? e.cert.slice(0, 10) + '…' : '—'}</td>
					<td class="font-mono">{e.action}</td>
					<td class="max-w-55 truncate font-mono text-ink-2">{e.object}</td>
					<td><span class="badge {badgeCls(badgeOf(e.result === 'ok' ? 'done' : 'failed'))}">{e.result}</span></td>
					<td class="text-ink-2">{e.note}</td>
				</tr>
			{:else}
				<tr><td colspan="7"><div class="p-6 text-center text-[13px] text-ink-muted">暂无审计记录</div></td></tr>
			{/each}
		</tbody>
	</table>
</div>
