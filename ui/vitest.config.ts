// vitest 独立配置：只测 src 下的 .ts 纯逻辑（node 环境），不挂 sveltekit/
// tailwind 插件（那些归 vite.config.ts 的 dev/build；组件不在此测试范围）。
// 本文件存在使 vitest 不去读 vite.config.ts（sveltekit 插件在 test 下无意义）。
import { defineConfig } from 'vitest/config';

export default defineConfig({
	test: {
		environment: 'node',
		include: ['src/**/*.test.ts']
	}
});
