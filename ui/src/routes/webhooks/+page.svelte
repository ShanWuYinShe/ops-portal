<script lang="ts">
	import {
		DELIVERY_STATUSES,
		asBool,
		attemptBadgeKind,
		bytesText,
		durationText,
		getDelivery,
		getStats,
		inboundUrl,
		listDeliveries,
		listEndpoints,
		listSources,
		removeEndpoint,
		removeSource,
		replayDelivery,
		rotateEndpointSecret,
		statusBadgeKind,
		statusLabel,
		upsertEndpoint,
		upsertSource,
		type WebhookAttempt,
		type WebhookDelivery,
		type WebhookDeliveryDetail,
		type WebhookEndpoint,
		type WebhookSource,
		type WebhookStats
	} from '$lib/webhook';
	import { badgeCls, portal, roleLevel, shortTime } from '$lib/state.svelte';
	import { poll } from '$lib/poll';
	import SecretReveal from '$lib/SecretReveal.svelte';
	import { askConfirm, toast } from '$lib/dialogs.svelte';

	// Webhook 网关管理台：端点（接收）→ 转发规则（出站）→ 投递记录（入站审计）。
	// 全部接口见 $lib/webhook.ts；只读角色可见但不可改（后端仍有 requireRole 兜底）。

	const canAdmin = $derived(roleLevel(portal.whoami?.role) >= 3);
	const canOperate = $derived(roleLevel(portal.whoami?.role) >= 2);
	// 接收地址用当前访问的 origin 拼（部署域名即用户实际能访问到的域名）
	const origin = $derived(typeof location === 'undefined' ? '' : location.origin);

	let endpoints = $state<WebhookEndpoint[]>([]);
	let sources = $state<WebhookSource[]>([]);
	let deliveries = $state<WebhookDelivery[]>([]);
	let stats = $state<WebhookStats | null>(null);
	let loadError = $state('');

	// 筛选条件
	let statusFilter = $state('');
	let endpointFilter = $state('');
	let limit = $state(50);

	// 端点表单：endpointEditId 为空 = 新建
	let endpointEditId = $state('');
	let epName = $state('');
	let epSlug = $state('');
	let epNote = $state('');
	let epEnabled = $state(true);
	let epSecret = $state('');

	// 转发规则表单
	let sourceEditId = $state('');
	let sourceEndpointID = $state('');
	let srcName = $state('');
	let srcURL = $state('');
	let srcEnabled = $state(true);
	let srcMaxAttempts = $state(3);
	let srcTimeoutMs = $state(10000);
	let srcSecret = $state('');

	// 一次性明文（仅轮换后显示一次）
	let rotatedSecret = $state('');
	let rotatedFor = $state('');

	// 投递详情抽屉
	let detail = $state<WebhookDeliveryDetail | null>(null);
	let attempts = $state<WebhookAttempt[]>([]);
	let detailLoading = $state(false);
	let detailOpen = $state(false);

	const endpointById = $derived(new Map(endpoints.map((e) => [e.id, e])));
	const sourceById = $derived(new Map(sources.map((s) => [s.id, s])));

	async function load() {
		try {
			const [eps, dels, st] = await Promise.all([
				listEndpoints(),
				listDeliveries({ limit, status: statusFilter, endpointId: endpointFilter }),
				getStats()
			]);
			endpoints = eps;
			deliveries = dels;
			stats = st;
			loadError = '';
			// 规则列表单独失败（例如某端点刚被删）不应拖垮整页
			sources = await listSources();
		} catch (e) {
			loadError = (e as Error).message;
		}
	}

	$effect(() => {
		poll(load, 10000);
	});

	// 筛选变化立即重取（不必等下一拍轮询）。主路径是下方 select 的 onchange；
	// 该 handler 只读状态、不写状态，因此不会与轮询 effect 互相触发。
	function reloadOnFilterChange() {
		void load();
	}

	function resetEndpointForm() {
		endpointEditId = '';
		epName = '';
		epSlug = '';
		epNote = '';
		epEnabled = true;
		epSecret = '';
	}

	function editEndpoint(e: WebhookEndpoint) {
		endpointEditId = e.id;
		epName = e.name;
		epSlug = e.slug;
		epNote = e.note;
		epEnabled = asBool(e.enabled);
		epSecret = '';
	}

	async function submitEndpoint() {
		if (!epName.trim() || !epSlug.trim()) {
			toast('名称与 slug 均必填', 'err');
			return;
		}
		try {
			await upsertEndpoint({
				...(endpointEditId ? { id: endpointEditId } : {}),
				name: epName.trim(),
				slug: epSlug.trim(),
				// 留空表示不改密钥，因此编辑时不带该字段（后端按省略保留）
				...(epSecret.trim() ? { secret: epSecret.trim() } : {}),
				enabled: epEnabled,
				note: epNote
			});
			toast(endpointEditId ? '端点已更新' : '端点已创建');
			resetEndpointForm();
			await load();
		} catch (e) {
			toast('保存失败：' + (e as Error).message, 'err');
		}
	}

	async function delEndpoint(e: WebhookEndpoint) {
		if (
			!(await askConfirm(
				'删除端点',
				`删除接收端点「${e.name}」（/api/hooks/${e.slug}）？\n该端点的转发规则与投递记录都会一并失效，外部服务回调将 404。`,
				true
			))
		)
			return;
		try {
			await removeEndpoint(e.id);
			toast('已删除');
			if (endpointEditId === e.id) resetEndpointForm();
			if (endpointFilter === e.id) endpointFilter = '';
			if (detail?.endpoint_id === e.id) closeDetail();
			await load();
		} catch (err) {
			toast('删除失败：' + (err as Error).message, 'err');
		}
	}

	async function toggleEndpoint(e: WebhookEndpoint) {
		const next = !asBool(e.enabled);
		try {
			await upsertEndpoint({
				id: e.id,
				name: e.name,
				slug: e.slug,
				enabled: next,
				note: e.note
			});
			toast(next ? `已启用 ${e.slug}` : `已停用 ${e.slug}（回调一律拒绝）`);
			await load();
		} catch (err) {
			toast('操作失败：' + (err as Error).message, 'err');
		}
	}

	async function rotate(e: WebhookEndpoint) {
		if (
			!(await askConfirm(
				'轮换入站密钥',
				`为「${e.name}」生成新密钥？\n旧密钥立即失效，外部服务需同步换成新值，否则签名校验会拒绝。`,
				true
			))
		)
			return;
		try {
			rotatedSecret = await rotateEndpointSecret(e.id);
			rotatedFor = e.slug;
			toast('密钥已轮换，请立即复制', 'ok', 5200);
			await load();
		} catch (err) {
			toast('轮换失败：' + (err as Error).message, 'err');
		}
	}

	function resetSourceForm(endpointId = '') {
		sourceEditId = '';
		sourceEndpointID = endpointId;
		srcName = '';
		srcURL = '';
		srcEnabled = true;
		srcMaxAttempts = 3;
		srcTimeoutMs = 10000;
		srcSecret = '';
	}

	function editSource(s: WebhookSource) {
		sourceEditId = s.id;
		sourceEndpointID = s.endpoint_id;
		srcName = s.name;
		srcURL = s.target_url;
		srcEnabled = asBool(s.enabled);
		srcMaxAttempts = s.max_attempts;
		srcTimeoutMs = s.timeout_ms;
		srcSecret = '';
	}

	async function submitSource() {
		if (!sourceEndpointID) {
			toast('请先选择所属端点', 'err');
			return;
		}
		if (!srcName.trim() || !srcURL.trim()) {
			toast('规则名与目标地址均必填', 'err');
			return;
		}
		if (!/^https?:\/\//.test(srcURL.trim())) {
			toast('目标地址须以 http:// 或 https:// 开头', 'err');
			return;
		}
		if (srcMaxAttempts < 1 || srcTimeoutMs < 1) {
			toast('最大尝试次数与超时都须大于 0', 'err');
			return;
		}
		try {
			await upsertSource({
				...(sourceEditId ? { id: sourceEditId } : {}),
				endpoint_id: sourceEndpointID,
				name: srcName.trim(),
				target_url: srcURL.trim(),
				...(srcSecret.trim() ? { secret: srcSecret.trim() } : {}),
				enabled: srcEnabled,
				max_attempts: srcMaxAttempts,
				timeout_ms: srcTimeoutMs
			});
			toast(sourceEditId ? '规则已更新' : '规则已创建');
			resetSourceForm(sourceEndpointID);
			await load();
		} catch (e) {
			toast('保存失败：' + (e as Error).message, 'err');
		}
	}

	async function delSource(s: WebhookSource) {
		if (
			!(await askConfirm('删除转发规则', `删除「${s.name}」→ ${s.target_url}？\n该规则不再接收后续转发。`, true))
		)
			return;
		try {
			await removeSource(s.id);
			toast('已删除');
			if (sourceEditId === s.id) resetSourceForm();
			await load();
		} catch (e) {
			toast('删除失败：' + (e as Error).message, 'err');
		}
	}

	async function toggleSource(s: WebhookSource) {
		const next = !asBool(s.enabled);
		try {
			await upsertSource({
				id: s.id,
				endpoint_id: s.endpoint_id,
				name: s.name,
				target_url: s.target_url,
				enabled: next,
				max_attempts: s.max_attempts,
				timeout_ms: s.timeout_ms
			});
			toast(next ? '规则已启用' : '规则已停用');
			await load();
		} catch (e) {
			toast('操作失败：' + (e as Error).message, 'err');
		}
	}

	async function openDetail(d: WebhookDelivery) {
		detailOpen = true;
		detailLoading = true;
		detail = null;
		attempts = [];
		try {
			const r = await getDelivery(d.id);
			detail = r.delivery;
			attempts = r.attempts;
		} catch (e) {
			toast('详情加载失败：' + (e as Error).message, 'err');
			detailOpen = false;
		} finally {
			detailLoading = false;
		}
	}

	function closeDetail() {
		detailOpen = false;
		detail = null;
		attempts = [];
	}

	async function replay(d: WebhookDelivery) {
		if (
			!(await askConfirm(
				'重放投递',
				`把投递 ${d.id}（${statusLabel(d.status)}）重新排入转发队列？\n会按当前启用的规则再投一次。`,
				false
			))
		)
			return;
		try {
			await replayDelivery(d.id);
			toast('已排入重放队列');
			if (detailOpen && detail?.id === d.id) detail = { ...detail, status: 'pending' };
			await load();
		} catch (e) {
			toast('重放失败：' + (e as Error).message, 'err');
		}
	}

	async function copyText(text: string, what = '内容') {
		try {
			await navigator.clipboard.writeText(text);
			toast(`已复制${what}`);
		} catch {
			toast('复制失败，请手动选择复制', 'err');
		}
	}
</script>
<div class="mb-4.5">
	<h1 class="page-title">Webhook 网关</h1>
	<div class="page-sub">
		接第三方回调（GitHub/Stripe/CI）→ 验签落库 → 转发到内部目标 → 失败重试 → 全量可查可重放。
	</div>
</div>

{#if loadError}
	<div class="card mb-4.5 border-[#f1d9a1] bg-warn-soft">
		<div class="card-body text-[13px]">加载失败：{loadError}（持续失败请检查后端 /api/portal/webhooks/* 是否就绪）</div>
	</div>
{/if}

<div class="card">
	<div class="card-body grid grid-cols-3 gap-3 max-md:grid-cols-2">
		{#each [
			{ k: '入站总数', v: stats?.total },
			{ k: '待转发', v: stats?.pending },
			{ k: '已送达', v: stats?.delivered },
			{ k: '转发失败', v: stats?.failed },
			{ k: '已拒绝', v: stats?.rejected },
			{ k: '端点 / 规则', v: `${stats?.endpoints ?? 0} / ${stats?.sources ?? 0}` }
		] as s (s.k)}
			<div>
				<div class="text-xs text-ink-muted">{s.k}</div>
				<div class="mt-0.5 text-[19px] font-semibold">{s.v ?? '—'}</div>
			</div>
		{/each}
	</div>
</div>

{#if rotatedSecret}
	<div class="card mt-4.5">
		<div class="card-body">
			<SecretReveal
				headline="新密钥仅显示这一次（端点 {rotatedFor}）——请立即填入回调方的签名配置："
				mono={rotatedSecret}
			/>
			<div class="mt-2.5 flex justify-end">
				<button class="btn btn-sm" onclick={() => (rotatedSecret = '')}>我已保存，关闭</button>
			</div>
		</div>
	</div>
{/if}

<div class="card mt-4.5">
	<div class="card-head">
		<h2 class="text-base font-semibold">接收端点</h2>
		<span class="text-xs text-ink-muted">外部服务 POST 到 /api/hooks/&lt;slug&gt;，按端点密钥验签</span>
	</div>
	<table class="table">
		<thead>
			<tr><th>名称</th><th>接收地址</th><th>密钥</th><th>状态</th><th>创建</th><th></th></tr>
		</thead>
		<tbody>
			{#each endpoints as e (e.id)}
				<tr>
					<td>
						<b>{e.name}</b>
						{#if e.note}
							<div class="text-xs text-ink-muted">{e.note}</div>
						{/if}
					</td>
					<td>
						<div class="flex items-center gap-2">
							<code class="max-w-70 truncate font-mono text-[12.5px]"
								>{inboundUrl(origin, e.slug)}</code
							>
							<button class="btn btn-sm" onclick={() => copyText(inboundUrl(origin, e.slug), '接收地址')}>
								复制
							</button>
						</div>
						<div class="mt-0.5 font-mono text-xs text-ink-muted">slug: {e.slug}</div>
					</td>
					<td class="font-mono text-[12.5px] text-ink-2">{e.secret || '未设置'}</td>
					<td>
						<span class="badge {asBool(e.enabled) ? 'bg-ok-soft text-ok' : ''}">
							{asBool(e.enabled) ? '启用' : '停用'}
						</span>
					</td>
					<td class="text-ink-2">
						<div class="font-mono">{shortTime(e.created_at)}</div>
						{#if e.created_by}<div class="text-xs">{e.created_by}</div>{/if}
					</td>
					<td class="text-right whitespace-nowrap">
						{#if canAdmin}
							<button class="btn btn-sm" onclick={() => toggleEndpoint(e)}>
								{asBool(e.enabled) ? '停用' : '启用'}
							</button>
							<button class="btn btn-sm" onclick={() => editEndpoint(e)}>编辑</button>
							<button class="btn btn-sm" onclick={() => rotate(e)}>轮换密钥</button>
							<button class="btn btn-sm" onclick={() => resetSourceForm(e.id)}>加规则</button>
							<button class="btn btn-sm btn-danger" onclick={() => delEndpoint(e)}>删除</button>
						{/if}
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="6"
						><div class="p-6 text-center text-[13px] text-ink-muted">暂无端点，先在下方创建一个</div></td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

{#if canAdmin}
	<div class="card mt-4.5">
		<div class="card-head">
			<h2 class="text-base font-semibold">{endpointEditId ? '编辑端点' : '新建端点'}</h2>
			{#if endpointEditId}
				<button class="btn btn-sm" onclick={resetEndpointForm}>取消编辑</button>
			{/if}
		</div>
		<div class="card-body">
			<div class="flex flex-wrap items-end gap-2.5">
				<div class="field">
					<label for="f-epName">名称</label>
					<input id="f-epName" class="input" placeholder="如 GitHub 主仓" bind:value={epName} />
				</div>
				<div class="field">
					<label for="f-epSlug">slug（接收路径，全局唯一）</label>
					<input id="f-epSlug" class="input" placeholder="如 github-main" bind:value={epSlug} />
				</div>
				<div class="field">
					<label for="f-epSecret">密钥{endpointEditId ? '（留空 = 不修改）' : '（留空 = 不验签，仅受信代理可调用）'}</label>
					<input id="f-epSecret" class="input" placeholder="HMAC secret" bind:value={epSecret} />
				</div>
				<div class="field">
					<label for="f-epNote">备注</label>
					<input id="f-epNote" class="input" placeholder="用途说明" bind:value={epNote} />
				</div>
				<label class="flex cursor-pointer items-center gap-1.5 pb-2 text-[13px]">
					<input type="checkbox" class="accent-primary" bind:checked={epEnabled} />
					启用
				</label>
				<button class="btn btn-primary" onclick={submitEndpoint}>
					{endpointEditId ? '保存修改' : '创建端点'}
				</button>
			</div>
			{#if epSlug}
				<code class="mt-2.5 block font-mono text-[12.5px] text-ink-2"
					>接收地址：{inboundUrl(origin, epSlug)}</code
				>
			{/if}
		</div>
	</div>
{/if}

<div class="card mt-4.5">
	<div class="card-head">
		<h2 class="text-base font-semibold">转发规则</h2>
		<span class="text-xs text-ink-muted">按端点的启用规则顺序转发；失败按 5s/30s/2m/10m/30m 退避重试</span>
	</div>
	<table class="table">
		<thead>
			<tr><th>端点</th><th>规则</th><th>目标地址</th><th>重试</th><th>状态</th><th></th></tr>
		</thead>
		<tbody>
			{#each sources as s (s.id)}
				<tr>
					<td class="font-mono text-ink-2">
						{endpointById.get(s.endpoint_id)?.name ?? s.endpoint_id}
					</td>
					<td>
						<b>{s.name}</b>
						<div class="font-mono text-xs text-ink-muted">出站密钥 {s.secret || '未设置'}</div>
					</td>
					<td class="max-w-70 truncate font-mono text-[12.5px]" title={s.target_url}>{s.target_url}</td>
					<td class="text-ink-2">{s.max_attempts} 次 / {s.timeout_ms} ms</td>
					<td>
						<span class="badge {asBool(s.enabled) ? 'bg-ok-soft text-ok' : ''}">
							{asBool(s.enabled) ? '启用' : '停用'}
						</span>
					</td>
					<td class="text-right whitespace-nowrap">
						{#if canAdmin}
							<button class="btn btn-sm" onclick={() => toggleSource(s)}>
								{asBool(s.enabled) ? '停用' : '启用'}
							</button>
							<button class="btn btn-sm" onclick={() => editSource(s)}>编辑</button>
							<button class="btn btn-sm btn-danger" onclick={() => delSource(s)}>删除</button>
						{/if}
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="6"
						><div class="p-6 text-center text-[13px] text-ink-muted">暂无转发规则</div></td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

{#if canAdmin}
	<div class="card mt-4.5">
		<div class="card-head">
			<h2 class="text-base font-semibold">{sourceEditId ? '编辑转发规则' : '新建转发规则'}</h2>
			{#if sourceEditId}
				<button class="btn btn-sm" onclick={() => resetSourceForm()}>取消编辑</button>
			{/if}
		</div>
		<div class="card-body">
			<div class="flex flex-wrap items-end gap-2.5">
				<div class="field">
					<label for="f-srcEndpoint">所属端点</label>
					<select id="f-srcEndpoint" class="input" bind:value={sourceEndpointID}>
						<option value="">选择</option>
						{#each endpoints as e (e.id)}
							<option value={e.id}>{e.name}（{e.slug}）</option>
						{/each}
					</select>
				</div>
				<div class="field">
					<label for="f-srcName">规则名</label>
					<input id="f-srcName" class="input" placeholder="如 转发到 CI" bind:value={srcName} />
				</div>
				<div class="field">
					<label for="f-srcURL">目标地址</label>
					<input id="f-srcURL" class="input" placeholder="https://internal.example/hook" bind:value={srcURL} />
				</div>
				<div class="field">
					<label for="f-srcSecret">出站密钥{sourceEditId ? '（留空 = 不修改）' : ''}</label>
					<input id="f-srcSecret" class="input" placeholder="可选" bind:value={srcSecret} />
				</div>
				<div class="field">
					<label for="f-srcAttempts">最大尝试次数</label>
					<input id="f-srcAttempts" class="input" type="number" min="1" bind:value={srcMaxAttempts} />
				</div>
				<div class="field">
					<label for="f-srcTimeout">超时（毫秒）</label>
					<input id="f-srcTimeout" class="input" type="number" min="1" bind:value={srcTimeoutMs} />
				</div>
				<label class="flex cursor-pointer items-center gap-1.5 pb-2 text-[13px]">
					<input type="checkbox" class="accent-primary" bind:checked={srcEnabled} />
					启用
				</label>
				<button class="btn btn-primary" onclick={submitSource}>
					{sourceEditId ? '保存修改' : '创建规则'}
				</button>
			</div>
		</div>
	</div>
{/if}

<div class="card mt-4.5">
	<div class="card-head">
		<h2 class="text-base font-semibold">投递记录</h2>
		<div class="flex items-center gap-2.5">
			<select class="input w-45" bind:value={endpointFilter} onchange={reloadOnFilterChange}>
				<option value="">全部端点</option>
				{#each endpoints as e (e.id)}
					<option value={e.id}>{e.name}</option>
				{/each}
			</select>
			<select class="input w-32" bind:value={statusFilter} onchange={reloadOnFilterChange}>
				<option value="">全部状态</option>
				{#each DELIVERY_STATUSES as s (s)}
					<option value={s}>{statusLabel(s)}</option>
				{/each}
			</select>
			<select class="input w-28" bind:value={limit} onchange={reloadOnFilterChange}>
				<option value={50}>最近 50</option>
				<option value={100}>最近 100</option>
				<option value={200}>最近 200</option>
			</select>
		</div>
	</div>
	<table class="table">
		<thead>
			<tr>
				<th>时间</th><th>端点</th><th>事件</th><th>来源 IP</th><th>体量</th>
				<th>验签</th><th>状态</th><th>说明</th><th></th>
			</tr>
		</thead>
		<tbody>
			{#each deliveries as d (d.id)}
				<tr>
					<td class="font-mono text-ink-2">{shortTime(d.received_at)}</td>
					<td class="font-mono">
						{endpointById.get(d.endpoint_id)?.slug ?? d.slug}
						<div class="text-xs text-ink-muted">{d.method}</div>
					</td>
					<td class="text-ink-2">{d.event_type || '—'}</td>
					<td class="font-mono text-xs text-ink-2">{d.remote_ip || '—'}</td>
					<td class="text-ink-2" title="{d.body_bytes} 字节">{bytesText(d.body_bytes)}</td>
					<td>
						<span class="badge {asBool(d.sig_ok) ? 'bg-ok-soft text-ok' : 'bg-err-soft text-err'}">
							{asBool(d.sig_ok) ? '通过' : '未通过'}
						</span>
					</td>
					<td>
						<span class="badge {badgeCls(statusBadgeKind(d.status))}">{statusLabel(d.status)}</span>
					</td>
					<td class="max-w-55 truncate text-ink-2" title={d.reject_reason}>{d.reject_reason || '—'}</td>
					<td class="text-right whitespace-nowrap">
						<button class="btn btn-sm" onclick={() => openDetail(d)}>详情</button>
						{#if canOperate}
							<button class="btn btn-sm" onclick={() => replay(d)}>重放</button>
						{/if}
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="9"
						><div class="p-6 text-center text-[13px] text-ink-muted">暂无投递记录</div></td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

{#if detailOpen}
	<!-- 背景点击关闭由 render effect 派发（无障碍：抽屉内有关闭按钮） -->
	<div
		class="fixed inset-0 z-40 bg-black/30"
		onclick={closeDetail}
		onkeydown={(e) => e.key === 'Escape' && closeDetail()}
		role="presentation"
	></div>
	<aside class="fixed top-0 right-0 z-50 h-full w-[720px] max-w-full overflow-y-auto bg-white shadow-xl">
		<div class="sticky top-0 flex items-center justify-between border-b border-line bg-white px-4 py-3">
			<h2 class="text-base font-semibold">
				投递详情{#if detail}<span class="ml-2 font-mono text-xs text-ink-muted">{detail.id}</span>{/if}
			</h2>
			<div class="flex items-center gap-2">
				{#if canOperate && detail}
					{@const d = detail}
					<button class="btn btn-sm" onclick={() => replay(d)}>重放</button>
				{/if}
				<button class="btn btn-sm" onclick={closeDetail}>关闭</button>
			</div>
		</div>

		{#if detailLoading}
			<div class="p-6 text-[13px] text-ink-muted">加载中…</div>
		{:else if detail}
			<div class="card-body">
				<div class="grid grid-cols-2 gap-x-4 gap-y-2 text-[13px] max-md:grid-cols-1">
					<div><span class="text-ink-muted">状态：</span>{statusLabel(detail.status)}</div>
					<div><span class="text-ink-muted">验签：</span>{asBool(detail.sig_ok) ? '通过' : '未通过'}</div>
					<div><span class="text-ink-muted">端点：</span>{detail.slug}</div>
					<div><span class="text-ink-muted">事件：</span>{detail.event_type || '—'}</div>
					<div><span class="text-ink-muted">来源 IP：</span>{detail.remote_ip || '—'}</div>
					<div><span class="text-ink-muted">方法：</span>{detail.method}</div>
					<div><span class="text-ink-muted">Content-Type：</span>{detail.content_type || '—'}</div>
					<div><span class="text-ink-muted">体量：</span>{bytesText(detail.body_bytes)}</div>
					<div><span class="text-ink-muted">收到时间：</span>{detail.received_at || '—'}</div>
					<div><span class="text-ink-muted">去重键：</span>{detail.dedup_key || '—'}</div>
				</div>
				{#if detail.reject_reason}
					<div class="mt-3 rounded-[7px] bg-err-soft p-2.5 text-[13px]">{detail.reject_reason}</div>
				{/if}

				<div class="mt-4">
					<div class="mb-1.5 flex items-center justify-between">
						<span class="text-xs font-medium text-ink-2">请求头</span>
						<button
							class="btn btn-sm"
							onclick={() => copyText(JSON.stringify(detail?.headers ?? {}, null, 2), '请求头')}>复制</button
						>
					</div>
					<pre
						class="max-h-40 overflow-auto rounded-[7px] bg-[#fafbfd] p-2.5 font-mono text-[12px]">{JSON.stringify(
							detail.headers,
							null,
							2
						)}</pre>
				</div>

				<div class="mt-4">
					<div class="mb-1.5 flex items-center justify-between">
						<span class="text-xs font-medium text-ink-2">请求体（原文）</span>
						<button class="btn btn-sm" onclick={() => copyText(detail?.body ?? '', '请求体')}>复制</button>
					</div>
					<pre
						class="max-h-70 overflow-auto rounded-[7px] bg-[#fafbfd] p-2.5 font-mono text-[12px] whitespace-pre-wrap break-all">{detail.body ||
							'（空）'}</pre>
				</div>

				<div class="mt-4">
					<div class="mb-1.5 text-xs font-medium text-ink-2">转发尝试（{attempts.length}）</div>
					<table class="table">
						<thead>
							<tr><th>#</th><th>规则</th><th>状态</th><th>HTTP</th><th>耗时</th><th>时间</th><th>错误</th></tr>
						</thead>
						<tbody>
							{#each attempts as a (a.id)}
								<tr>
									<td class="font-mono">{a.attempt_no}</td>
									<td class="text-ink-2">{sourceById.get(a.source_id)?.name ?? a.source_id}</td>
									<td>
										<span class="badge {badgeCls(attemptBadgeKind(a.status))}">{a.status || '—'}</span>
									</td>
									<td class="font-mono text-ink-2">{a.http_status || '—'}</td>
									<td class="text-ink-2">{durationText(a.duration_ms)}</td>
									<td class="font-mono text-xs text-ink-2">{shortTime(a.at)}</td>
									<td class="max-w-55 text-ink-2" title="{a.error}
{a.response_body}">
										{a.error || a.response_body || '—'}
									</td>
								</tr>
							{:else}
								<tr
									><td colspan="7"
										><div class="p-4 text-center text-[13px] text-ink-muted">尚无转发尝试</div></td
									></tr
								>
							{/each}
						</tbody>
					</table>
				</div>
			</div>
		{/if}
	</aside>
{/if}