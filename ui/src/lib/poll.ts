// 轮询帮手：统一各页手写的 setInterval 模板（hidden 时跳过，返回清理函数）。
// 用法：$effect(() => poll(load, 10_000));

export function poll(fn: () => Promise<void> | void, ms: number): () => void {
	const tick = () => {
		if (!document.hidden) void fn();
	};
	tick();
	const t = setInterval(tick, ms);
	return () => clearInterval(t);
}
