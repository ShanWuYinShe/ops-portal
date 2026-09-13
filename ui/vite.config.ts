import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	server: {
		proxy: {
			'/api': {
				target: process.env.VITE_API_PROXY || 'http://127.0.0.1:8319',
				changeOrigin: true
			}
		}
	}
});
