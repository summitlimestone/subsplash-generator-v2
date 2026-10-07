<script lang="ts">
  // First run: bring over v1's settings if it was used here, then connect
  // to OBS. Everything here can be done later in Settings.
  import { onMount } from 'svelte'
  import { api, type Settings } from './lib/api'
  import { app, refresh } from './lib/state.svelte'

  let { ondone }: { ondone: () => void } = $props()

  type Step = 'import' | 'obs' | 'done'
  let step = $state<Step>(app.info?.v1Found ? 'import' : 'obs')
  let form = $state<Settings | null>(null)
  let password = $state('')
  let imported = $state('')
  let error = $state('')
  let busy = $state(false)
  let tried = $state(false)

  async function load() {
    form = await api.settings()
    password = ''
  }

  async function importV1() {
    busy = true
    error = ''
    try {
      const r = await api.importV1()
      imported = r.settings || r.series ? `Imported ${r.settings ? 'your settings' : ''}${r.settings && r.series ? ' and ' : ''}${r.series ? `${r.series} series` : ''}.` : 'There was nothing to import.'
      await Promise.all([load(), refresh()])
      step = 'obs'
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function connect() {
    if (!form) return
    busy = true
    error = ''
    try {
      await api.saveSettings({ ...form, obs: { ...form.obs, host: form.obs.host || 'localhost', password } })
      await load()
      tried = true
    } catch (e) {
      error = (e as Error).message
    } finally {
      busy = false
    }
  }

  async function finish() {
    await api.welcomed().catch(() => {})
    ondone()
  }

  onMount(() => {
    load().catch((e) => (error = (e as Error).message))
  })
</script>

<div class="backdrop">
  <div class="card">
    <h2>Welcome to Subsplash Generator</h2>

    {#if step === 'import'}
      <p>The old version was used on this computer. Bring over its settings and series?</p>
      {#if error}<p class="error">{error}</p>{/if}
      <div class="buttons">
        <button class="accent" onclick={importV1} disabled={busy}>Import</button>
        <button onclick={() => (step = 'obs')}>Start fresh</button>
      </div>
    {:else if step === 'obs' && form}
      {#if imported}<p class="notice">{imported}</p>{/if}
      <p>Connect to OBS so the app can follow your recordings.</p>
      <ol class="muted small">
        <li>In OBS, open <b>Tools</b>, <b>WebSocket Server Settings</b>.</li>
        <li>Tick <b>Enable WebSocket server</b>, then click <b>Show Connect Info</b> for the port and password.</li>
      </ol>
      <form class="grid" onsubmit={(e) => (e.preventDefault(), connect())}>
        <label>Computer <input bind:value={form.obs.host} placeholder="localhost" /></label>
        <label>Port <input type="number" bind:value={form.obs.port} /></label>
        <label>Password
          <input type="password" bind:value={password} placeholder={form.hasOBSPassword ? 'saved (type to change)' : ''} />
        </label>
        <button type="submit" class="accent" disabled={busy}>Connect</button>
      </form>
      {#if app.live?.obsConnected}
        <p class="ok">Connected to OBS.</p>
      {:else if tried}
        <p class="muted">{app.live?.obsError || 'Connecting…'}</p>
      {/if}
      {#if error}<p class="error">{error}</p>{/if}
      <div class="buttons">
        <button class:accent={app.live?.obsConnected} onclick={() => (step = 'done')}>{app.live?.obsConnected ? 'Next' : 'Skip for now'}</button>
      </div>
    {:else if step === 'done'}
      <p>You're ready.</p>
      <ul class="small">
        <li><b>Live</b> marks the sermon while you record a service, then trims and stitches it.</li>
        <li><b>Bulk edit</b> works through a folder of older recordings.</li>
        <li><b>Series</b> holds the intros and outros. <b>Settings</b> has ProPresenter, the OBS dock and the rest.</li>
      </ul>
      <div class="buttons"><button class="accent" onclick={finish}>Get started</button></div>
    {/if}
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.6); display: flex; align-items: center; justify-content: center; z-index: 50; }
  .card { background: var(--surface); border: 1px solid var(--border); border-radius: 10px; padding: 20px 24px; width: min(520px, calc(100% - 32px)); display: flex; flex-direction: column; gap: 10px; }
  h2 { margin: 0 0 4px; }
  p { margin: 0; }
  ol, ul { margin: 0; padding-left: 20px; display: flex; flex-direction: column; gap: 4px; }
  .grid { display: grid; grid-template-columns: minmax(0, 1fr) 80px minmax(0, 1fr) auto; gap: 8px; align-items: end; }
  label { display: flex; flex-direction: column; gap: 4px; color: var(--muted); min-width: 0; }
  label input { color: var(--text); width: 100%; box-sizing: border-box; }
  .buttons { display: flex; gap: 8px; justify-content: flex-end; margin-top: 6px; }
  .ok { color: var(--accent); }
  .small { font-size: 13px; }
</style>
