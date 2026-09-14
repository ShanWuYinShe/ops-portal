<script lang="ts">
	import { api, type CertRecord } from '$lib/api';
	import { badgeCls, portal, roleLevel, shortTime } from '$lib/state.svelte';
	import { askConfirm, toast } from '$lib/dialogs.svelte';

	let certs = $state<CertRecord[]>([]);
	let showForm = $state(false);
	let form = $state({
		label: '',
		kind: 'human',
		cf_id: '',
		serial: '',
		fingerprint: '',
		common_name: '',
		expires_at: '',
		note: ''
	});

	const isAdmin = $derived(roleLevel(portal.whoami?.role) >= 3);
	const soonExpiring = $derived(
		certs.filter((c) => {
			if (c.status !== 'active' || !c.expires_at) return false;
			return new Date(c.expires_at).getTime() - Date.now() < 30 * 86400_000;
		})
	);

	$effect(() => {
		const load = async () => {
			try {
				const r = await api<{ certs: CertRecord[] }>('/api/portal/certs');
				certs = r.certs ?? [];
			} catch {
				/* 轮询容错 */
			}
		};
		load();
		const t = setInterval(() => {
			if (!document.hidden) load();
		}, 20000);
		return () => clearInterval(t);
	});

	async function register() {
		if (!form.label || !form.serial) {
			toast('label 与 serial 必填（CF 建证后回填）', 'err');
			return;
		}
		try {
			await api('/api/portal/certs/register', {
				method: 'POST',
				body: JSON.stringify(form)
			});
			toast(`已登记 ${form.label}`);
			form = {
				label: '', kind: 'human', cf_id: '', serial: '', fingerprint: '',
				common_name: '', expires_at: '', note: ''
			};
			showForm = false;
		} catch (e) {
			toast('登记失败：' + (e as Error).message, 'err');
		}
	}

	async function revoke(c: CertRecord) {
		if (
			!(await askConfirm(
				'吊销证书',
				`吊销 ${c.label}（serial ${c.serial.slice(0, 16)}…）？\n本地吊销后请同步在 CF 吊销，边缘才真正拒绝。`,
				true
			))
		)
			return;
		try {
			const r = await api<{ cf_sync_required?: boolean }>('/api/portal/certs/revoke', {
				method: 'POST',
				body: JSON.stringify({ id: c.id })
			});
			toast(r.cf_sync_required ? '本地已吊销，请同步吊销 CF 侧证书' : '已吊销', 'ok', 5200);
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

	// --- 用户与证书绑定 ---
	interface PortalUser {
		username: string;
		role: string;
		cert_id: string;
		cert_label: string;
	}
	let users = $state<PortalUser[]>([]);
	let uform = $state({ username: '', role: 'ops.viewer', cert_id: '' });

	async function loadUsers() {
		try {
			const r = await api<{ users: PortalUser[] }>('/api/portal/users');
			users = r.users ?? [];
		} catch {
			/* 静默重试 */
		}
	}

	$effect(() => {
		if (isAdmin) loadUsers();
	});

	async function saveUser() {
		if (!uform.username || !uform.cert_id) {
			toast('用户名与绑定证书必填', 'err');
			return;
		}
		try {
			await api('/api/portal/users/upsert', {
				method: 'POST',
				body: JSON.stringify(uform)
			});
			toast(`已绑定 ${uform.username}`);
			uform = { username: '', role: 'ops.viewer', cert_id: '' };
			await Promise.all([loadUsers()]);
		} catch (e) {
			toast('保存失败：' + (e as Error).message, 'err');
		}
	}

	async function removeUser(u: PortalUser) {
		if (!(await askConfirm('删除用户', `删除 ${u.username} 的登录绑定？`, true))) return;
		try {
			await api('/api/portal/users/remove', {
				method: 'POST',
				body: JSON.stringify({ username: u.username })
			});
			toast('已删除');
			await loadUsers();
		} catch (e) {
			toast('删除失败：' + (e as Error).message, 'err');
		}
	}
</script>

<div class="mb-4.5 flex items-center justify-between">
	<div>
		<h1 class="page-title">设备证书</h1>
		<div class="page-sub">CF 托管 CA 签发的设备证书登记表 · 门禁以本表为准</div>
	</div>
	{#if isAdmin}
		<button class="btn btn-primary" onclick={() => (showForm = !showForm)}>
			{showForm ? '收起' : '登记证书'}
		</button>
	{/if}
</div>

{#if showForm}
	<div class="card mb-4.5">
		<div class="card-head"><h2 class="text-base font-semibold">登记新证书</h2></div>
		<div class="card-body">
			<div class="flex flex-wrap items-end gap-2.5">
				<div class="field">
					<label>Label（持有人-设备）*</label>
					<input class="input" placeholder="liyou-iphone" bind:value={form.label} />
				</div>
				<div class="field w-28 flex-none">
					<label>类型</label>
					<select class="input" bind:value={form.kind}>
						<option value="human">human</option>
						<option value="agent">agent</option>
					</select>
				</div>
				<div class="field">
					<label>Serial Number *</label>
					<input class="input" placeholder="CF 返回的 serial_number" bind:value={form.serial} />
				</div>
			</div>
			<div class="mt-2.5 flex flex-wrap items-end gap-2.5">
				<div class="field">
					<label>CF 证书 ID</label>
					<input class="input" bind:value={form.cf_id} />
				</div>
				<div class="field flex-[2]">
					<label>SHA-256 指纹</label>
					<input class="input" bind:value={form.fingerprint} />
				</div>
				<div class="field">
					<label>有效期至</label>
					<input class="input" placeholder="2028-12-16" bind:value={form.expires_at} />
				</div>
			</div>
			<div class="mt-2.5 flex flex-wrap items-end gap-2.5">
				<div class="field">
					<label>Common Name</label>
					<input class="input" bind:value={form.common_name} />
				</div>
				<div class="field flex-[2]">
					<label>备注</label>
					<input class="input" bind:value={form.note} />
				</div>
				<button class="btn btn-primary" onclick={register}>保存</button>
			</div>
		</div>
	</div>
{/if}

{#if soonExpiring.length}
	<div class="card mb-4.5 border-warn">
		<div class="card-body p-3 text-warn">
			⚠ {soonExpiring.length} 张证书 30 天内到期：{soonExpiring.map((c) => c.label).join('、')}，请及时重签。
		</div>
	</div>
{/if}

<div class="card">
	<table class="table">
		<thead>
			<tr><th>Label</th><th>类型</th><th>Serial</th><th>状态</th><th>有效期至</th><th>登记时间</th><th></th></tr>
		</thead>
		<tbody>
			{#each certs as c (c.id)}
				<tr>
					<td>
						<b>{c.label}</b>
						{#if c.note}<div class="text-xs text-ink-muted">{c.note}</div>{/if}
					</td>
					<td><span class="chip">{c.kind}</span></td>
					<td class="font-mono text-ink-2" title={c.fingerprint}>{c.serial.slice(0, 18)}…</td>
					<td>
						<span class="badge {badgeCls(c.status === 'active' ? 'ok' : '')}">{c.status}</span>
					</td>
					<td class="text-ink-2">{c.expires_at ? c.expires_at.slice(0, 10) : '—'}</td>
					<td class="font-mono text-ink-2">{shortTime(c.created_at)}</td>
					<td class="text-right whitespace-nowrap">
						{#if isAdmin && c.status === 'active'}
							<button class="btn btn-sm btn-danger" onclick={() => revoke(c)}>吊销</button>
						{/if}
					</td>
				</tr>
			{:else}
				<tr><td colspan="7"><div class="p-6 text-center text-[13px] text-ink-muted">暂无证书登记</div></td></tr>
			{/each}
		</tbody>
	</table>
</div>

{#if isAdmin}
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
				<div class="mt-4 rounded-[7px] border border-[#f1d9a1] bg-warn-soft p-3">
					<div class="mb-1.5 font-semibold">证书包仅显示一次（serial {siteBundle.serial.slice(0, 18)}…），请立即交付被管网站：</div>
					<code class="block max-h-40 overflow-auto whitespace-pre-wrap break-all font-mono text-[12px]">{siteBundle.client_cert}{siteBundle.client_key}</code>
				</div>
			{/if}
		</div>
	</div>

	<div class="card mt-4.5">
		<div class="card-head"><h2 class="text-base font-semibold">用户与证书绑定</h2></div>
		<table class="table">
			<thead>
				<tr><th>用户名</th><th>角色</th><th>绑定证书</th><th></th></tr>
			</thead>
			<tbody>
				{#each users as u (u.username)}
					<tr>
						<td><b>{u.username}</b></td>
						<td><span class="chip">{u.role}</span></td>
						<td class="text-ink-2">{u.cert_label || u.cert_id}</td>
						<td class="text-right">
							<button class="btn btn-sm btn-danger" onclick={() => removeUser(u)}>删除</button>
						</td>
					</tr>
				{:else}
					<tr><td colspan="4"><div class="p-6 text-center text-[13px] text-ink-muted">暂无用户绑定（无绑定时持证用户默认 viewer）</div></td></tr>
				{/each}
			</tbody>
		</table>
		<div class="card-body">
			<div class="flex flex-wrap items-end gap-2.5">
				<div class="field">
					<label for="f-uName">用户名</label>
					<input id="f-uName" class="input" placeholder="如 liyou" bind:value={uform.username} />
				</div>
				<div class="field w-32 flex-none">
					<label for="f-uRole">角色</label>
					<select id="f-uRole" class="input" bind:value={uform.role}>
						<option value="ops.viewer">ops.viewer</option>
						<option value="ops.operator">ops.operator</option>
						<option value="ops.admin">ops.admin</option>
					</select>
				</div>
				<div class="field flex-[2]">
					<label for="f-uCert">绑定证书</label>
					<select id="f-uCert" class="input" bind:value={uform.cert_id}>
						<option value="">选择</option>
						{#each certs.filter((c) => c.status === 'active') as c (c.id)}
							<option value={c.id}>{c.label}（{c.kind}）</option>
						{/each}
					</select>
				</div>
				<button class="btn btn-primary" onclick={saveUser}>保存绑定</button>
			</div>
		</div>
	</div>
{/if}
