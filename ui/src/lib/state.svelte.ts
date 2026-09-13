// 全局共享状态（Svelte 5 runes）：whoami / agents / sites，由 layout 轮询刷新。

import { api, type Agent, type SiteOverview, type Whoami } from '$lib/api';

export const portal = $state({
	whoami: null as Whoami | null,
	agents: [] as Agent[],
	sites: [] as SiteOverview[],
	loadError: ''
});

export async function loadPortal() {
	try {
		portal.whoami = await api<Whoami>('/api/portal/whoami');
		const [a, s] = await Promise.all([
			api<{ agents: Agent[] }>('/api/portal/agents'),
			api<{ sites: SiteOverview[] }>('/api/portal/sites')
		]);
		portal.agents = a.agents ?? [];
		portal.sites = s.sites ?? [];
		portal.loadError = '';
	} catch (e) {
		portal.loadError = (e as Error).message;
	}
}

export function roleLevel(role: string | undefined): number {
	return { 'ops.admin': 3, 'ops.operator': 2, 'ops.viewer': 1 }[role ?? ''] ?? 0;
}

export function agentOnline(lastSeen: string): boolean {
	return !!lastSeen && Date.now() - new Date(lastSeen).getTime() < 60_000;
}

export function relTime(iso: string): string {
	if (!iso) return '—';
	const s = (Date.now() - new Date(iso).getTime()) / 1000;
	if (s < 5) return '刚刚';
	if (s < 60) return `${Math.floor(s)} 秒前`;
	if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
	if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
	return `${Math.floor(s / 86400)} 天前`;
}

export function shortTime(iso: string): string {
	if (!iso) return '—';
	const d = new Date(iso);
	const p = (n: number) => String(n).padStart(2, '0');
	return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

const BADGE: Record<string, string> = {
	running: 'ok',
	healthy: 'ok',
	done: 'ok',
	active: 'ok',
	queued: 'info',
	acked: 'warn',
	exited: '',
	stopped: '',
	revoked: '',
	failed: 'err',
	dead: 'err',
	unhealthy: 'err'
};

export function badgeOf(state: string): string {
	const s = (state || '').toLowerCase();
	for (const [k, v] of Object.entries(BADGE)) if (s.includes(k)) return v;
	return '';
}

const BADGE_CLS: Record<string, string> = {
	ok: 'bg-ok-soft text-ok',
	warn: 'bg-warn-soft text-warn',
	err: 'bg-err-soft text-err',
	info: 'bg-primary-soft text-primary'
};

export function badgeCls(kind: string): string {
	return BADGE_CLS[kind] ?? 'bg-neutral-100 text-ink-2';
}
