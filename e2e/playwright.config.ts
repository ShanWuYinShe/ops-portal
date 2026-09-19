import { defineConfig } from '@playwright/test';

// E2E 对本地/CI 启动的 backend 容器跑（DEV_ALLOW_NO_CERT=1 的 dev 管理员），
// 不含 webServer——容器由 CI 作业或本地 docker run 负责，见 ci.yml smoke/e2e
// 与 README「本地跑 e2e」。
export default defineConfig({
	testDir: './tests',
	timeout: 30_000,
	// 共享一个 backend 实例与数据集，串行跑避免相互污染
	workers: 1,
	// forbidOnly：本地遗留的 test.only 一旦提交，CI 会"全绿但只跑一条"
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 1 : 0,
	// html 报告供 CI 失败时上传（ci.yml 的 upload-artifact），list 供控制台
	reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : [['list']],
	use: {
		baseURL: 'http://127.0.0.1:8319',
		headless: true
	}
});
