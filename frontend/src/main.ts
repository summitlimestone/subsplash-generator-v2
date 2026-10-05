import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'

// The window opens with ?token= to sign in; the server has set a cookie
// by now, so keep the token out of the address.
if (new URLSearchParams(location.search).has('token')) history.replaceState(null, '', location.pathname)

mount(App, { target: document.getElementById('app')! })
