import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),

	kit: {
		// SPA 模式：页面客户端渲染，后端对未知路径回退 index.html（与 hotspot 同构）
		adapter: adapter({ fallback: 'index.html' }),
		alias: {
			'@/*': './src/*'
		}
	}
};

export default config;
