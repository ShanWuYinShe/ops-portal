import { test, expect, type APIRequestContext } from '@playwright/test';
import { createHmac } from 'node:crypto';

// webhook 闭环冒烟：建端点 → 带正确 HMAC 签名 POST 到 /api/hooks/<slug> 得 202
// → 管理 API 列表里能看到这条投递记录。
//
// 依赖 e2e 容器的启动参数（见 README「本地跑 e2e」/ ci.yml 的 e2e 作业）：
//   DEV_ALLOW_NO_CERT=1   → 无证请求按 dev-no-cert/ops.admin 放行（管理 API 可写）
//   TRUSTED_PROXY_CIDRS=0.0.0.0/0 → 直连来源被判为受信代理，requireCert 才放行
// 两者缺一都会 403，属环境问题而非用例问题。

// 与后端 readWebhookBody 的 webhookMaxBody 一致（256KB）
const WEBHOOK_BODY_LIMIT = 256 * 1024;

/** 与 backend/webhook_receive.go verifyWebhookSignature 同构：
 *  无时间戳头时 payload = body；有则 payload = "<ts>." + body。 */
function sign(secret: string, body: string, ts?: string): string {
	const payload = ts ? `${ts}.${body}` : body;
	return `sha256=${createHmac('sha256', secret).update(payload).digest('hex')}`;
}

/** 建端点并直接返回明文 secret（upsert 入参即明文，rotate 才是唯一另一次可读机会）。 */
async function createEndpoint(request: APIRequestContext, slug: string, secret: string) {
	const res = await request.post('/api/portal/webhooks/endpoints/upsert', {
		data: { name: `e2e ${slug}`, slug, secret, enabled: true, note: 'created by e2e' }
	});
	expect(res.ok(), await res.text()).toBeTruthy();
	return { slug, secret };
}

test('webhook 签名投递闭环：202 → 落库可见', async ({ request }) => {
	const slug = `e2e-${Date.now()}`;
	const secret = 'whsec_e2e_test_secret';
	await createEndpoint(request, slug, secret);

	const body = JSON.stringify({ type: 'e2e.ping', hello: 'world' });
	const res = await request.post(`/api/hooks/${slug}`, {
		headers: {
			'Content-Type': 'application/json',
			'X-Webhook-Signature': sign(secret, body),
			'X-Webhook-Event': 'e2e.ping',
			'X-Webhook-Delivery': `e2e-delivery-${slug}`
		},
		data: body
	});
	expect(res.status(), await res.text()).toBe(202);
	const accepted = await res.json();
	expect(accepted.ok).toBe(true);
	expect(accepted.accepted).toBe(true);
	expect(accepted.dedup).toBe(false);
	expect(accepted.delivery_id).toBeTruthy();

	// 列表能看到该投递，状态为 pending（尚无转发规则，worker 不会改它）
	const list = await request.get(`/api/portal/webhooks/deliveries?limit=50&status=pending`);
	expect(list.ok(), await list.text()).toBeTruthy();
	const { deliveries } = await list.json();
	const found = deliveries.find((d: { id: string }) => d.id === accepted.delivery_id);
	expect(found, '新投递应出现在列表里').toBeTruthy();
	expect(found.slug).toBe(slug);
	expect(found.event_type).toBe('e2e.ping');
	expect(found.sig_ok).toBe(true);

	// 详情含原文 body
	const detail = await request.get(`/api/portal/webhooks/deliveries/get?id=${accepted.delivery_id}`);
	expect(detail.ok(), await detail.text()).toBeTruthy();
	const got = await detail.json();
	expect(got.delivery.body).toBe(body);
});

test('webhook 拒绝路径：错签名 401、未知 slug 404、重复投递去重', async ({ request }) => {
	const slug = `e2e-neg-${Date.now()}`;
	const secret = 'whsec_e2e_neg_secret';
	await createEndpoint(request, slug, secret);
	const body = JSON.stringify({ type: 'e2e.neg' });

	// 1) 签名不匹配 → 401，且落一条 rejected 记录（body 仍存）
	const bad = await request.post(`/api/hooks/${slug}`, {
		headers: { 'X-Webhook-Signature': sign('wrong-secret', body) },
		data: body
	});
	expect(bad.status()).toBe(401);

	const rejected = await request.get(`/api/portal/webhooks/deliveries?limit=50&status=rejected`);
	const { deliveries: rejects } = await rejected.json();
	const rej = rejects.find((d: { slug: string }) => d.slug === slug);
	expect(rej, '签名失败也应留痕').toBeTruthy();
	expect(rej.sig_ok).toBe(false);
	expect(rej.reject_reason).toBeTruthy();

	// 2) 未知 slug → 404
	const unknown = await request.post('/api/hooks/e2e-no-such-slug', {
		headers: { 'X-Webhook-Signature': sign(secret, body) },
		data: body
	});
	expect(unknown.status()).toBe(404);

	// 3) 同 X-Webhook-Delivery 重复投递 → 200 dedup=true 且复用同一 delivery_id
	const dedupKey = `e2e-dedup-${slug}`;
	const headers = { 'X-Webhook-Signature': sign(secret, body), 'X-Webhook-Delivery': dedupKey };
	const first = await request.post(`/api/hooks/${slug}`, { headers, data: body });
	expect(first.status()).toBe(202);
	const firstID = (await first.json()).delivery_id;
	const second = await request.post(`/api/hooks/${slug}`, { headers, data: body });
	expect(second.status()).toBe(200);
	const secondBody = await second.json();
	expect(secondBody.dedup).toBe(true);
	expect(secondBody.delivery_id).toBe(firstID);
});

test('webhook 时间戳参与签名：带 ts 时签名串为 "<ts>." + body', async ({ request }) => {
	const slug = `e2e-ts-${Date.now()}`;
	const secret = 'whsec_e2e_ts_secret';
	await createEndpoint(request, slug, secret);
	const body = JSON.stringify({ type: 'e2e.ts' });
	const ts = String(Math.floor(Date.now() / 1000));

	// 按含 ts 的签名串签 → 202
	const ok = await request.post(`/api/hooks/${slug}`, {
		headers: { 'X-Webhook-Signature': sign(secret, body, ts), 'X-Webhook-Timestamp': ts },
		data: body
	});
	expect(ok.status(), await ok.text()).toBe(202);

	// 带 ts 头但按不含 ts 的签名串签 → 401
	const bad = await request.post(`/api/hooks/${slug}`, {
		headers: { 'X-Webhook-Signature': sign(secret, body), 'X-Webhook-Timestamp': ts },
		data: body
	});
	expect(bad.status()).toBe(401);
});

test('webhook 管理 API 校验与统计', async ({ request }) => {
	const slug = `e2e-adm-${Date.now()}`;

	// 坏 slug 被拒（400）
	const badSlug = await request.post('/api/portal/webhooks/endpoints/upsert', {
		data: { name: 'bad', slug: 'E2E Space Slug!', secret: 'x', enabled: true }
	});
	expect(badSlug.status()).toBe(400);

	// 私网 target_url 被拒（SSRF 红线；e2e 容器未设 OPS_PROBE_ALLOW_PRIVATE）
	await createEndpoint(request, slug, 'whsec_e2e_adm');
	const badURL = await request.post('/api/portal/webhooks/sources/upsert', {
		data: {
			endpoint_id: (await endpointID(request, slug)),
			name: 'ssrf',
			target_url: 'http://127.0.0.1:9999/internal',
			secret: 's',
			enabled: true,
			max_attempts: 3,
			timeout_ms: 1000
		}
	});
	expect(badURL.status()).toBe(400);

	// 统计信封齐全
	const stats = await request.get('/api/portal/webhooks/stats');
	expect(stats.ok(), await stats.text()).toBeTruthy();
	const { stats: s } = await stats.json();
	for (const k of ['total', 'pending', 'delivered', 'failed', 'rejected', 'endpoints', 'sources']) {
		expect(typeof s[k], `stats.${k} 应为数字`).toBe('number');
	}
	// 端点计数应 ≥ 本文件此前建的端点
	expect(s.endpoints).toBeGreaterThan(0);
});

/** 按 slug 查端点 id（sources/upsert 需要）。 */
async function endpointID(request: APIRequestContext, slug: string): Promise<string> {
	const res = await request.get('/api/portal/webhooks/endpoints');
	expect(res.ok(), await res.text()).toBeTruthy();
	const { endpoints } = await res.json();
	const ep = endpoints.find((e: { slug: string }) => e.slug === slug);
	expect(ep, `端点 ${slug} 应在列表里`).toBeTruthy();
	return ep.id;
}

test('webhook 端点列表密钥打码，不泄露明文', async ({ request }) => {
	const slug = `e2e-mask-${Date.now()}`;
	const secret = 'whsec_e2e_mask_plaintext_secret';
	await createEndpoint(request, slug, secret);

	const res = await request.get('/api/portal/webhooks/endpoints');
	const { endpoints } = await res.json();
	const ep = endpoints.find((e: { slug: string }) => e.slug === slug);
	expect(ep).toBeTruthy();
	expect(ep.secret).not.toBe(secret);
	expect(ep.secret).toContain('********');
	// 打码串不得包含原密钥的中间段
	expect(ep.secret.length).toBeLessThan(secret.length);
});

// 常量保持与后端一致，改后端上限时这里会失败提醒同步文档
test('webhook body 上限常量与后端一致（256KB）', () => {
	expect(WEBHOOK_BODY_LIMIT).toBe(262144);
});

// ── UI 层（真实浏览器）：此前只有 API 用例，页面 DOM/表单/轮询无回归保护 ──

test('webhook 管理台 UI：建端点 → 密钥打码 → 签名回调出现在投递列表', async ({ page, request }) => {
	test.setTimeout(90_000); // 页面 10s 轮询 + dispatcher 5s 扫描，给足余量
	const slug = `ui-${Date.now()}`;
	// 密钥每次运行唯一：容器数据集跨用例共享，固定字面量会产生两条相同打码行，
	// getByText 触发 strict mode violation
	const secret = `ui-e2e-secret-${Date.now()}`;
	// 与后端 maskSecret 同构：首4位 + 定长8个* + 尾4位（改算法时此处失败提醒同步）
	const masked = secret.slice(0, 4) + '********' + secret.slice(-4);
	await page.goto('/webhooks');

	// 表单 label 与 input 经 for/id 关联（a11y），getByLabel 精确匹配全文案
	await page.getByLabel('名称', { exact: true }).fill(`UI e2e ${slug}`);
	await page.getByLabel('slug（接收路径，全局唯一）').fill(slug);
	await page.getByLabel('密钥（留空 = 不验签，仅受信代理可调用）').fill(secret);
	await page.getByRole('button', { name: '创建端点' }).click();
	await expect(page.getByText('端点已创建')).toBeVisible();

	// 表格行出现；密钥只以打码形式展示（首尾各4位+中间定长8个*），明文不得出现
	await expect(page.getByText(`slug: ${slug}`)).toBeVisible();
	await expect(page.getByText(masked)).toBeVisible();
	await expect(page.getByText(secret)).toHaveCount(0);

	// 用 UI 里刚填的密钥发签名回调：202 后投递行出现，并最终被 worker 判为
	//「已送达」（无启用规则 → 送达完毕，留 reject_reason=no enabled source）
	const body = JSON.stringify({ type: 'ui-e2e', at: slug });
	const res = await request.post(`/api/hooks/${slug}`, {
		headers: { 'Content-Type': 'application/json', 'X-Webhook-Signature': sign(secret, body) },
		data: body
	});
	expect(res.status(), await res.text()).toBe(202);

	const row = page.getByRole('row', { name: new RegExp(slug) });
	await expect(row).toBeVisible({ timeout: 25_000 });
	await expect(row.getByText('已送达')).toBeVisible({ timeout: 30_000 });
});

