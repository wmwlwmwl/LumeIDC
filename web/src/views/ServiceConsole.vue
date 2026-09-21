<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { WarningFilled } from '@element-plus/icons-vue'
import RFB from '@novnc/novnc'
import { fetchServiceDetail, fetchVncPass } from '@/api/user'
import { apiPrefix } from '@/http/index'

// VNC 控制台：满屏裸页（前台路由 meta.bare；后台挂在顶层路由，均不套框架头尾），
// noVNC 随前端打包，wss 隧道与会话密码都走本站后端（见 internal/handler/user_vnc.go），
// 浏览器不接触任何上游地址。后台代管视图下 apiPrefix() 自动带上后台路径，
// 隧道/密码命中 /admin/services/{id}/* 镜像路由（见 internal/handler/admin_service_proxy.go）。
// 上游每次申请会话都会换 token，故拨号前先取一次密码预热同一会话。

const route = useRoute()
const serviceId = computed(() => String(route.params.id || ''))
const screenRef = ref<HTMLDivElement | null>(null)
const serviceName = ref('')
// 实例登录密码：用于在目标机登录界面「粘贴密码」
const instancePassword = ref('')
const statusText = ref('准备中…')
const statusType = ref<'success' | 'info' | 'warning' | 'danger'>('info')
const connected = ref(false)
const errorMsg = ref('')

let rfb: RFB | null = null
// 远端桌面名（noVNC desktopname 事件，形如 QEMU (kvm1342)），可能早于或晚于 connect 到达
let desktopName = ''

function setStatus(text: string, type: 'success' | 'info' | 'warning' | 'danger' = 'info') {
  statusText.value = text
  statusType.value = type
}

function markConnected() {
  setStatus(desktopName ? `已连接 · ${desktopName}` : '已连接', 'success')
}

function disconnect() {
  connected.value = false
  desktopName = ''
  const client = rfb
  rfb = null
  if (!client) return
  try {
    client.disconnect()
  } catch {
    /* 已断开：忽略 */
  }
}

async function loadMeta() {
  try {
    const detail = await fetchServiceDetail(serviceId.value)
    serviceName.value = detail.svc.name
    instancePassword.value = detail.host?.password || ''
  } catch {
    /* 只影响标题与「粘贴密码」，不阻断控制台连接 */
  }
}

function connect() {
  const target = screenRef.value
  if (!target) return
  errorMsg.value = ''
  setStatus('连接中…')
  try {
    const wsProto = location.protocol === 'https:' ? 'wss://' : 'ws://'
    const client = new RFB(target, `${wsProto}${location.host}${apiPrefix()}/services/${serviceId.value}/vnc-ws`)
    rfb = client
    const stale = () => rfb !== client
    client.scaleViewport = true
    client.addEventListener('connect', () => {
      if (stale()) return
      connected.value = true
      markConnected()
    })
    client.addEventListener('disconnect', (event) => {
      if (stale()) return
      connected.value = false
      const clean = (event as CustomEvent<{ clean: boolean }>).detail?.clean
      // 非正常断开必须给出重连入口：否则只剩一帧黑屏，用户无法判断发生了什么
      if (!clean) errorMsg.value = '连接异常断开，请重新连接'
      setStatus(clean ? '连接已断开' : '连接异常断开', clean ? 'info' : 'warning')
    })
    client.addEventListener('securityfailure', () => {
      if (stale()) return
      errorMsg.value = 'VNC 认证失败，请重新连接'
      setStatus('认证失败', 'danger')
    })
    client.addEventListener('credentialsrequired', () => {
      if (stale()) return
      void sendSessionPassword(client)
    })
    client.addEventListener('desktopname', (event) => {
      if (stale()) return
      const name = (event as CustomEvent<{ name: string }>).detail?.name
      if (!name) return
      desktopName = name
      if (connected.value) markConnected()
    })
  } catch (err) {
    errorMsg.value = `控制台初始化失败：${(err as Error).message || '未知错误'}`
    setStatus('初始化失败', 'danger')
  }
}

async function sendSessionPassword(client: RFB) {
  setStatus('获取会话密码…')
  try {
    const password = await fetchVncPass(serviceId.value)
    client.sendCredentials({ password })
    setStatus('认证中…')
  } catch (err) {
    errorMsg.value = (err as Error).message || '获取会话密码失败'
    setStatus('获取会话密码失败', 'warning')
  }
}

// 先取一次会话密码：既预检实例是否支持 VNC（不支持时后端返回 ok:0 + 原因），
// 又让 ws 拨号复用后端缓存的同一会话（上游 token 疑似一次性）。
async function start() {
  if (rfb) return
  errorMsg.value = ''
  try {
    await fetchVncPass(serviceId.value)
  } catch (err) {
    errorMsg.value = (err as Error).message || '该实例暂不支持 VNC 控制台'
    setStatus('不可用', 'warning')
    return
  }
  connect()
}

function reconnect() {
  disconnect()
  void start()
}

// 键名映射：字符 -> [XT scancode 键名, 该键的基键 keysym, 是否需要 Shift]。
// 全部走 QEMU 扩展键事件（scancode + keysym），物理键级输出，不受目标机 CapsLock 影响。
const KEYMAP: Record<string, [string, number, number?]> = {
  a: ['KeyA', 0x61], b: ['KeyB', 0x62], c: ['KeyC', 0x63], d: ['KeyD', 0x64], e: ['KeyE', 0x65],
  f: ['KeyF', 0x66], g: ['KeyG', 0x67], h: ['KeyH', 0x68], i: ['KeyI', 0x69], j: ['KeyJ', 0x6a],
  k: ['KeyK', 0x6b], l: ['KeyL', 0x6c], m: ['KeyM', 0x6d], n: ['KeyN', 0x6e], o: ['KeyO', 0x6f],
  p: ['KeyP', 0x70], q: ['KeyQ', 0x71], r: ['KeyR', 0x72], s: ['KeyS', 0x73], t: ['KeyT', 0x74],
  u: ['KeyU', 0x75], v: ['KeyV', 0x76], w: ['KeyW', 0x77], x: ['KeyX', 0x78], y: ['KeyY', 0x79], z: ['KeyZ', 0x7a],
  '1': ['Digit1', 0x31], '2': ['Digit2', 0x32], '3': ['Digit3', 0x33], '4': ['Digit4', 0x34],
  '5': ['Digit5', 0x35], '6': ['Digit6', 0x36], '7': ['Digit7', 0x37], '8': ['Digit8', 0x38],
  '9': ['Digit9', 0x39], '0': ['Digit0', 0x30],
  '!': ['Digit1', 0x21, 1], '@': ['Digit2', 0x40, 1], '#': ['Digit3', 0x23, 1], $: ['Digit4', 0x24, 1],
  '%': ['Digit5', 0x25, 1], '^': ['Digit6', 0x5e, 1], '&': ['Digit7', 0x26, 1], '*': ['Digit8', 0x2a, 1],
  '(': ['Digit9', 0x28, 1], ')': ['Digit0', 0x29, 1],
  '-': ['Minus', 0x2d], _: ['Minus', 0x5f, 1], '=': ['Equal', 0x3d], '+': ['Equal', 0x2b, 1],
  '[': ['BracketLeft', 0x5b], '{': ['BracketLeft', 0x7b, 1],
  ']': ['BracketRight', 0x5d], '}': ['BracketRight', 0x7d, 1],
  '\\': ['Backslash', 0x5c], '|': ['Backslash', 0x7c, 1],
  ';': ['Semicolon', 0x3b], ':': ['Semicolon', 0x3a, 1],
  "'": ['Quote', 0x27], '"': ['Quote', 0x22, 1],
  '`': ['Backquote', 0x60], '~': ['Backquote', 0x7e, 1],
  '.': ['Period', 0x2e], '>': ['Period', 0x3e, 1],
  '/': ['Slash', 0x2f], '?': ['Slash', 0x3f, 1],
}
const SHIFT_KEYSYM = 0xffe1

function sendKeys(text: string) {
  const client = rfb
  if (!client) return
  for (const ch of text) {
    const base = KEYMAP[ch.toLowerCase()]
    if (!base) continue
    let [code, keysym, shift] = base
    if (ch >= 'A' && ch <= 'Z') {
      keysym = ch.charCodeAt(0)
      shift = 1
    } else if (KEYMAP[ch]?.[2]) {
      // 本身就是 Shift 组合字符（如 '!'），改用该条目
      const shifted = KEYMAP[ch]
      code = shifted[0]
      keysym = shifted[1]
      shift = shifted[2]
    }
    if (shift) client.sendKey(SHIFT_KEYSYM, 'ShiftLeft', true)
    client.sendKey(keysym, code, true)
    client.sendKey(keysym, code, false)
    if (shift) client.sendKey(SHIFT_KEYSYM, 'ShiftLeft', false)
  }
}

function swapCase(text: string): string {
  return text.replace(/[a-zA-Z]/g, (c) => (c === c.toLowerCase() ? c.toUpperCase() : c.toLowerCase()))
}

function sendCtrlAltDel() {
  if (!rfb) return
  rfb.focus()
  rfb.sendCtrlAltDel()
}

async function pasteText() {
  const client = rfb
  if (!client) return
  const input = await ElMessageBox.prompt('输入要发送到控制台的文本（不会发送回车键）', '粘贴文本', {
    confirmButtonText: '发送',
    cancelButtonText: '取消',
    inputValue: '',
  }).catch(() => null)
  if (!input || !input.value) return
  client.focus()
  sendKeys(input.value)
}

// invert：目标机 CapsLock 状态未知，大小写相反时用「反转重试」
function pastePassword(invert: boolean) {
  const client = rfb
  if (!client) return
  if (!instancePassword.value) {
    ElMessage.warning('未获取到实例密码，请先在实例详情页刷新信息')
    return
  }
  client.focus()
  sendKeys(invert ? swapCase(instancePassword.value) : instancePassword.value)
  if (!invert) ElMessage.info('密码已发送。若目标机显示的大小写相反，请点「反转重试」')
}

onMounted(async () => {
  await loadMeta()
  void start()
})

onBeforeUnmount(disconnect)

// 同一路由切换到别的实例时组件复用，需断开旧隧道后重连
watch(serviceId, () => {
  disconnect()
  serviceName.value = ''
  instancePassword.value = ''
  void loadMeta()
  void start()
})
</script>

<template>
  <div class="vnc-console">
    <header class="vnc-bar">
      <span class="vnc-bar__title">VNC 控制台</span>
      <span v-if="serviceName" class="vnc-bar__name">{{ serviceName }}</span>

      <div class="vnc-bar__actions">
        <span class="vnc-bar__status" :class="`is-${statusType}`">{{ statusText }}</span>
        <button type="button" class="vnc-bar__btn" :disabled="!connected" @click="pastePassword(false)">粘贴密码</button>
        <button type="button" class="vnc-bar__btn" :disabled="!connected" @click="pastePassword(true)">反转重试</button>
        <button type="button" class="vnc-bar__btn" :disabled="!connected" @click="pasteText">粘贴文本</button>
        <button type="button" class="vnc-bar__btn" :disabled="!connected" @click="sendCtrlAltDel">Ctrl+Alt+Del</button>
        <button type="button" class="vnc-bar__btn vnc-bar__btn--primary" @click="reconnect">重新连接</button>
      </div>
    </header>

    <main class="vnc-stage">
      <div ref="screenRef" class="vnc-screen"></div>
      <div v-if="errorMsg" class="vnc-mask">
        <el-icon class="vnc-mask__icon" size="24"><WarningFilled /></el-icon>
        <p>{{ errorMsg }}</p>
        <button type="button" class="vnc-bar__btn vnc-bar__btn--primary" @click="reconnect">重新连接</button>
      </div>
    </main>
  </div>
</template>

<style scoped>
.vnc-console {
  display: flex;
  flex-direction: column;
  width: 100vw;
  height: 100vh;
  height: 100dvh;
  overflow: hidden;
  background: #0b1220;
}

.vnc-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 44px;
  padding: 0 12px;
  color: #e5e7eb;
  font-size: 12.5px;
  background: #1f2937;
  border-bottom: 1px solid #374151;
}

.vnc-bar__title {
  flex: 0 0 auto;
  font-weight: 600;
}

.vnc-bar__name {
  overflow: hidden;
  color: #9ca3af;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.vnc-bar__actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-left: auto;
}

.vnc-bar__status {
  color: #9ca3af;
}

.vnc-bar__status.is-success {
  color: #34d399;
}

.vnc-bar__status.is-warning {
  color: #fbbf24;
}

.vnc-bar__status.is-danger {
  color: #f87171;
}

.vnc-bar__btn {
  padding: 4px 10px;
  color: #d1d5db;
  font-size: 12.5px;
  font-family: inherit;
  background: transparent;
  border: 1px solid #4b5563;
  border-radius: 6px;
  cursor: pointer;
  transition: color 0.16s ease, background-color 0.16s ease, border-color 0.16s ease;
}

.vnc-bar__btn:hover:not(:disabled) {
  color: #fff;
  background: #374151;
}

.vnc-bar__btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.vnc-bar__btn--primary {
  color: #fff;
  background: #2563eb;
  border-color: #2563eb;
}

.vnc-bar__btn--primary:hover:not(:disabled) {
  background: #1d4ed8;
  border-color: #1d4ed8;
}

.vnc-stage {
  position: relative;
  flex: 1;
  min-height: 0;
  background: #000;
}

.vnc-screen {
  width: 100%;
  height: 100%;
}

.vnc-mask {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 24px;
  text-align: center;
  background: rgba(11, 18, 32, 0.94);
}

.vnc-mask__icon {
  color: #f59e0b;
}

.vnc-mask p {
  margin: 0;
  color: #e5e7eb;
  font-size: 13px;
}

@media (max-width: 800px) {
  .vnc-bar {
    flex-wrap: wrap;
    min-height: 0;
    padding: 6px 10px;
  }
}
</style>
