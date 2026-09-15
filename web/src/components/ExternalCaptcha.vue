<script setup lang="ts">
/**
 * 外部人机验证（geetest / vaptcha / corptcha）。
 *
 * 与 SSR 版 assets/js/auth-captcha.js 使用同一后端契约：
 *   GET /auth/captcha/config?scene=<scene> → CaptchaPublicConfig
 *   验证通过后把 provider 专有字段写回表单，字段名与后端 captchaCheckVals 读取的一致
 *   （captcha_token / lot_number / captcha_output / pass_token / gen_time / knock / dfu / ip）。
 */
import { ref, onMounted, onBeforeUnmount } from 'vue'
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

async function initVaptcha(el: HTMLElement, cfg: CaptchaConfig) {
  if (typeof w.vaptcha !== 'function') throw new Error('Vaptcha SDK 初始化失败')
  const mount = document.createElement('div')
  const hint = document.createElement('div')
  hint.className = 'mt-1 text-xs text-[var(--el-color-danger)]'
  const button = document.createElement('button')
  button.type = 'button'
  button.className = 'rounded-custom-sm border border-[var(--art-card-border)] px-3 py-1.5 text-sm'
  button.textContent = '开始人机验证'
  el.append(mount, button, hint)
  const widget = await w.vaptcha({ vid: cfg.public_id, container: mount, mode: 'click' })
  if (!widget || typeof widget.validate !== 'function' || typeof widget.getVerifyResult !== 'function') {
    throw new Error('Vaptcha SDK 版本不受支持')
  }
  button.addEventListener('click', async () => {
    hint.textContent = ''
    try {
      await widget.validate()
      const r = widget.getVerifyResult()
      if (!r || !r.token || !r.knock) throw new Error('请先完成行为验证')
      setFields({ captcha_token: r.token, knock: r.knock, dfu: r.dfu || '', ip: r.ip || '' })
      button.textContent = '验证通过'
      button.disabled = true
    } catch {
      clearFields()
      ready.value = false
      button.textContent = '开始人机验证'
      button.disabled = false
      hint.textContent = '行为验证未通过，请重试'
    }
  })
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
    if (conf.provider === 'geetest') await initGeetest(el, conf)
    else if (conf.provider === 'vaptcha') await initVaptcha(el, conf)
    else if (conf.provider === 'corptcha') await initCorptcha(el, conf)
    else throw new Error('未知验证码 provider')
  } catch (e) {
    emit('update:required', true)
    error.value = (e as Error).message || '外部人机验证加载失败，请刷新后重试'
  }
}

onMounted(init)
onBeforeUnmount(() => {
  clearFields()
})
</script>

<template>
  <div class="ext-captcha">
    <div ref="box" class="ext-captcha__box" aria-live="polite"></div>
    <p v-if="error" class="ext-captcha__error">{{ error }}</p>
  </div>
</template>

<style scoped>
.ext-captcha {
  width: 100%;
}

/* 第三方验证组件（geetest / vaptcha / corptcha）由各自 SDK 自绘皮肤，
   这里只负责把容器对齐页面宽度并控制圆角/溢出，保持与表单观感一致。 */
.ext-captcha__box {
  width: 100%;
  min-height: 56px;
  overflow: hidden;
  border-radius: 8px;
}

.ext-captcha__box :deep(iframe),
.ext-captcha__box :deep(img) {
  max-width: 100%;
}

.ext-captcha__error {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--el-color-danger);
}
</style>