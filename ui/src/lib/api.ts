// 统一 API 客户端：非 2xx 或 ok:false 时抛错。

export async function api<T = Record<string, unknown>>(
	path: string,
	opts: RequestInit = {}
): Promise<T> {
	const res = await fetch(path, {
		headers: { 'Content-Type': 'application/json', ...(opts.headers ?? {}) },
		...opts
	});
	const body = await res.json().catch(() => ({}));
	if (!res.ok || (body as { ok?: boolean }).ok === false) {
		const msg =
			(body as { error?: string }).error ?? `HTTP ${res.status}`;
		throw new Error(msg);
	}
	return body as T;
}

export interface Whoami {
	username: string;
	role: string;
	serial: string;
	subject: string;
	fingerprint: string;
	trusted: boolean;
	dev_no_cert: boolean;
}

export interface AgentSite {
	name: string;
	path: string;
	head: string;
	branch: string;
	dirty: boolean;
}

export interface AgentContainer {
	id: string;
	name: string;
	project: string;
	service: string;
	state: string;
	status: string;
	image: string;
}

export interface Agent {
	agent_id: string;
	last_seen: string;
	pending: number;
	heartbeat: {
		agent_id: string;
		hostname: string;
		compose_projects: string[];
		sites: AgentSite[];
		containers: AgentContainer[];
	};
}

/** 统一网站视图（/api/portal/sites）：按网站聚合的容器、git 与原生注册元数据。 */
export interface SiteAgentRef {
	agent_id: string;
	online: boolean;
}

/** 聚合视图中的容器/git 记录附带来源 agent（命令按机器下发）。 */
export interface SiteContainer extends AgentContainer {
	agent_id: string;
}

export interface SiteGit extends AgentSite {
	agent_id: string;
}

export interface SiteOverview {
	name: string;
	agents: SiteAgentRef[];
	containers: SiteContainer[];
	git: SiteGit[];
	ops_url?: string;
	cert_serial?: string;
	registered_at?: string;
	source: 'agent' | 'native' | 'both';
	/** portal 拨测 ops_url 的结果；undefined = 未注册 ops_url 或尚未探测 */
	reachable?: boolean;
	checked_at?: string;
	/** 站点自报健康档（ok|degraded|starting|down）；仅 reachable 时有效，空串=体不可解析 */
	health?: string;
}

export interface CertRecord {
	id: string;
	label: string;
	kind: string;
	cf_id: string;
	serial: string;
	fingerprint: string;
	common_name: string;
	issuer: string;
	status: string;
	expires_at: string;
	created_at: string;
	revoked_at: string;
	note: string;
}

export interface PortalCommand {
	id: string;
	agent_id: string;
	action: string;
	service: string;
	project: string;
	targets: string[] | null;
	status: string;
	created_by: string;
	created_at: string;
	result: string;
}

export interface AgentToken {
	id: string;
	agent_id: string;
	created_by: string;
	created_at: string;
	revoked: boolean;
	/** 正式 token 为 agent（旧数据无此字段），一次性注册令牌为 enroll */
	kind?: string;
	expires_at?: string;
	used?: boolean;
}

export interface AuditEntry {
	at: string;
	actor: string;
	cert: string;
	action: string;
	object: string;
	result: string;
	note: string;
}
