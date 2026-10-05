<script lang="ts">
  // Sets the series of several jobs at once. With freeText, any name can be
  // typed (a marking machine may not have the series set up yet);
  // otherwise only configured series can be picked.
  import { onMount } from 'svelte'
  import { app } from './lib/state.svelte'

  let {
    count,
    freeText = false,
    onapply,
    oncancel,
  }: { count: number; freeText?: boolean; onapply: (series: string) => void; oncancel: () => void } = $props()

  let value = $state('')
  let field = $state<HTMLInputElement | HTMLSelectElement>()
  onMount(() => field?.focus())
  const configured = $derived(app.series.filter((s) => !s.hidden).map((s) => s.name))
  const suggestions = $derived([...new Set([...configured, ...app.jobs.map((j) => j.series).filter(Boolean)])].sort())

  function submit(e: Event) {
    e.preventDefault()
    if (value.trim()) onapply(value.trim())
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="modal" onclick={oncancel}>
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
  <form class="panel" onclick={(e) => e.stopPropagation()} onsubmit={submit}>
    <strong>Set the series of {count} {count === 1 ? 'recording' : 'recordings'}</strong>
    {#if freeText}
      <input bind:this={field} bind:value list="series-dialog-options" placeholder="Series name" />
      <datalist id="series-dialog-options">{#each suggestions as name}<option value={name}></option>{/each}</datalist>
    {:else if configured.length}
      <select bind:this={field} bind:value>
        <option value="" disabled>Choose a series</option>
        {#each configured as name}<option value={name}>{name}</option>{/each}
      </select>
    {:else}
      <p class="muted">No series are set up yet.</p>
    {/if}
    <div class="buttons">
      <button type="button" onclick={oncancel}>Cancel</button>
      <button type="submit" class="accent" disabled={!value.trim()}>Set series</button>
    </div>
  </form>
</div>

<style>
  .modal { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.6); display: flex; align-items: center; justify-content: center; z-index: 10; }
  .panel { background: var(--bg); border: 1px solid var(--border); border-radius: 8px; padding: 16px; width: 380px; display: flex; flex-direction: column; gap: 12px; }
  .buttons { display: flex; justify-content: flex-end; gap: 8px; }
</style>
