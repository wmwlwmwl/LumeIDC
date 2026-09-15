<script setup lang="ts">
/**
 * 外部人机验证（geetest / vaptcha / corptcha）。
 *
 * 与 SSR 版 assets/js/auth-captcha.js 使用同一后端契约：
 *   GET /auth/captcha/config?scene=<scene> → CaptchaPublicConfig
 *   验证通过后把 provider 专有字段写回表单，字段名与后端 captchaCheckVals 读取的一致
 *   （captcha_token / lot_number / captcha_output / pass_token / gen_time / knock / dfu / ip）。
 */
import { ref, shallowRef, onMounted, onBeforeUnmount } from 'vue'
import { http } from '../http/index'

const props = defineProps<{ scene: string }>()
const emit = defineEmits<{
  (e: 'update:fields', v: Record<string, string>): void
  (e: 'update:required', v: boolean): void
}>()

interface CaptchaConfig {
  enabled: boolean
  provider?: string
  public_id?: string
  sdk_url?: string
  script_url?: string
  api_base_url?: string
  purpose?: string
  scene: string
}

const box = ref<HTMLElement | null>(null)
const error = ref('')
const ready = ref(false)

// 当前生效的 provider：模板据此决定容器占位与是否需要自绘触发按钮
const provider = ref('')
const hint = ref('')
const vpDone = ref(false)
// 挑战进行中：防止重复触发导致 Vaptcha 实例进入 closed 状态
const vpBusy = ref(false)
// 第三方实例用 shallowRef：避免被 Vue 深度代理后内部状态判断出错
const vpWidget = shallowRef<VpWidget | null>(null)

// 外部验证码字段名（与后端白名单一致）
const FIELD_NAMES = ['captcha_token', 'lot_number', 'captcha_output', 'pass_token', 'gen_time', 'knock', 'dfu', 'ip'] as const
const fields = ref<Record<string, string>>({})

function clearFields() {
  fields.value = {}
  emit('update:fields', {})
}
function setFields(vals: Record<string, string>) {
  const next: Record<string, string> = {}
  for (const name of FIELD_NAMES) {
    const v = vals[name]
    if (v !== undefined && v !== null) next[name] = String(v)
  }
  fields.value = next
  emit('update:fields', { ...next })
  ready.value = true
}

// SDK 脚本按 URL 缓存，避免重复加载
const scriptCache = new Map<string, Promise<void>>()
function loadScript(url: string): Promise<void> {
  const cached = scriptCache.get(url)
  if (cached) return cached
  const task = new Promise<void>((resolve, reject) => {
    const s = document.createElement('script')
    s.src = url
    s.async = true
    s.referrerPolicy = 'no-referrer'
    s.onload = () => resolve()
    s.onerror = () => {
      // 失败不入缓存：允许下次触发时重试加载
      scriptCache.delete(url)
      reject(new Error('验证码 SDK 加载失败'))
    }
    document.head.appendChild(s)
  })
  scriptCache.set(url, task)
  return task
}

type W = Window & {
  initGeetest4?: (opts: Record<string, unknown>, cb: (i: GtInstance) => void) => void
  vaptcha?: (opts: Record<string, unknown>) => Promise<VpWidget>
  Corptcha?: { render: (el: HTMLElement, opts: Record<string, unknown>) => { execute?: () => void } }
}
interface GtInstance {
  appendTo: (el: HTMLElement) => void
  onSuccess: (cb: () => void) => void
  onError: (cb: () => void) => void
  onClose: (cb: () => void) => void
  getValidate: () => Record<string, string>
}
interface VpWidget {
  validate: () => Promise<void>
  getVerifyResult: () => { token?: string; knock?: string; dfu?: string; ip?: string }
}

const w = window as W

async function initGeetest(el: HTMLElement, cfg: CaptchaConfig) {
  if (typeof w.initGeetest4 !== 'function') throw new Error('Geetest SDK 初始化失败')
  w.initGeetest4({ captchaId: cfg.public_id, product: 'bind' }, (instance) => {
    instance.appendTo(el)
    instance.onSuccess(() => setFields(instance.getValidate()))
    instance.onError(() => { clearFields(); ready.value = false })
    instance.onClose(() => { clearFields(); ready.value = false })
  })
}

// SDK 只认 container 挂载点，不注入任何 UI；触发按钮由模板渲染
let vpMount: HTMLElement | null = null
let vpCfg: CaptchaConfig | null = null

async function mountVaptcha() {
  if (!vpMount || !vpCfg) return
  if (typeof w.vaptcha !== 'function') throw new Error('Vaptcha SDK 初始化失败')
  // 每次都用全新的挂载点建实例：旧实例在挑战失败/关闭后不再接受 validate()
  const mount = document.createElement('div')
  vpMount.replaceChildren(mount)
  const widget = await w.vaptcha({ vid: vpCfg.public_id, container: mount, mode: 'click' })
  if (!widget || typeof widget.validate !== 'function' || typeof widget.getVerifyResult !== 'function') {
    throw new Error('Vaptcha SDK 版本不受支持')
  }
  vpWidget.value = widget
}

async function initVaptcha(el: HTMLElement, cfg: CaptchaConfig) {
  vpMount = el
  vpCfg = cfg
  await mountVaptcha()
}

async function runVaptcha() {
  const widget = vpWidget.value
  // vpBusy 期间忽略重复点击：挑战未结束就再次 validate() 会把实例打成 closed 状态
  if (!widget || vpDone.value || vpBusy.value) return
  vpBusy.value = true
  hint.value = ''
  try {
    await widget.validate()
    const r = widget.getVerifyResult()
    if (!r || !r.token || !r.knock) throw new Error('请先完成行为验证')
    setFields({ captcha_token: r.token, knock: r.knock, dfu: r.dfu || '', ip: r.ip || '' })
    vpDone.value = true
  } catch {
    clearFields()
    ready.value = false
    vpDone.value = false
    hint.value = '行为验证未通过，请重试'
    // 旧实例已不可用，重建后下次点击才能正常拉起挑战
    vpWidget.value = null
    try {
      await mountVaptcha()
    } catch {
      hint.value = '人机验证初始化失败，请刷新页面后重试'
    }
  } finally {
    vpBusy.value = false
  }
}

async function initCorptcha(el: HTMLElement, cfg: CaptchaConfig) {
  if (!w.Corptcha || typeof w.Corptcha.render !== 'function') throw new Error('Corptcha SDK 初始化失败')
  const mount = document.createElement('div')
  el.append(mount)
  w.Corptcha.render(mount, {
    siteKey: cfg.public_id,
    apiBaseUrl: cfg.api_base_url,
    purpose: cfg.purpose || cfg.scene || props.scene,
    autoExecute: true,
    onSuccess: (token: string) => setFields({ captcha_token: token }),
    onError: () => { clearFields(); ready.value = false },
    onExpired: () => { clearFields(); ready.value = false },
  })
}

async function init() {
  emit('update:required', false)
  try {
    const cfg = await http.get<CaptchaConfig & { ok?: number }>('/auth/captcha/config', { scene: props.scene })
    const conf = cfg as unknown as CaptchaConfig
    if (!conf.enabled) return
    emit('update:required', true)
    const el = box.value
    if (!el) return
    el.replaceChildren()
    const sdk = conf.script_url || conf.sdk_url
    if (!sdk) throw new Error('验证码 SDK 地址未配置')
    await loadScript(sdk)
    // 先定型 provider：vaptcha 的触发按钮随模板立即渲染，SDK 就绪前保持禁用
    provider.value = conf.provider || ''
    if (conf.provider === 'geetest') await initGeetest(el, conf)
    else if (conf.provider === 'vaptcha') await initVaptcha(el, conf)
    else if (conf.provider === 'corptcha') await initCorptcha(el, conf)
    else throw new Error('未知验证码 provider')
  } catch (e) {
    provider.value = ''
    emit('update:required', true)
    error.value = (e as Error).message || '外部人机验证加载失败，请刷新后重试'
  }
}

onMounted(init)
onBeforeUnmount(() => {
  clearFields()
  vpWidget.value = null
  vpMount = null
  vpCfg = null
})
</script>

<template>
  <div class="ext-captcha">
    <div
      ref="box"
      class="ext-captcha__box"
      :class="provider ? `ext-captcha__box--${provider}` : ''"
      aria-live="polite"
    ></div>
    <el-button
      v-if="provider === 'vaptcha'"
      class="ext-captcha__trigger"
      size="large"
      :disabled="vpDone || !vpWidget"
      @click="runVaptcha"
    >
      <template #icon>
        <ArtSvgIcon icon="ri:shield-check-line" />
      </template>
      {{ vpDone ? '验证通过' : '开始人机验证' }}
    </el-button>
    <p v-if="hint" class="ext-captcha__hint" role="alert">{{ hint }}</p>
    <p v-if="error" class="ext-captcha__error" role="alert">{{ error }}</p>
  </div>
</template>

<style scoped>
.ext-captcha {
  width: 100%;
  /* 与 el-form-item 默认下间距一致（Element Plus 18px），夹在表单项之间时节奏一致 */
  margin-bottom: 18px;
}

/* 第三方验证组件（geetest / vaptcha / corptcha）由各自 SDK 自绘皮肤，
   这里只负责把容器对齐页面宽度并控制圆角/溢出，保持与表单观感一致。 */
.ext-captcha__box {
  width: 100%;
  overflow: hidden;
  border-radius: 8px;
}

/* 只有 geetest / corptcha 会在容器内直接绘制组件，需要预留高度；
   vaptcha 的容器只是 SDK 挂载点，不占位（触发按钮另由模板渲染）。 */
.ext-captcha__box--geetest,
.ext-captcha__box--corptcha {
  min-height: 56px;
}

.ext-captcha__box :deep(iframe),
.ext-captcha__box :deep(img) {
  max-width: 100%;
}

/* vaptcha 触发按钮铺满整行，与上下输入框、提交按钮等宽 */
.ext-captcha__trigger {
  width: 100%;
}

.ext-captcha__hint,
.ext-captcha__error {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--el-color-danger);
}
</style>