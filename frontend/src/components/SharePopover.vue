<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { requestJSON } from '../api/client'
import { copyText, notify } from '../ui/fileUi'
import { createSVG as createQRCode } from '../ui/qrcode'

import { usePopover } from '../composables/usePopover'
const { open, pinned, setOpen, onPointerEnter, onPointerLeave, onFocusOut } = usePopover('shareAccess', 'accessButton')

interface AccessResult { urls?: string[]; local?: boolean }
const accessUrls = ref<string[]>([])
const selectedUrl = ref('')
const localOnly = ref(false)
const error = ref('')
const qrHost = ref<HTMLElement | null>(null)
const currentURL = computed(() => selectedUrl.value)
const qrCode = computed(() => {
  if (!currentURL.value) return null
  try { return createQRCode(currentURL.value) } catch { return null }
})
const addressHint = computed(() => localOnly.value ? '当前仅本机访问。请监听所有网卡（-listen :8080）并允许防火墙访问，手机才能扫码连接。' : '手机与电脑连接同一局域网后，扫描二维码即可访问')
async function loadAccessInfo() {
  let urls: string[] = []
  try { const result = await requestJSON<AccessResult>('/api/access', { cache: 'no-store' }); if (Array.isArray(result.urls)) urls = result.urls; localOnly.value = false }
  catch { error.value = '读取访问地址失败' }
  const currentOrigin = location.origin, loopback = ['localhost', '127.0.0.1', '::1', '[::1]'].includes(location.hostname)
  localOnly.value = loopback && urls.length === 0
  const candidates = loopback ? (urls.length ? urls : [currentOrigin]) : [currentOrigin, ...urls]
  accessUrls.value = [...new Set(candidates.filter(url => /^https?:\/\//.test(url)))].sort((a, b) => Number(b.startsWith('http://192.')) - Number(a.startsWith('http://192.')))
  selectedUrl.value = accessUrls.value[0] || ''
  if (!selectedUrl.value) error.value = '未找到可访问地址'
}
watch(qrCode, value => {
  qrHost.value?.replaceChildren()
  if (value && qrHost.value) qrHost.value.append(value)
}, { flush: 'post' })
async function copyAddress() {
  const copied = await copyText(selectedUrl.value)
  notify(copied ? '访问地址已复制' : '复制失败，请手动选择', !copied)
}
onMounted(() => {
  void loadAccessInfo()
})

</script>

<template>
  <div id="shareAccess" class="share-access" @pointerenter="onPointerEnter" @pointerleave="onPointerLeave" @focusin="setOpen(true)" @focusout="onFocusOut">
    <button class="share-access-button" id="accessButton" type="button" aria-label="分享" title="分享" aria-haspopup="dialog" :aria-expanded="open" aria-controls="accessPanel" @click="pinned=!pinned;setOpen(pinned)"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/><path d="m8.6 10.5 6.8-4m-6.8 7 6.8 4"/></svg></button>
    <section class="access-card access-popover" id="accessPanel" role="dialog" aria-labelledby="shareAccessTitle" :hidden="!open">
      <span class="access-eyebrow" id="shareAccessTitle">分享</span>
      <div class="share-phone"><div class="access-copy"><h2 id="accessTitle">手机扫码，立即打开</h2><p id="accessHint">{{ addressHint }}</p></div><div ref="qrHost" class="qr-code" :class="{loading:!currentURL,error:Boolean(currentURL&&!qrCode)}" role="img" :aria-label="localOnly?'本机访问二维码，暂不可用于手机连接':currentURL?`扫描后访问 ${currentURL}`:'未找到可访问地址'"> <span v-if="!currentURL">{{error||'正在获取局域网地址…'}}</span><span v-else-if="!qrCode">二维码生成失败</span></div></div>
      <div class="access-copy share-computer"><h2>分享地址，电脑打开</h2><p>复制下方地址，分享给同一局域网内的电脑，在浏览器中打开。</p><div class="access-address"><span>局域网访问地址</span><a id="accessAddress" :href="currentURL||undefined">{{ currentURL||error||'正在获取局域网地址…' }}</a></div><div class="access-url-row"><select id="accessUrl" v-model="selectedUrl" aria-label="分享访问地址" :disabled="accessUrls.length<2" :hidden="accessUrls.length<2"><option v-for="url in accessUrls" :key="url" :value="url">{{ url }}</option></select><button id="copyAccessUrl" type="button" :disabled="!currentURL" @click="copyAddress">复制地址</button></div></div>
    </section>
  </div>
</template>
