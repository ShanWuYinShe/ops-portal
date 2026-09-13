<script>
  import { onMount } from 'svelte';

  let whoami = null;
  let agents = [];
  let error = '';

  async function load() {
    error = '';
    try {
      const w = await fetch('/api/portal/whoami');
      whoami = await w.json();
      const a = await fetch('/api/portal/agents');
      const aj = await a.json();
      agents = aj.agents ?? [];
    } catch (e) {
      error = String(e);
    }
  }

  onMount(load);
</script>

<main>
  <h1>Ops Portal（Phase 1 骨架）</h1>
  {#if error}
    <p class="error">加载失败：{error}</p>
  {/if}
  <section>
    <h2>门禁（whoami）</h2>
    <pre>{JSON.stringify(whoami, null, 2)}</pre>
  </section>
  <section>
    <h2>在线 Agent（{agents.length}）</h2>
    {#each agents as a}
      <details>
        <summary>{a.agent_id}（{a.last_seen}）</summary>
        <pre>{JSON.stringify(a.heartbeat, null, 2)}</pre>
      </details>
    {:else}
      <p>暂无 agent 上报。本地联调：docker compose --profile dev up -d</p>
    {/each}
  </section>
  <button onclick={load}>刷新</button>
</main>

<style>
  main {
    max-width: 960px;
    margin: 2rem auto;
    padding: 0 1rem;
    font-family: system-ui, sans-serif;
  }
  pre {
    background: #f4f4f4;
    padding: 1rem;
    overflow: auto;
  }
  .error {
    color: #b00;
  }
</style>
