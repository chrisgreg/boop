import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'
import { registerServiceWorker } from './lib/web-push'

void registerServiceWorker().catch((error) => console.warn('service worker registration failed', error))

const app = mount(App, { target: document.getElementById('app')! })

export default app
