import { test, expect, type ConsoleMessage } from '@playwright/test';

// 收集一页上的控制台错误与 CSP 违规（CSP 拦截以 console error 形式出现）。
function collectViolations(page: import('@playwright/test').Page): string[] {
	const violations: string[] = [];
	const push = (m: ConsoleMessage) => {
		if (m.type() === 'error' || /Refused to|Content-Security-Policy/i.test(m.text())) {
			violations.push(`[${m.type()}] ${m.text()}`);
		}
	};
	page.on('console', push);
	page.on('pageerror', (e) => violations.push(`[pageerror] ${e.message}`));
	return violations;
}

test('全站页面渲染且 CSP 零违规', async ({ page }) => {
	const violations = collectViolations(page);
	await page.goto('/');
	await expect(page.getByRole('heading', { name: '仪表盘' })).toBeVisible();
	await expect(page.getByText('dev-no-cert')).toBeVisible(); // dev 免证管理员
	for (const path of ['/sites', '/ops', '/certs', '/tokens', '/audit']) {
		await page.goto(path);
		await page.waitForLoadState('networkidle');
	}
	expect(violations, violations.join('\n')).toEqual([]);
});

test('登记设备证书表单闭环', async ({ page }) => {
	const label = `pw-${Date.now()}`;
	await page.goto('/certs');
	await page.getByRole('button', { name: '登记设备证书' }).click();
	// getByLabel 依赖 label/for 关联（a11y 修复的回归测试）
	await page.getByLabel('名称（持有人-设备）*').fill(label);
	await page.getByLabel('证书序列号 Serial *').fill(`PW${Date.now()}`);
	await page.getByRole('button', { name: '保存', exact: true }).click();
	await expect(page.getByText(`已登记 ${label}`)).toBeVisible();
	await expect(page.getByRole('cell', { name: label })).toBeVisible();
});

test('下发命令出现在命令历史', async ({ page, request }) => {
	const agentID = `pw-agent-${Date.now()}`;
	const res = await request.post('/api/portal/commands/enqueue', {
		data: { portal_agent_id: agentID, action: 'restart', service: 'nginx', project: 'pw-demo' }
	});
	expect(res.ok()).toBeTruthy();
	await page.goto('/ops');
	await expect(page.getByRole('cell', { name: agentID })).toBeVisible();
	await expect(page.getByRole('cell', { name: /restart/ }).first()).toBeVisible();
});

test('审计记录可见并完成在线备份', async ({ page }) => {
	await page.goto('/audit');
	await expect(page.getByRole('button', { name: '备份数据库' })).toBeVisible();
	// 此前登记/入队操作留下的审计（workers=1 串行保证先跑）
	await expect(page.getByRole('cell', { name: 'certs.register' }).first()).toBeVisible();
	await page.getByRole('button', { name: '备份数据库' }).click();
	await page.getByRole('button', { name: '确认' }).click();
	await expect(page.getByText(/已备份/)).toBeVisible();
	await expect(page.getByRole('cell', { name: 'db.backup' }).first()).toBeVisible();
});
