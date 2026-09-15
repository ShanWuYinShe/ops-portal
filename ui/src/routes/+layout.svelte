<script lang="ts">
	import '../app.css';
	import { page } from '$app/state';
	import {
		LayoutDashboard,
		Server,
		TerminalSquare,
		Users,
		KeyRound,
		History
	} from '@lucide/svelte';
	import Overlays from '$lib/Overlays.svelte';
	import { loadPortal, portal, roleLevel } from '$lib/state.svelte';
	import { toast } from '$lib/dialogs.svelte';

	let { children } = $props();

	const NAV = [
		{ group: '概览' },
		{ href: '/dashboard', label: '仪表盘', icon: LayoutDashboard },
		{ group: '站点管理' },
		{ href: '/sites', label: '站点与服务', icon: Server },
		{ href: '/ops', label: '命令与发布', icon: TerminalSquare },
		{ group: '访问控制', adminOnly: true },
		{ href: '/certs', label: '用户与设备', icon: Users, adminOnly: true },
		{ href: '/tokens', label: '机器凭据', icon: KeyRound, adminOnly: true },
		{ href: '/audit', label: '审计日志', icon: History, adminOnly: true }
	];

	$effect(() => {
		loadPortal();
		const t = setInterval(() => {
			if (!document.hidden) loadPortal();
		}, 15000);
		return () => clearInterval(t);
	});

	$effect(() => {
		if (portal.loadError) toast('加载失败：' + portal.loadError, 'err');
	});

	function visible(item: (typeof NAV)[number]) {
		if (item.adminOnly && roleLevel(portal.whoami?.role) < 3) return false;
		return true;
	}
</script>

<svelte:head>
	<title>Ops Portal · 统一管理站</title>
</svelte:head>

<div class="flex min-h-screen">
	<aside
		class="sticky top-0 flex h-screen w-54 flex-none flex-col overflow-x-auto bg-sidebar text-sidebar-text max-md:static max-md:h-auto max-md:w-full max-md:flex-row"
	>
		<div class="flex items-center gap-2.5 px-4.5 pt-5 pb-4 text-white max-md:py-3">
			<div
				class="grid h-[30px] w-[30px] flex-none place-items-center rounded-lg bg-gradient-to-br from-primary to-purple-600 text-sm font-bold"
			>
				OP
			</div>
			<div>
				<b class="text-[15px]">Ops Portal</b>
				<small class="block text-[11px] font-normal">ops.yinghuoren.com</small>
			</div>
		</div>
		<nav class="flex flex-1 flex-col gap-0.5 px-2.5 py-1.5 max-md:flex-row">
			{#each NAV as item (item.href ?? item.group)}
				{#if visible(item)}
					{#if item.group}
						<div class="px-2.5 pt-3.5 pb-1 text-[11px] tracking-wider text-slate-500 max-md:hidden">
							{item.group}
						</div>
					{:else}
						<a
							href={item.href}
							class="flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13.5px] hover:bg-sidebar-active hover:text-white
								{page.url.pathname === item.href ? 'bg-primary font-medium text-white' : ''}"
						>
							<item.icon class="h-4 w-4 flex-none" />
							{item.label}
						</a>
					{/if}
				{/if}
			{/each}
		</nav>
		<!-- 构建版本标识：部署时注入 git SHA，用于核对线上实际运行的构建 -->
		<div class="px-4.5 pb-2.5 text-[10.5px] text-slate-600 select-all max-md:hidden">build {__APP_VERSION__}</div>
		{#if portal.whoami}
			<div class="m-3 rounded-[10px] bg-sidebar-active p-3 max-md:hidden">
				<b class="block text-[13px] text-white">{portal.whoami.username}</b>
				<span class="text-[11.5px]">
					{portal.whoami.role}{portal.whoami.dev_no_cert ? ' · dev' : ''}
				</span>
			</div>
		{/if}
	</aside>

	<main class="min-w-0 max-w-[1240px] flex-1 px-7 pt-5.5 pb-12 max-md:px-4">
		{@render children()}
	</main>
</div>

<Overlays />
