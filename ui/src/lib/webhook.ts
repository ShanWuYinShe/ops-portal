// Webhook 网关的前端 API 封装（设计见 .workwork/plan/webhook-design.md）。
// 契约与后端 handler 冻结一致：全部响应为 {ok:true, ...} 信封，非 2xx 或 ok:false
// 由 api() 抛错，此处只做路径/请求体拼接与纯逻辑映射（纯逻辑单测见 webhook.test.ts）。

import { api } from './api';

export interface WebhookEndpoint {
	id: string;
	name: string;
	slug: string;
	/** 后端返回的打码串（前 4 + **** + 后 4），明文仅轮换时一次性返回 */
	secret: string;
	note: string;
	enabled: boolean;
	created_at: string;
	created_by: string;
}

export interface WebhookSource {
	id: string;
	endpoint_id: string;
	name: string;
	target_url: string;
	secret: string;
	enabled: boolean;
	max_attempts: number;
	timeout_ms: number;
	created_at: string;
}

export interface WebhookDelivery {
	id: string;
	endpoint_id: string;
	slug: string;
	received_at: string;
	remote_ip: string;
	method: string;
	content_type: string;
	body_bytes: number;
	sig_ok: boolean;
	status: string;
	reject_reason: string;
	event_type: string;
	dedup_key: string;
}

/** deliveries/get 的投递详情：比列表多出 body 与 headers */
export interface WebhookDeliveryDetail extends WebhookDelivery {
	body: string;
	headers: Record<string, string>;
}

export interface WebhookAttempt {
	id: number;
	delivery_id: string;
	source_id: string;
	attempt_no: number;
	status: string;
	http_status: number;
	error: string;
	duration_ms: number;
	response_body: string;
	at: string;
}

export interface WebhookStats {
	total: number;
	pending: number;
	delivered: number;
	failed: number;
	rejected: number;
	endpoints: number;
	sources: number;
}

/**
 * 可筛选的投递状态 = 后端**实际会落库**的 4 种。
 *
 * 不含 'dedup'：命中幂等时接收端点直接返回 200 {"dedup":true} 且不新建记录
 * （见 docs/webhook.md「投递状态」），所以库里永远查不到该状态的行，把它放进
 * 筛选下拉只会让用户选中后看到空列表——属误导，故不提供。
 * 后端 status 白名单仍接受 'dedup'（向前兼容），此处仅收窄 UI 可选项。
 */
export const DELIVERY_STATUSES = ['pending', 'delivered', 'failed', 'rejected'] as const;

export interface WebhookEndpointInput {
	/** 省略表示新建 */
	id?: string;
	name: string;
	slug: string;
	/** 省略表示保留原密钥；新建时空串=不启用签名校验（仅受信代理可调用） */
	secret?: string;
	enabled: boolean;
	note: string;
}

export interface WebhookSourceInput {
	/** 省略表示新建 */
	id?: string;
	endpoint_id: string;
	name: string;
	target_url: string;
	/** 省略表示保留原密钥 */
	secret?: string;
	enabled: boolean;
	max_attempts: number;
	timeout_ms: number;
}

/** 接收地址：外部服务回调要打到的公网地址（origin + /api/hooks/<slug>） */
export function inboundUrl(origin: string, slug: string): string {
	return `${(origin || '').replace(/\/+$/, '')}/api/hooks/${encodeURIComponent(slug || '')}`;
}

/** 拼接查询串：空值（undefined/null/空串）一律丢弃，避免后端收到 status= 之类的空过滤 */
export function queryString(params: Record<string, string | number | undefined | null>): string {
	const q = new URLSearchParams();
	for (const [k, v] of Object.entries(params)) {
		if (v === undefined || v === null || v === '') continue;
		q.set(k, String(v));
	}
	const s = q.toString();
	return s ? `?${s}` : '';
}

export async function listEndpoints(): Promise<WebhookEndpoint[]> {
	const r = await api<{ endpoints: WebhookEndpoint[] }>('/api/portal/webhooks/endpoints');
	return r.endpoints ?? [];
}

export async function upsertEndpoint(input: WebhookEndpointInput): Promise<void> {
	await api('/api/portal/webhooks/endpoints/upsert', {
		method: 'POST',
		body: JSON.stringify(input)
	});
}

export async function removeEndpoint(id: string): Promise<void> {
	await api('/api/portal/webhooks/endpoints/remove', {
		method: 'POST',
		body: JSON.stringify({ id })
	});
}

/** 轮换入站密钥：明文只在此处返回一次，页面须立即提示复制 */
export async function rotateEndpointSecret(id: string): Promise<string> {
	const r = await api<{ secret: string }>('/api/portal/webhooks/endpoints/rotate-secret', {
		method: 'POST',
		body: JSON.stringify({ id })
	});
	return r.secret ?? '';
}

export async function listSources(endpointId?: string): Promise<WebhookSource[]> {
	const r = await api<{ sources: WebhookSource[] }>(
		`/api/portal/webhooks/sources${queryString({ endpoint_id: endpointId })}`
	);
	return r.sources ?? [];
}

export async function upsertSource(input: WebhookSourceInput): Promise<void> {
	await api('/api/portal/webhooks/sources/upsert', {
		method: 'POST',
		body: JSON.stringify(input)
	});
}

export async function removeSource(id: string): Promise<void> {
	await api('/api/portal/webhooks/sources/remove', {
		method: 'POST',
		body: JSON.stringify({ id })
	});
}

export async function listDeliveries(opts: {
	limit?: number;
	status?: string;
	endpointId?: string;
} = {}): Promise<WebhookDelivery[]> {
	const r = await api<{ deliveries: WebhookDelivery[] }>(
		`/api/portal/webhooks/deliveries${queryString({
			limit: opts.limit ?? 50,
			status: opts.status,
			endpoint_id: opts.endpointId
		})}`
	);
	return r.deliveries ?? [];
}

export async function getDelivery(
	id: string
): Promise<{ delivery: WebhookDeliveryDetail; attempts: WebhookAttempt[] }> {
	const r = await api<{ delivery: WebhookDeliveryDetail; attempts: WebhookAttempt[] }>(
		`/api/portal/webhooks/deliveries/get${queryString({ id })}`
	);
	return { delivery: r.delivery, attempts: r.attempts ?? [] };
}

export async function replayDelivery(id: string): Promise<void> {
	await api('/api/portal/webhooks/deliveries/replay', {
		method: 'POST',
		body: JSON.stringify({ id })
	});
}

export async function getStats(): Promise<WebhookStats> {
	const r = await api<{ stats: WebhookStats }>('/api/portal/webhooks/stats');
	return (
		r.stats ?? {
			total: 0,
			pending: 0,
			delivered: 0,
			failed: 0,
			rejected: 0,
			endpoints: 0,
			sources: 0
		}
	);
}

// ── 纯展示映射 ──

const STATUS_LABEL: Record<string, string> = {
	pending: '待转发',
	delivered: '已送达',
	failed: '转发失败',
	rejected: '已拒绝',
	dedup: '重复投递'
};

/** 投递状态中文标签；未知状态原样返回，不吞掉后端新状态 */
export function statusLabel(status: string): string {
	return STATUS_LABEL[status] ?? (status || '—');
}

/** 投递状态 → 既有 badge 配色 kind（对应 state.svelte.ts 的 badgeCls） */
export function statusBadgeKind(status: string): string {
	switch (status) {
		case 'delivered':
			return 'ok';
		case 'pending':
			return 'info';
		case 'dedup':
			return 'warn';
		case 'failed':
		case 'rejected':
			return 'err';
		default:
			return '';
	}
}

/** 0/1（SQLite INTEGER）或 bool 都按启用处理 */
export function asBool(v: boolean | number | undefined | null): boolean {
	return v === true || v === 1;
}

/** 字节数可读化（表格里省一列宽度） */
export function bytesText(n: number): string {
	if (!n || n <= 0) return '0 B';
	if (n < 1024) return `${n} B`;
	if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
	return `${(n / (1024 * 1024)).toFixed(2)} MB`;
}

/** 转发尝试耗时展示（未执行完为 0） */
export function durationText(ms: number): string {
	if (!ms || ms <= 0) return '—';
	return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(2)} s`;
}

/** 尝试记录的状态 → badge kind（ok 视为成功，其余按失败） */
export function attemptBadgeKind(status: string): string {
	return status === 'ok' ? 'ok' : 'err';
}
