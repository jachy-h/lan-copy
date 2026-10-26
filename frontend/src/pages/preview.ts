import './recovery'
import { createApp } from 'vue'
import PreviewPage from '../components/PreviewPage.vue'

const host = document.querySelector('#preview-app')
if (host) createApp(PreviewPage).mount(host)
