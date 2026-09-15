// 全局类型补充
declare global {
	// 构建时由 vite define 注入（见 vite.config.ts，UI_VERSION）
	const __APP_VERSION__: string;

	namespace App {
		// interface Error {}
		// interface Locals {}
		// interface PageData {}
	}
}

export {};
