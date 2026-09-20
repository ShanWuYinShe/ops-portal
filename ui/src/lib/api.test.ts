// api() 纯逻辑单测：URL/头合并、超时默认、错误分支（非 2xx、ok:false、
// JSON 解析失败回退）。不测 Svelte 组件，fetch 以 stub 注入。
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';

function jsonResponse(status: number, body: unknown): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('api()', () => {
	it('2xx 且 body 无 ok 字段：原样返回 body', async () => {
		const fetchMock = vi.fn(async () => jsonResponse(200, { hello: 'world' }));
		vi.stubGlobal('fetch', fetchMock);

		await expect(api<{ hello: string }>('/api/x')).resolves.toEqual({ hello: 'world' });
	});

	it('默认 20s 超时 signal 与 application/json 头；调用方可覆盖头', async () => {
		let captured: RequestInit = {};
		const fetchMock = vi.fn(async (_url: string | URL, init?: RequestInit) => {
			captured = init ?? {};
			return jsonResponse(200, { ok: true });
		});
		vi.stubGlobal('fetch', fetchMock);

		await api('/api/y', { headers: { 'Content-Type': 'text/plain' } });

		expect(fetchMock).toHaveBeenCalledWith('/api/y', expect.any(Object));
		const headers = captured.headers as Record<string, string>;
		expect(headers['Content-Type']).toBe('text/plain'); // 调用方覆盖默认
		expect(captured.signal).toBeInstanceOf(AbortSignal); // 默认超时 signal 存在
	});

	it('ok:false 时抛 body.error', async () => {
		vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(200, { ok: false, error: '没有权限' })));

		await expect(api('/api/z')).rejects.toThrow('没有权限');
	});

	it('非 2xx 带 error：抛 error 字段而非状态码', async () => {
		vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(403, { ok: false, error: 'forbidden' })));

		await expect(api('/api/z')).rejects.toThrow('forbidden');
	});

	it('非 2xx 且 body 非 JSON：回退 `HTTP <status>`', async () => {
		const res = new Response('<html>502</html>', { status: 502 });
		vi.stubGlobal('fetch', vi.fn(async () => res));

		await expect(api('/api/z')).rejects.toThrow('HTTP 502');
	});

	it('2xx 但 body 空（解析失败回退 {}）：不抛错并返回空对象', async () => {
		const res = new Response('', { status: 200 });
		vi.stubGlobal('fetch', vi.fn(async () => res));

		await expect(api('/api/z')).resolves.toEqual({});
	});
});
