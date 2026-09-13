<script lang="ts">
	import { toasts, confirmBox, settleConfirm } from '$lib/dialogs.svelte';
	import { X, Check } from '@lucide/svelte';
</script>

<div class="fixed top-4 right-4 z-60 flex flex-col gap-2">
	{#each toasts.list as t (t.id)}
		<div
			class="toast min-w-60 max-w-96 rounded-[10px] border border-line bg-white px-3.5 py-2.5 text-[13px] shadow-lg
				{t.kind === 'ok' ? 'border-l-[3px] border-l-ok' : 'border-l-[3px] border-l-err'}"
		>
			{#if t.kind === 'ok'}<Check class="mt-0.5 h-4 w-4 flex-none text-ok" />{:else}<X class="mt-0.5 h-4 w-4 flex-none text-err" />{/if}
			<span>{t.message}</span>
		</div>
	{/each}
</div>

{#if confirmBox.current}
	<div class="fixed inset-0 z-50 grid place-items-center bg-[#0d1421]/70" role="dialog" aria-modal="true">
		<div class="w-[min(430px,calc(100vw-40px))] rounded-xl bg-white p-5 shadow-xl">
			<h3 class="mb-2 text-[15px] font-semibold text-ink">{confirmBox.current.title}</h3>
			<p class="mb-4.5 text-[13.5px] whitespace-pre-wrap text-ink-2">{confirmBox.current.message}</p>
			<div class="flex justify-end gap-2.5">
				<button class="btn" onclick={() => settleConfirm(false)}>取消</button>
				<button
					class="btn {confirmBox.current.danger
						? 'border-err text-err hover:bg-err-soft'
						: 'btn-primary'}"
					onclick={() => settleConfirm(true)}
				>
					确认
				</button>
			</div>
		</div>
	</div>
{/if}
