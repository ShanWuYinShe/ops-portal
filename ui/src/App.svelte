<script>
  import { onMount } from 'svelte';
  import { api } from './lib/api.js';
  import Sites from './lib/Sites.svelte';
  import Commands from './lib/Commands.svelte';
  import Certs from './lib/Certs.svelte';
  import Tokens from './lib/Tokens.svelte';
  import Audit from './lib/Audit.svelte';

  let whoami = null;
  let agents = [];
  let error = '';
  let tab = 'sites';

  let commandsRef;
  let certsRef;
  let tokensRef;
  let auditRef;

  async function load() {
    error = '';
    try {
      whoami = await api('/api/portal/whoami');
      const a = await api('/api/portal/agents');
      agents = a.agents ?? [];
      await refreshTab();
    } catch (e) {
      error = String(e);
    }
  }

  async function refreshTab() {
    if (tab === 'commands') await commandsRef?.load?.();
    if (tab === 'certs') await certsRef?.load?.();
    if (tab === 'tokens') await tokensRef?.load?.();
    if (tab === 'audit') await auditRef?.load?.();
  }

  async function switchTab(t) {
    tab = t;
    await refreshTab();
  }

  onMount(load);
</script>

<main>
  <h1>Ops Portal（Phase 2）</h1>
  {#if whoami}
    <p>当前：{whoami.username}（{whoami.role}）· 证书 {whoami.serial || '无（dev）'}</p>
  {/if}
  {#if error}
    <p class="error">加载失败：{error}</p>
  {/if}
  <nav>
    <button class:active={tab === 'sites'} onclick={() => switchTab('sites')}>站点</button>
    <button class:active={tab === 'commands'} onclick={() => switchTab('commands')}>命令</button>
    <button class:active={tab === 'certs'} onclick={() => switchTab('certs')}>证书</button>
    <button class:active={tab === 'tokens'} onclick={() => switchTab('tokens')}>Token</button>
    <button class:active={tab === 'audit'} onclick={() => switchTab('audit')}>审计</button>
    <button onclick={load}>全部刷新</button>
  </nav>

  <div hidden={tab !== 'sites'}><Sites {agents} /></div>
  <div hidden={tab !== 'commands'}><Commands {agents} onChange={load} bind:this={commandsRef} /></div>
  <div hidden={tab !== 'certs'}><Certs bind:this={certsRef} /></div>
  <div hidden={tab !== 'tokens'}><Tokens bind:this={tokensRef} /></div>
  <div hidden={tab !== 'audit'}><Audit bind:this={auditRef} /></div>
</main>

<style>
  main {
    max-width: 1100px;
    margin: 2rem auto;
    padding: 0 1rem;
    font-family: system-ui, sans-serif;
  }
  nav {
    display: flex;
    gap: 0.5rem;
    margin: 1rem 0;
  }
  button.active {
    font-weight: bold;
  }
  .error {
    color: #b00;
  }
</style>
