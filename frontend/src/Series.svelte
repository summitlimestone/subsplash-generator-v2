<script lang="ts">
  // Series: each is an intro, an outro and the transition between them
  // and the sermon.
  import { api, type Series } from './lib/api'
  import { app } from './lib/state.svelte'
  import { TRANSITIONS } from './lib/transitions'

  const blank = (): Series => ({
    name: '', intro: '', intro_duration: 5, outro: '', outro_duration: 5,
    transition: 'fade', transition_duration: 1, hidden: false,
  })

  let selected = $state<string | null>(null) // the name being edited; '' for a new one
  let form = $state<Series>(blank())
  let error = $state('')
  let saved = $state('')

  const sorted = $derived([...app.series].sort((a, b) => a.name.localeCompare(b.name)))
  const usedBy = (name: string) => app.jobs.filter((j) => j.series === name && j.status !== 'done').length
  const isImage = (p: string) => /\.(jpe?g|png|bmp|tiff?|webp)$/i.test(p)
  const file = (p: string) => p.split(/[\\/]/).pop()

  function edit(s: Series | null) {
    error = saved = ''
    selected = s ? s.name : ''
    form = s ? { ...s } : blank()
  }

  function duplicate() {
    const copy = { ...form, name: `${form.name} (copy)` }
    edit(null)
    form = copy
  }

  async function browse(which: 'intro' | 'outro') {
    if (!app.info?.canOpenFiles) return
    try {
      const { path } = await api.openFile(`Choose the ${which}`, ['*.mp4', '*.mov', '*.mkv', '*.png', '*.jpg', '*.jpeg'])
      if (path) form[which] = path
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function save() {
    error = saved = ''
    try {
      const s = selected ? await api.updateSeries(selected, form) : await api.createSeries(form)
      selected = s.name
      form = { ...s }
      saved = 'Saved'
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function remove() {
    const n = usedBy(selected!)
    const msg = n ? `${n} unfinished ${n === 1 ? 'job uses' : 'jobs use'} "${selected}". Delete it anyway?` : `Delete "${selected}"?`
    if (!confirm(msg)) return
    try {
      await api.deleteSeries(selected!, n > 0)
      selected = null
    } catch (e) {
      error = (e as Error).message
    }
  }
</script>

<div class="series">
  <aside>
    <button class="accent" onclick={() => edit(null)}>New series</button>
    <ul>
      {#each sorted as s (s.name)}
        <li>
          <button class="item" class:active={selected === s.name} onclick={() => edit(s)}>
            {s.name}
            {#if s.hidden}<span class="muted small">hidden</span>{/if}
          </button>
        </li>
      {/each}
    </ul>
    {#if sorted.length === 0}<p class="muted small">No series yet.</p>{/if}
  </aside>

  {#if selected !== null}
    <form class="editor" onsubmit={(e) => (e.preventDefault(), save())}>
      <label>Name <input bind:value={form.name} required /></label>

      {#each ['intro', 'outro'] as const as which}
        <fieldset>
          <legend>{which === 'intro' ? 'Intro' : 'Outro'}</legend>
          <div class="pick">
            <input bind:value={form[which]} placeholder="Video or still image" title={form[which]} />
            {#if app.info?.canOpenFiles}<button type="button" onclick={() => browse(which)}>Browse&hellip;</button>{/if}
          </div>
          {#if isImage(form[which])}
            <label class="inline">Show the image for
              <input type="number" min="0.5" step="0.5" bind:value={form[which === 'intro' ? 'intro_duration' : 'outro_duration']} /> seconds
            </label>
          {:else if form[which]}
            <span class="muted small">{file(form[which])}</span>
          {/if}
        </fieldset>
      {/each}

      <fieldset>
        <legend>Transition</legend>
        <div class="pick">
          <select bind:value={form.transition}>
            {#each TRANSITIONS as t}<option value={t}>{t}</option>{/each}
          </select>
          <label class="inline"><input type="number" min="0.1" step="0.1" bind:value={form.transition_duration} /> seconds</label>
        </div>
      </fieldset>

      <label class="inline"><input type="checkbox" bind:checked={form.hidden} /> Hidden (left out of series lists)</label>

      <div class="buttons">
        <button type="submit" class="accent">Save</button>
        {#if selected}
          <button type="button" onclick={duplicate}>Duplicate</button>
          <span class="spacer"></span>
          <button type="button" class="danger" onclick={remove}>Delete</button>
        {/if}
      </div>
      {#if saved}<p class="muted">{saved}</p>{/if}
      {#if error}<p class="error">{error}</p>{/if}
    </form>
  {:else}
    <p class="muted hint">Choose a series to edit, or make a new one.</p>
  {/if}
</div>

<style>
  .series { display: flex; gap: 20px; padding: 12px 14px; align-items: flex-start; }
  aside { width: 240px; display: flex; flex-direction: column; gap: 8px; }
  ul { list-style: none; margin: 0; padding: 0; }
  .item { width: 100%; text-align: left; background: none; border-color: transparent; display: flex; justify-content: space-between; }
  .item.active { background: var(--surface); border-color: var(--border); }
  .editor { flex: 1; max-width: 620px; display: flex; flex-direction: column; gap: 12px; }
  label { display: flex; flex-direction: column; gap: 4px; color: var(--muted); }
  label.inline { flex-direction: row; align-items: center; gap: 6px; }
  label input:not([type='checkbox']) { color: var(--text); }
  input[type='number'] { width: 80px; }
  fieldset { border: 1px solid var(--border); border-radius: 6px; padding: 8px 12px 12px; display: flex; flex-direction: column; gap: 8px; }
  legend { color: var(--muted); padding: 0 4px; }
  .pick { display: flex; gap: 8px; align-items: center; }
  .pick input { flex: 1; }
  .buttons { display: flex; gap: 8px; }
  .spacer { flex: 1; }
  .small { font-size: 12px; }
  .hint { margin-top: 40px; }
</style>
