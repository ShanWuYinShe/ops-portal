<script lang="ts">
	import { api, type CertRecord } from '$lib/api';
	import { badgeCls, portal, roleLevel, shortTime } from '$lib/state.svelte';
	import { poll } from '$lib/poll';
	import { askConfirm, toast } from '$lib/dialogs.svelte';

	// 本页只管"人"的凭据：设备证书登记表 + 用户登录绑定。
	// 机器凭据（portal-agent 令牌 / 证书包 / service / site 证书）在 /tokens。

	let certs = $state<CertRecord[]>([]);
	let showForm = $state(false);
	// 行内改名态（label 仅显示用；portal-agent 证书 label=机器身份，后端拒绝）
	let renaming = $state<{ id: string; value: string } | null>(null);
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
	// 角色的展示名：角色值本身是机器格式（ops.*），界面配中文权限说明
	const roleLabel = (role: string) =>
		({ 'ops.viewer': 'ops.viewer · 只读', 'ops.operator': 'ops.operator · 可操作', 'ops.admin': 'ops.admin · 管理员' })[role] ?? role;
	const soonExpiring = $derived(
		certs.filter((c) => {
			if (c.status !== 'active' || !c.expires_at) return false;
			return new Date(c.expires_at).getTime() - Date.now() < 30 * 86400_000;
		})
	);

	async function load() {
		try {
			const r = await api<{ certs: CertRecord[] }>('/api/portal/certs');
			certs = r.certs ?? [];
		} catch {
			/* 轮询容错 */
		}
	}

	$effect(() => {
		poll(load, 20000);
	});

	async function saveRename(c: CertRecord) {
		if (!renaming || renaming.id !== c.id) return;
		const label = renaming.value.trim();
		if (!label || label === c.label) {
			renaming = null;
			return;
		}
		try {
			await api('/api/portal/certs/rename', {
				method: 'POST',
				body: JSON.stringify({ id: c.id, label })
			});
			toast(`已改名：${c.label} → ${label}`);
			renaming = null;
			await load();
		} catch (e) {
			toast('改名失败：' + (e as Error).message, 'err');
		}
	}

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
			await loadUsers();
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
		<h1 class="page-title">用户与设备</h1>
		<div class="page-sub">管理「谁能访问管理站」：每台设备一张客户端证书，用户名绑定证书后即以该身份登录</div>
	</div>
	{#if isAdmin}
		<button class="btn btn-primary" onclick={() => (showForm = !showForm)}>
			{showForm ? '收起' : '登记设备证书'}
		</button>
	{/if}
</div>

{#if showForm}
	<div class="card mb-4.5">
		<div class="card-head"><h2 class="text-base font-semibold">登记新设备证书</h2></div>
		<div class="card-body">
			<div class="text-[13px] text-ink-2">
				流程：在 Cloudflare 给新设备签发一张客户端证书并装入设备 → 把 CF 返回的
				Serial 等信息登记到下面，门禁即放行该设备。带 * 为必填。
			</div>
			<div class="mt-2.5 flex flex-wrap items-end gap-2.5">
				<div class="field">
					<label>名称（持有人-设备）*</label>
					<input class="input" placeholder="liyou-iphone" bind:value={form.label} />
				</div>
				<div class="field w-40 flex-none">
					<label>用途</label>
					<select class="input" bind:value={form.kind}>
						<option value="human">人用（登录管理站）</option>
						<option value="portal-agent">机器用（portal-agent）</option>
					</select>
				</div>
				<div class="field">
					<label>证书序列号 Serial *</label>
					<input class="input" placeholder="CF 建证返回的 serial_number" bind:value={form.serial} />
				</div>
			</div>
			<div class="mt-2.5 flex flex-wrap items-end gap-2.5">
				<div class="field">
					<label>CF 证书 ID（选填）</label>
					<input class="input" bind:value={form.cf_id} />
				</div>
				<div class="field flex-[2]">
					<label>SHA-256 指纹（选填）</label>
					<input class="input" bind:value={form.fingerprint} />
				</div>
				<div class="field">
					<label>有效期至</label>
					<input class="input" placeholder="2028-12-16" bind:value={form.expires_at} />
				</div>
			</div>
			<div class="mt-2.5 flex flex-wrap items-end gap-2.5">
				<div class="field">
					<label>Common Name（选填）</label>
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
	<div class="card-head">
		<h2 class="text-base font-semibold">设备证书（登录门禁名册）</h2>
		<span class="text-xs text-ink-muted">在此名单内的设备才能打开管理站；吊销即拒绝访问</span>
	</div>
	<table class="table">
		<thead>
			<tr><th>名称</th><th>用途</th><th>序列号 Serial</th><th>状态</th><th>有效期至</th><th>登记时间</th><th></th></tr>
		</thead>
		<tbody>
				{#each certs as c (c.id)}
					<tr>
						<td>
							{#if renaming?.id === c.id}
								<div class="flex items-center gap-1.5">
									<input
										class="input h-7 w-44 text-[13px]"
										bind:value={renaming.value}
										onkeydown={(e) => {
											if (e.key === 'Enter') saveRename(c);
											if (e.key === 'Escape') renaming = null;
										}}
									/>
									<button class="btn btn-sm" onclick={() => saveRename(c)}>保存</button>
									<button class="btn btn-sm" onclick={() => (renaming = null)}>取消</button>
								</div>
							{:else}
								<b>{c.label}</b>
								{#if isAdmin && c.kind !== 'portal-agent'}
									<button
										class="btn btn-sm ml-1.5"
										title="改显示名（不影响认证；portal-agent 证书的 label 即机器身份，不可改）"
										onclick={() => (renaming = { id: c.id, value: c.label })}
									>
										改名
									</button>
								{/if}
								{#if c.note}<div class="text-xs text-ink-muted">{c.note}</div>{/if}
							{/if}
						</td>
					<td><span class="chip">{c.kind === 'human' ? '人用' : '机器用'}</span></td>
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
		<div class="card-head">
			<h2 class="text-base font-semibold">用户与角色绑定</h2>
			<span class="text-xs text-ink-muted">把用户名绑到一张设备证书：持该证书访问，即以此用户身份和角色登录</span>
		</div>
		<table class="table">
			<thead>
				<tr><th>用户名</th><th>角色（权限）</th><th>绑定的设备证书</th><th></th></tr>
			</thead>
			<tbody>
				{#each users as u (u.username)}
					<tr>
						<td><b>{u.username}</b></td>
						<td><span class="chip">{roleLabel(u.role)}</span></td>
						<td class="text-ink-2">{u.cert_label || u.cert_id}</td>
						<td class="text-right">
							<button class="btn btn-sm btn-danger" onclick={() => removeUser(u)}>删除</button>
						</td>
					</tr>
				{:else}
					<tr><td colspan="4"><div class="p-6 text-center text-[13px] text-ink-muted">暂无绑定；未绑定时持证访问按只读（viewer）处理</div></td></tr>
				{/each}
			</tbody>
		</table>
		<div class="card-body">
			<div class="flex flex-wrap items-end gap-2.5">
				<div class="field">
					<label for="f-uName">用户名</label>
					<input id="f-uName" class="input" placeholder="如 liyou" bind:value={uform.username} />
				</div>
				<div class="field w-52 flex-none">
					<label for="f-uRole">角色（权限）</label>
					<select id="f-uRole" class="input" bind:value={uform.role}>
						<option value="ops.viewer">ops.viewer · 只读查看</option>
						<option value="ops.operator">ops.operator · 可操作站点</option>
						<option value="ops.admin">ops.admin · 管理员</option>
					</select>
				</div>
				<div class="field flex-[2]">
					<label for="f-uCert">绑定的设备证书</label>
					<select id="f-uCert" class="input" bind:value={uform.cert_id}>
						<option value="">选择</option>
						{#each certs.filter((c) => c.status === 'active') as c (c.id)}
							<option value={c.id}>{c.label}（{c.kind === 'human' ? '人用' : '机器用'}）</option>
						{/each}
					</select>
				</div>
				<button class="btn btn-primary" onclick={saveUser}>保存绑定</button>
			</div>
		</div>
	</div>
{/if}
