import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	// 构建版本标识（git SHA），部署时经 compose args 注入；未设置则显示 dev
	define: {
		__APP_VERSION__: JSON.stringify(process.env.UI_VERSION ?? 'dev')
	},
	server: {
		proxy: {
			'/api': {
				target: process.env.VITE_API_PROXY || 'http://127.0.0.1:8319',
				changeOrigin: true
			}
		}
	}
});
