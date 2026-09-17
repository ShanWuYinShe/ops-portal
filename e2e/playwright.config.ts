import { defineConfig } from '@playwright/test';

// E2E 对本地/CI 启动的 backend 容器跑（DEV_ALLOW_NO_CERT=1 的 dev 管理员），
// 不含 webServer——容器由 CI 作业或本地 docker run 负责，见 ci.yml smoke/e2e。
export default defineConfig({
	testDir: './tests',
	timeout: 30_000,
	// 共享一个 backend 实例与数据集，串行跑避免相互污染
	workers: 1,
	retries: 0,
	reporter: [['list']],
	use: {
		baseURL: 'http://127.0.0.1:8319',
		headless: true
	}
});
