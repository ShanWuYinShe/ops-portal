<script lang="ts">
	import { api, type AgentToken } from '$lib/api';
	import { shortTime } from '$lib/state.svelte';
	import { poll } from '$lib/poll';
	import SecretReveal from '$lib/SecretReveal.svelte';
	import { askConfirm, toast } from '$lib/dialogs.svelte';

	// 本页集中所有"机器"凭据的签发与管理：portal-agent 令牌/证书包、
	// portal 出访 service 证书、被管网站 site 注册证书。
	// 人类设备证书与登录绑定在「用户与设备」页，勿在此重复。

	let tokens = $state<AgentToken[]>([]);
	let agentID = $state('');
	let lastPlain = $state('');
	// 一次性注册令牌（enroll_，30 分钟有效）：被管机自助兑换正式 token，
	// 长效 token 永不经人工手（见 docs/portal-agent-protocol.md「注册」）。
	let enrollAgentID = $state('');
	let lastEnroll = $state('');
	let lastEnrollExpires = $state('');

	async function load() {
		try {
			const r = await api<{ tokens: AgentToken[] }>('/api/portal/agent-tokens');
			tokens = r.tokens ?? [];
		} catch {
			/* 静默重试 */
		}
	}

	$effect(() => {
		poll(load, 20000);
	});

	async function issue() {
		if (!agentID) {
			toast('portal_agent_id 必填', 'err');
			return;
		}
		try {
			const r = await api<{ token: string; portal_agent_id: string }>('/api/portal/agent-tokens/issue', {
				method: 'POST',
				body: JSON.stringify({ portal_agent_id: agentID })
			});
			lastPlain = r.token;
			toast(`已为 ${r.portal_agent_id} 签发 token，仅显示一次`);
			agentID = '';
			await load();
		} catch (e) {
			toast('签发失败：' + (e as Error).message, 'err');
		}
	}

	async function issueEnroll() {
		if (!enrollAgentID) {
			toast('portal_agent_id 必填', 'err');
			return;
		}
		try {
			const r = await api<{ enroll_token: string; portal_agent_id: string; expires_at: string }>(
				'/api/portal/enroll-tokens/issue',
				{
					method: 'POST',
					body: JSON.stringify({ portal_agent_id: enrollAgentID })
				}
			);
			lastEnroll = r.enroll_token;
			lastEnrollExpires = r.expires_at;
			toast(`已为 ${r.portal_agent_id} 签发一次性注册令牌，仅显示一次`);
			enrollAgentID = '';
			await load();
		} catch (e) {
			toast('签发失败：' + (e as Error).message, 'err');
		}
	}

	// 机器证书包：机器证书 + 正式 token 一次发放（需服务器配 CF_API_TOKEN）。
	// 推荐流程仍是「注册令牌 + onboard 脚本自注册」；本入口用于补发/调试。
	let bundleAgentID = $state('');
	let bundle = $state<{
		portal_agent_token: string;
		client_cert: string;
		client_key: string;
		serial: string;
		expires_at: string;
	} | null>(null);

	async function issueBundle() {
		if (!bundleAgentID) {
			toast('portal_agent_id 必填', 'err');
			return;
		}
		if (
			!(await askConfirm(
				'签发机器证书包',
				`为 ${bundleAgentID} 签发机器证书 + 正式 token（CF 托管 CA，包内 token 为长效）。确认被管机可信交付？`,
				true
			))
		)
			return;
		try {
			const r = await api<{
				portal_agent_token: string;
				client_cert: string;
				client_key: string;
				serial: string;
				expires_at: string;
			}>('/api/portal/agent-certificates/issue', {
				method: 'POST',
				body: JSON.stringify({ portal_agent_id: bundleAgentID })
			});
			bundle = r;
			toast(`已签发 ${bundleAgentID} 的机器证书包，仅显示一次`, 'ok', 5200);
			bundleAgentID = '';
			await load();
		} catch (e) {
			toast('签发失败：' + (e as Error).message, 'err');
		}
	}

	async function revoke(t: AgentToken) {
		if (
			!(await askConfirm(
				'吊销 Token',
				`吊销 ${t.portal_agent_id} 的 token（${t.id}）？\n该 portal-agent 下次心跳即断连。`,
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

	// --- service 证书（portal 出访被管网站 ops 接口的客户端证书）---
	let svcInfo = $state<{ serial: string; fingerprint: string; expires_at: string } | null>(null);

	async function issueService() {
		if (
			!(await askConfirm(
				'签发 service 证书',
				'已有证书则直接复用（幂等）。新签发后所有被管网站放行清单需同步更新 serial，确认继续？',
				false
			))
		)
			return;
		try {
			const r = await api<{ serial: string; fingerprint: string; expires_at: string }>(
				'/api/portal/service-cert/issue',
				{ method: 'POST', body: '{}' }
			);
			svcInfo = r;
			toast('service 证书就绪：把 serial 钉死到各被管网站放行清单');
		} catch (e) {
			toast('签发失败：' + (e as Error).message, 'err');
		}
	}

	// --- site 注册证书（被管网站自助登记 ops_url 用）---
	let siteName = $state('');
	let siteBundle = $state<{ serial: string; client_cert: string; client_key: string } | null>(null);

	async function issueSite() {
		if (!siteName) {
			toast('site_name 必填（与注册名/证书 label 一致）', 'err');
			return;
		}
		try {
			const r = await api<{ serial: string; client_cert: string; client_key: string }>(
				'/api/portal/site-certificates/issue',
				{ method: 'POST', body: JSON.stringify({ site_name: siteName }) }
			);
			siteBundle = r;
			toast(`已为 ${siteName} 签发注册证书包，仅显示一次`, 'ok', 5200);
			siteName = '';
		} catch (e) {
			toast('签发失败：' + (e as Error).message, 'err');
		}
	}
</script>

<div class="mb-4.5">
	<h1 class="page-title">机器凭据</h1>
	<div class="page-sub">portal-agent 令牌与证书包 · service / site 证书 · portal 只存摘要</div>
</div>

<div class="card">
	<div class="card-head"><h2 class="text-base font-semibold">一次性注册令牌（推荐）</h2></div>
	<div class="card-body">
		<div class="text-[13px] text-ink-2">
			被管机凭此令牌自助兑换正式 token（30 分钟有效、单次使用），配合
			<code class="font-mono">scripts/onboard-portal-agent.sh</code> 一键接入，长效 token 永不经人工手。
		</div>
		<div class="mt-2.5 flex flex-wrap items-end gap-2.5">
			<div class="field">
				<label for="f-enrollAgent">Agent ID</label>
				<input id="f-enrollAgent" class="input" placeholder="如 agent-hotspot-01" bind:value={enrollAgentID}
					onkeydown={(e) => e.key === 'Enter' && issueEnroll()} />
			</div>
			<button class="btn btn-primary" onclick={issueEnroll}>签发注册令牌</button>
		</div>
		{#if lastEnroll}
			<SecretReveal
				headline="请立即复制（{lastEnrollExpires} 前有效，用一次即废）："
				mono={lastEnroll}
			/>
		{/if}
	</div>
</div>

<div class="card mt-4.5">
	<div class="card-head"><h2 class="text-base font-semibold">直接签发长效 token（不推荐）</h2></div>
	<div class="card-body">
		<div class="flex flex-wrap items-end gap-2.5">
			<div class="field">
				<label for="f-agentID">Agent ID</label>
				<input id="f-agentID" class="input" placeholder="如 agent-hotspot-01" bind:value={agentID}
					onkeydown={(e) => e.key === 'Enter' && issue()} />
			</div>
			<button class="btn btn-primary" onclick={issue}>签发</button>
		</div>
		{#if lastPlain}
			<SecretReveal headline="请立即复制（关闭后不再显示）：" mono={lastPlain} />
		{/if}
	</div>
</div>

<div class="card mt-4.5">
	<div class="card-head"><h2 class="text-base font-semibold">机器证书包（补发/调试用）</h2></div>
	<div class="card-body">
		<div class="text-[13px] text-ink-2">
			一次发放机器证书 + 正式 token（需服务器配好 CF_API_TOKEN）。日常接入请用上面的注册令牌流程。
		</div>
		<div class="mt-2.5 flex flex-wrap items-end gap-2.5">
			<div class="field">
				<label for="f-bundleAgent">Agent ID</label>
				<input id="f-bundleAgent" class="input" placeholder="如 agent-hotspot-01" bind:value={bundleAgentID} />
			</div>
			<button class="btn btn-primary" onclick={issueBundle}>签发证书包</button>
		</div>
		{#if bundle}
			<SecretReveal
				headline="证书包仅显示一次（serial {bundle.serial.slice(0, 18)}…，有效期至 {bundle.expires_at.slice(0, 10)}），请立即交付被管机："
				blob={`portal_agent_token: ${bundle.portal_agent_token}\n${bundle.client_cert}${bundle.client_key}`}
			/>
		{/if}
	</div>
</div>

<div class="card mt-4.5">
	<div class="card-head"><h2 class="text-base font-semibold">service 证书（portal 出访凭证）</h2></div>
	<div class="card-body">
		<div class="text-[13px] text-ink-2">
			portal 主动调用被管网站 <code class="font-mono">/ops/v1/*</code>
		 时出示；各被管网站放行清单钉死其 serial。幂等：已签发则直接复用。
		</div>
		<div class="mt-2.5">
			<button class="btn btn-primary" onclick={issueService}>签发 / 查看 service 证书</button>
		</div>
		{#if svcInfo}
			<div class="mt-3 font-mono text-[12.5px]">
				<div>serial: <b>{svcInfo.serial}</b></div>
				<div class="text-ink-2">fingerprint: {svcInfo.fingerprint}</div>
				<div class="text-ink-2">有效期至: {svcInfo.expires_at}</div>
			</div>
		{/if}
	</div>
</div>

<div class="card mt-4.5">
	<div class="card-head"><h2 class="text-base font-semibold">site 注册证书（被管网站自助登记用）</h2></div>
	<div class="card-body">
		<div class="flex flex-wrap items-end gap-2.5">
			<div class="field">
				<label for="f-siteName">Site 名称</label>
				<input id="f-siteName" class="input" placeholder="如 hotspot" bind:value={siteName} />
			</div>
			<button class="btn btn-primary" onclick={issueSite}>签发注册证书包</button>
		</div>
		{#if siteBundle}
			<SecretReveal
				headline="证书包仅显示一次（serial {siteBundle.serial.slice(0, 18)}…），请立即交付被管网站："
				blob={`${siteBundle.client_cert}${siteBundle.client_key}`}
			/>
		{/if}
	</div>
</div>

<div class="card mt-4.5">
	<table class="table">
		<thead>
			<tr><th>ID</th><th>Agent</th><th>类型</th><th>签发人</th><th>签发时间</th><th>状态</th><th></th></tr>
		</thead>
		<tbody>
			{#each tokens as t (t.id)}
				<tr>
					<td class="font-mono">{t.id}</td>
					<td><b>{t.portal_agent_id}</b></td>
					<td><span class="chip">{t.kind === 'enroll' ? '一次性注册' : '正式'}</span></td>
					<td class="text-ink-2">{t.created_by}</td>
					<td class="font-mono text-ink-2">{shortTime(t.created_at)}</td>
					<td>
						<span class="badge {t.revoked || t.used ? '' : 'bg-ok-soft text-ok'}">
							{t.revoked ? '已吊销' : t.used ? '已使用' : '有效'}
						</span>
					</td>
					<td class="text-right">
						{#if !t.revoked && !t.used}
							<button class="btn btn-sm btn-danger" onclick={() => revoke(t)}>吊销</button>
						{/if}
					</td>
				</tr>
			{:else}
				<tr><td colspan="7"><div class="p-6 text-center text-[13px] text-ink-muted">暂无令牌</div></td></tr>
			{/each}
		</tbody>
	</table>
</div>
