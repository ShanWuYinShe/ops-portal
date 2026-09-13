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
