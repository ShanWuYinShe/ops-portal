<script lang="ts">
	import { toast } from '$lib/dialogs.svelte';

	// 一次性密文展示（签发结果仅显示一次 + 复制）。
	// mono：单行凭据（token/enroll）；blob：多行大段（证书包）。
	let { headline, mono = '', blob = '' }: { headline: string; mono?: string; blob?: string } =
		$props();

	let copied = $state(false);

	async function copy() {
		try {
			await navigator.clipboard.writeText(mono || blob);
			copied = true;
		} catch {
			toast('复制失败，请手动选择复制', 'err');
		}
	}
</script>

<div class="mt-4 rounded-[7px] border border-[#f1d9a1] bg-warn-soft p-3">
	<div class="mb-1.5 font-semibold">{headline}</div>
	{#if mono}
		<div class="flex items-center gap-2.5">
			<code class="flex-1 break-all font-mono text-[12.5px]">{mono}</code>
			<button class="btn btn-sm" onclick={copy}>{copied ? '已复制' : '复制'}</button>
		</div>
	{:else}
		{#if blob}
			<div class="mb-1.5 flex items-center justify-end">
				<button class="btn btn-sm" onclick={copy}>{copied ? '已复制' : '复制全部'}</button>
			</div>
			<code class="block max-h-40 overflow-auto whitespace-pre-wrap break-all font-mono text-[12px]"
				>{blob}</code
			>
		{/if}
	{/if}
</div>
