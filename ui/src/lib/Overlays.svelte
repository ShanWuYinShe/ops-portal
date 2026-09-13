<script>
  // 全局浮层：toast 队列 + 确认弹窗（App.svelte 挂载一次）
  import { toasts, confirmState, settleConfirm } from './ui.js';
</script>

<div class="toasts">
  {#each $toasts as t (t.id)}
    <div class="toast {t.kind}">{t.message}</div>
  {/each}
</div>

{#if $confirmState}
  <div class="mask" role="dialog" aria-modal="true">
    <div class="dialog">
      <h3>{$confirmState.title}</h3>
      <p>{$confirmState.message}</p>
      <div class="acts">
        <button class="btn" onclick={() => settleConfirm(false)}>取消</button>
        <button
          class="btn {$confirmState.danger ? 'danger' : 'primary'}"
          onclick={() => settleConfirm(true)}
        >
          确认
        </button>
      </div>
    </div>
  </div>
{/if}
