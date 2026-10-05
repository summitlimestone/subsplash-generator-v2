<script lang="ts">
  import { onMount } from 'svelte'
  import { api, type Settings, type SlideMatch } from './lib/api'
  import { app, refresh } from './lib/state.svelte'

  let form = $state<Settings | null>(null)
  let saved = $state('')
  let original = ''
  let obsPassword = $state('')
  let ppPassword = $state('')
  let clearOBS = $state(false)
  let clearPP = $state(false)
  let error = $state('')
  let notice = $state('')
  let dockURLs = $state<string[]>([])
  let restartNeeded = $state(false)

  const dirty = $derived(!!form && (JSON.stringify(form) !== original || !!obsPassword || !!ppPassword || clearOBS || clearPP))
  const encoder = $derived(app.info?.encoders.find((e) => e.name === form?.render.encoder))
  const slides = $derived(app.live?.slides ?? [])

  async function load() {
    form = await api.settings()
    original = JSON.stringify(form)
    obsPassword = ppPassword = ''
    clearOBS = clearPP = false
    dockURLs = (await api.dockURLs()).urls
  }

  async function save() {
    if (!form) return
    error = saved = ''
    try {
      const out = await api.saveSettings({
        ...form,
        obs: { ...form.obs, password: obsPassword },
        propresenter: { ...form.propresenter, password: ppPassword },
        clearOBSPassword: clearOBS,
        clearPPPassword: clearPP,
      })
      restartNeeded ||= out.restartNeeded
      await load()
      saved = 'Saved'
    } catch (e) {
      error = (e as Error).message
    }
  }

  async function folder(which: 'trimmed_dir' | 'final_dir') {
    if (!form || !app.info?.canOpenFiles) return
    const { path } = await api.openFolder(which === 'trimmed_dir' ? 'Folder for trimmed clips' : 'Folder for finished videos')
    if (path) form[which] = path
  }

  // The mode follows which field is present, not whether it's filled in,
  // so choosing "text" before typing any stays on "text".
  function slideMode(s: SlideMatch): 'uid' | 'text' | 'none' {
    return s.uid !== undefined ? 'uid' : s.text !== undefined ? 'text' : 'none'
  }
  function setMode(which: 'begin_slide' | 'end_slide', mode: string) {
    if (!form) return
    form.propresenter[which] = mode === 'text' ? { text: form.propresenter[which].text ?? '', match: 'exact' } : mode === 'uid' ? { uid: slides[0]?.uid ?? '' } : {}
  }
  function useSlide(which: 'begin_slide' | 'end_slide', uid: string) {
    if (form) form.propresenter[which] = { uid }
  }
  const slideText = (uid?: string) => slides.find((s) => s.uid === uid)?.text

  async function regenerate() {
    if (!confirm('Make a new token? Docks and scripts using the current one will stop working until you paste the new address in.')) return
    restartNeeded ||= (await api.regenerateToken()).restartNeeded
    await load()
  }

  async function importV1() {
    error = notice = ''
    try {
      const r = await api.importV1()
      notice = r.settings || r.series ? `Imported ${r.settings ? 'settings' : ''}${r.settings && r.series ? ' and ' : ''}${r.series ? `${r.series} series` : ''} from ${r.folder}.` : `Nothing to import in ${r.folder}.`
      await Promise.all([load(), refresh()])
    } catch (e) {
      error = (e as Error).message
    }
  }

  onMount(() => {
    load().catch((e) => (error = (e as Error).message))
  })
</script>

{#if form}
  <form class="settings" onsubmit={(e) => (e.preventDefault(), save())}>
    <div class="bar">
      <button type="submit" class="accent" disabled={!dirty}>Save</button>
      {#if saved}<span class="muted">{saved}</span>{/if}
      {#if error}<span class="error">{error}</span>{/if}
      <span class="spacer"></span>
      <button type="button" onclick={importV1} title="Bring in the settings and series from the old version on this computer">Import from v1</button>
    </div>
    {#if notice}<p class="notice">{notice}</p>{/if}
    {#if restartNeeded}<p class="notice">Restart the app for the dock address and token changes to take effect.</p>{/if}

    <section>
      <h3>OBS <span class="state" class:ok={app.live?.obsConnected}>{app.live?.obsConnected ? 'connected' : app.live?.obsError || 'not connected'}</span></h3>
      <div class="grid">
        <label>Computer <input bind:value={form.obs.host} placeholder="localhost" /></label>
        <label>Port <input type="number" bind:value={form.obs.port} /></label>
        <label>Password
          <input type="password" bind:value={obsPassword} placeholder={form.hasOBSPassword ? 'saved (type to change)' : 'none'} />
        </label>
        {#if form.hasOBSPassword}<label class="inline"><input type="checkbox" bind:checked={clearOBS} /> Remove the saved password</label>{/if}
      </div>
      <p class="muted small">In OBS: Tools, WebSocket Server Settings.</p>
    </section>

    <section>
      <h3>ProPresenter <span class="state" class:ok={app.live?.ppConnected}>{app.live?.ppConnected ? 'connected' : app.live?.ppError || (form.propresenter.host ? 'not connected' : 'not used')}</span></h3>
      <div class="grid">
        <label>Computer <input bind:value={form.propresenter.host} placeholder="Leave empty to mark by hand" /></label>
        <label>Port <input type="number" bind:value={form.propresenter.port} /></label>
        <label>Stage App password
          <input type="password" bind:value={ppPassword} placeholder={form.hasPPPassword ? 'saved (type to change)' : 'none'} />
        </label>
        {#if form.hasPPPassword}<label class="inline"><input type="checkbox" bind:checked={clearPP} /> Remove the saved password</label>{/if}
      </div>

      {#each [['begin_slide', 'Sermon start slide'], ['end_slide', 'Sermon end slide']] as const as [which, label]}
        {@const s = form.propresenter[which]}
        <div class="slide-config">
          <span class="label">{label}</span>
          <select value={slideMode(s)} onchange={(e) => setMode(which, (e.target as HTMLSelectElement).value)}>
            <option value="none">Not used (mark by hand)</option>
            <option value="uid">A specific slide</option>
            <option value="text">Any slide with this text</option>
          </select>
          {#if slideMode(s) === 'uid'}
            <span class="mono small" title={s.uid}>{slideText(s.uid) ? `"${slideText(s.uid)}"` : s.uid?.slice(0, 8) + '…'}</span>
          {:else if slideMode(s) === 'text'}
            <input bind:value={s.text} placeholder="Slide text" />
            <select bind:value={s.match}>
              <option value="exact">exactly</option>
              <option value="regex">as a pattern</option>
            </select>
            <label class="inline"><input type="checkbox" bind:checked={s.case_sensitive} /> Match case</label>
          {/if}
        </div>
      {/each}

      <div class="slides">
        <span class="muted small">Recent slides: show the start and end slides in ProPresenter, then pick them here.</span>
        {#each slides.slice(0, 8) as sl (sl.at)}
          <div class="slide-row">
            <span class="text">{sl.text || '(no text)'}</span>
            <span class="mono muted small">{sl.uid.slice(0, 8)}</span>
            <button type="button" class="small" onclick={() => useSlide('begin_slide', sl.uid)}>Use as start</button>
            <button type="button" class="small" onclick={() => useSlide('end_slide', sl.uid)}>Use as end</button>
          </div>
        {:else}
          <span class="muted small">{app.live?.ppConnected ? 'None yet.' : 'Connect to ProPresenter to see slides.'}</span>
        {/each}
      </div>
    </section>

    <section>
      <h3>Marks</h3>
      <div class="grid">
        <label>Move the start mark by <span class="inline"><input type="number" step="0.1" bind:value={form.pad_start} /> s</span></label>
        <label>Move the end mark by <span class="inline"><input type="number" step="0.1" bind:value={form.pad_end} /> s</span></label>
      </div>
      <p class="muted small">Applied when a mark is set during a live service. Positive is later.</p>
    </section>

    <section>
      <h3>Output folders</h3>
      <div class="grid wide">
        <label>Trimmed clips
          <span class="pick"><input bind:value={form.trimmed_dir} />{#if app.info?.canOpenFiles}<button type="button" onclick={() => folder('trimmed_dir')}>Browse&hellip;</button>{/if}</span>
        </label>
        <label>Finished videos
          <span class="pick"><input bind:value={form.final_dir} />{#if app.info?.canOpenFiles}<button type="button" onclick={() => folder('final_dir')}>Browse&hellip;</button>{/if}</span>
        </label>
      </div>
    </section>

    <section>
      <h3>Rendering</h3>
      <div class="grid">
        <label>Encoder
          <select bind:value={form.render.encoder} onchange={() => form && (form.render.preset = '')}>
            {#each app.info?.encoders ?? [] as e}<option value={e.name}>{e.name === 'software' ? 'software (CPU)' : e.name}</option>{/each}
          </select>
        </label>
        <label>Speed preset
          <select bind:value={form.render.preset}>
            <option value="">default ({encoder?.defaultPreset})</option>
            {#each encoder?.presets ?? [] as p}<option value={p}>{p}</option>{/each}
          </select>
        </label>
        <label>Trim quality (CRF) <input type="number" min="0" max="51" bind:value={form.render.trim_crf} /></label>
        <label>Stitch quality (CRF) <input type="number" min="0" max="51" bind:value={form.render.stitch_crf} /></label>
        <label class="inline"><input type="checkbox" bind:checked={form.render.fast_copy} /> Fast trim (copy instead of re-encoding)</label>
        <label class="inline"><input type="checkbox" bind:checked={form.render.subsplash} /> Subsplash 1080p preset for the finished video</label>
        <label class="inline"><input type="checkbox" bind:checked={form.render.normalize} /> Even out loudness to
          <input type="number" step="0.5" bind:value={form.render.target_lufs} disabled={!form.render.normalize} /> LUFS</label>
      </div>
      <p class="muted small">New jobs start with these. Hardware encoders fall back to software if they can't run.</p>
    </section>

    <section>
      <h3>OBS dock</h3>
      <p class="muted small">In OBS: Docks, Custom Browser Docks. Paste an address below as the URL.</p>
      {#each dockURLs as url}
        <div class="pick"><input readonly value={url} /><button type="button" onclick={() => navigator.clipboard.writeText(url)}>Copy</button></div>
      {/each}
      <div class="grid">
        <label class="inline"><input type="checkbox" checked={form.api.host === '0.0.0.0'} onchange={(e) => form && (form.api.host = (e.target as HTMLInputElement).checked ? '0.0.0.0' : '127.0.0.1')} /> Allow other computers on the network</label>
        <label>Port <input type="number" bind:value={form.api.port} /></label>
      </div>
      <button type="button" class="small" onclick={regenerate}>Make a new token</button>
    </section>
  </form>
{:else if error}
  <p class="error settings">{error}</p>
{/if}

<style>
  .settings { padding: 12px 14px 40px; max-width: 820px; display: flex; flex-direction: column; gap: 14px; }
  .bar { display: flex; gap: 10px; align-items: center; position: sticky; top: 0; background: var(--bg); padding: 6px 0; z-index: 2; }
  .spacer { flex: 1; }
  section { border: 1px solid var(--border); border-radius: 8px; padding: 10px 14px; display: flex; flex-direction: column; gap: 10px; }
  h3 { margin: 0; font-size: 15px; display: flex; gap: 10px; align-items: baseline; }
  .state { font-size: 12px; font-weight: 400; color: var(--warning); }
  .state.ok { color: var(--accent); }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 10px 16px; }
  .grid.wide { grid-template-columns: 1fr; }
  label { display: flex; flex-direction: column; gap: 4px; color: var(--muted); font-size: 13px; }
  label.inline, .inline { flex-direction: row; display: flex; align-items: center; gap: 6px; }
  input[type='number'] { width: 90px; }
  .pick { display: flex; gap: 6px; }
  .pick input { flex: 1; }
  .slide-config { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  .slide-config .label { width: 140px; color: var(--muted); font-size: 13px; }
  .slides { display: flex; flex-direction: column; gap: 4px; }
  .slide-row { display: flex; gap: 8px; align-items: center; }
  .slide-row .text { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .small { font-size: 12px; }
  .notice { margin: 0; padding: 8px 12px; background: #2f3a24; border: 1px solid var(--accent); border-radius: 6px; }
</style>
