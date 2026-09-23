// 轮询帮手：统一各页手写的 setInterval 模板（hidden 时跳过，返回清理函数）。
// 用法：$effect(() => poll(load, 10_000));

export function poll(fn: () => Promise<void> | void, ms: number): () => void {
	const tick = () => {
		if (!document.hidden) void fn();
	};
	tick();
	const t = setInterval(tick, ms);
	// hidden 期间的间隔拍全被跳过：切回标签页时立即补一拍，否则要干等
	// 一个完整周期才能看到刷新（期间是空态表格或过期在线态）。
	const onVisible = () => {
		if (!document.hidden) tick();
	};
	document.addEventListener('visibilitychange', onVisible);
	return () => {
		clearInterval(t);
		document.removeEventListener('visibilitychange', onVisible);
	};
}
