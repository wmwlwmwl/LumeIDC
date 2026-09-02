(() => {
  'use strict'
  const scripts = new Map()
  const RESEND_SECONDS = 60
  const resendTimers = new WeakMap()

  function startResendCountdown(button) {
    if (resendTimers.has(button)) return
    if (!button.dataset.resendLabel) button.dataset.resendLabel = button.textContent
    const until = Date.now() + RESEND_SECONDS * 1000
    button.dataset.cooldownUntil = String(until)
    const timer = window.setInterval(() => {
      const remaining = Math.max(0, Math.ceil((until - Date.now()) / 1000))
      if (remaining <= 0) {
        window.clearInterval(timer)
        resendTimers.delete(button)
        button.disabled = false
        button.textContent = button.dataset.resendLabel
        return
      }
      button.textContent = remaining + '秒后可重新发送'
      button.disabled = true
    }, 250)
    resendTimers.set(button, timer)
  }

  function enforceResendCooldown(button) {
    if (button && button.dataset.cooldownUntil && Number(button.dataset.cooldownUntil) > Date.now()) {
      button.disabled = true
      if (!resendTimers.has(button)) startResendCountdown(button)
    }
  }

  function loadScript(url) {
    if (scripts.has(url)) return scripts.get(url)
    const task = new Promise((resolve, reject) => {
      const script = document.createElement('script')
      script.src = url
      script.async = true
      script.referrerPolicy = 'no-referrer'
      script.onload = () => resolve()
      script.onerror = () => reject(new Error('验证码 SDK 加载失败'))
      document.head.appendChild(script)
    })
    scripts.set(url, task)
    return task
  }

  function formOf(box) { return box.closest('form') }

  function input(form, name) {
    let el = form.querySelector(`[name="${name}"]`)
    if (!el) {
      el = document.createElement('input')
      el.type = 'hidden'
      el.name = name
      form.appendChild(el)
    }
    return el
  }

  function clearExternal(form) {
    ;['captcha_token', 'lot_number', 'captcha_output', 'pass_token', 'gen_time', 'knock', 'dfu', 'ip'].forEach(name => {
      const el = form.querySelector(`[name="${name}"]`)
      if (el) el.value = ''
    })
  }

  function setFields(form, values) {
    clearExternal(form)
    Object.entries(values || {}).forEach(([name, value]) => {
      if (value !== undefined && value !== null) input(form, name).value = String(value)
    })
    form.dataset.externalCaptchaReady = '1'
  }

  function ready(form) {
    return form.dataset.externalCaptchaRequired !== '1' || form.dataset.externalCaptchaReady === '1'
  }

  function markError(box, message) {
    box.textContent = message
    box.dataset.externalCaptchaError = '1'
  }

  async function initGeetest(box, config) {
    if (typeof window.initGeetest4 !== 'function') throw new Error('Geetest SDK 初始化失败')
    const form = formOf(box)
    window.initGeetest4({ captchaId: config.public_id, product: 'bind' }, instance => {
      instance.appendTo(box)
      instance.onSuccess(() => setFields(form, instance.getValidate()))
      instance.onError(() => { clearExternal(form); form.dataset.externalCaptchaReady = '0' })
      instance.onClose(() => { clearExternal(form); form.dataset.externalCaptchaReady = '0' })
    })
  }

  async function initVaptcha(box, config) {
    const form = formOf(box)
    if (typeof window.vaptcha !== 'function') throw new Error('Vaptcha SDK 初始化失败')
    const mount = document.createElement('div')
    const hint = document.createElement('div')
    hint.className = 'captcha-hint'
    const button = document.createElement('button')
    button.type = 'button'
    button.className = 'ui-btn ui-btn-secondary'
    button.textContent = '开始人机验证'
    box.append(mount, button, hint)
    const widget = await window.vaptcha({ vid: config.public_id, container: mount, mode: 'click' })
    if (!widget || typeof widget.validate !== 'function' || typeof widget.getVerifyResult !== 'function') throw new Error('Vaptcha SDK 版本不受支持')
    button.addEventListener('click', async () => {
      hint.textContent = ''
      hint.classList.remove('is-error')
      try {
        await widget.validate()
        const result = widget.getVerifyResult()
        if (!result || !result.token || !result.knock) throw new Error('请先完成行为验证')
        setFields(form, { captcha_token: result.token, knock: result.knock, dfu: result.dfu || '', ip: result.ip || '' })
        button.textContent = '验证通过'
        button.classList.remove('ui-btn-secondary')
        button.disabled = true
      } catch (_) {
        clearExternal(form)
        form.dataset.externalCaptchaReady = '0'
        button.textContent = '开始人机验证'
        button.classList.add('ui-btn-secondary')
        button.disabled = false
        hint.textContent = '行为验证未通过，请重试'
        hint.classList.add('is-error')
      }
    })
  }

  async function initCorptcha(box, config) {
    const form = formOf(box)
    if (!window.Corptcha || typeof window.Corptcha.render !== 'function') throw new Error('Corptcha SDK 初始化失败')
    const mount = document.createElement('div')
    box.append(mount)
    const widget = window.Corptcha.render(mount, {
      siteKey: config.public_id,
      apiBaseUrl: config.api_base_url,
      purpose: config.purpose || config.scene || 'login',
      autoExecute: true,
      onSuccess: token => setFields(form, { captcha_token: token }),
      onError: () => { clearExternal(form); form.dataset.externalCaptchaReady = '0' },
      onExpired: () => { clearExternal(form); form.dataset.externalCaptchaReady = '0' }
    })
    if (!widget || typeof widget.execute !== 'function') throw new Error('Corptcha SDK 版本不受支持')
  }

  async function initExternal(box) {
    const form = formOf(box)
    const scene = box.dataset.externalScene
    if (!form || !scene || scene === 'dynamic') return
    form.dataset.externalCaptchaReady = '0'
    box.replaceChildren()
    try {
      const response = await fetch(`/auth/captcha/config?scene=${encodeURIComponent(scene)}`, { credentials: 'same-origin', cache: 'no-store' })
      if (!response.ok) throw new Error('验证码配置读取失败')
      const config = await response.json()
      if (!config.enabled) { box.hidden = true; form.dataset.externalCaptchaRequired = '0'; return }
      box.hidden = false
      form.dataset.externalCaptchaRequired = '1'
      await loadScript(config.sdk_url)
      if (config.provider === 'geetest') await initGeetest(box, config)
      else if (config.provider === 'vaptcha') await initVaptcha(box, config)
      else if (config.provider === 'corptcha') await initCorptcha(box, config)
      else throw new Error('未知验证码 provider')
    } catch (error) {
      form.dataset.externalCaptchaRequired = '1'
      form.dataset.externalCaptchaReady = '0'
      markError(box, '外部人机验证加载失败，请刷新后重试')
    }
  }

  async function loadLocal(box) {
    const scene = box.dataset.captchaScene
    if (!scene || scene === 'dynamic') return
    const response = await fetch(`/captcha?scene=${encodeURIComponent(scene)}`, { credentials: 'same-origin', cache: 'no-store' })
    const data = await response.json()
    if (!data.enabled) return
    box.querySelector('[data-captcha-image]').src = data.image
    box.querySelector('[data-captcha-id]').value = data.id
  }

  function initializeLocal(box) {
    loadLocal(box)
    const refresh = box.querySelector('[data-captcha-refresh]')
    if (refresh) refresh.addEventListener('click', () => loadLocal(box))
  }

  function currentMode(form) {
    return form.querySelector('[name="mode"]')?.value || document.querySelector('#register-mode')?.value || 'email'
  }

  function setMode(form) {
    const mode = currentMode(form)
    const emailBox = form.querySelector('#register-email-box')
    const phoneBox = form.querySelector('#register-phone-box')
    if (emailBox) emailBox.style.display = mode === 'email' ? 'block' : 'none'
    if (phoneBox) phoneBox.style.display = mode === 'phone' ? 'block' : 'none'
    const email = form.querySelector('[name="email"]')
    const phone = form.querySelector('[name="phone"]')
    if (email) email.required = mode === 'email'
    if (phone) phone.required = mode === 'phone'

    const dynamicCode = form.querySelector('#register-code-box')
    const policy = form.querySelector('#register-policy')
    const emailNeedsCode = policy?.dataset.emailVerification === 'true'
    const phoneNeedsCode = policy?.dataset.phoneVerification === 'true'
    const requiresCode = mode === 'phone' ? phoneNeedsCode : emailNeedsCode
    if (dynamicCode) {
      dynamicCode.style.display = requiresCode ? 'block' : 'none'
      dynamicCode.querySelectorAll('input,button').forEach(el => { el.disabled = !requiresCode })
      const code = dynamicCode.querySelector('[name="code"]')
      if (code) code.required = requiresCode
      const localCode = dynamicCode.querySelector('[data-captcha-answer]')
      if (localCode) localCode.required = requiresCode
      const codeCaptcha = dynamicCode.querySelector('[data-captcha-scene]')
      if (codeCaptcha) {
        if (requiresCode) {
          const captchaScene = mode === 'phone' ? 'phone_code' : 'email_code'
          if (codeCaptcha.dataset.captchaScene !== captchaScene) {
            codeCaptcha.dataset.captchaScene = captchaScene
            loadLocal(codeCaptcha)
          }
        } else {
          codeCaptcha.dataset.captchaScene = 'dynamic'
        }
      }
    }
    const sendCodeButton = dynamicCode ? dynamicCode.querySelector('button[onclick*="requestRegisterCode"]') : null
    if (sendCodeButton) enforceResendCooldown(sendCodeButton)
    const directLocal = form.querySelector('[data-register-direct-captcha]')
    if (directLocal) {
      directLocal.style.display = requiresCode ? 'none' : 'block'
      directLocal.querySelectorAll('input,button').forEach(el => { el.disabled = requiresCode })
    }
    const dynamicExternal = form.querySelector('[data-external-dynamic] [data-external-captcha]')
    if (dynamicExternal) {
      const externalWrapper = form.querySelector('[data-external-dynamic]')
      const emailNeedsCode = externalWrapper.dataset.emailVerification === 'true'
      const phoneNeedsCode = externalWrapper.dataset.phoneVerification === 'true'
      const requiresCode = mode === 'email' ? emailNeedsCode : phoneNeedsCode
      clearExternal(form)
      dynamicExternal.hidden = requiresCode
      form.dataset.externalCaptchaRequired = requiresCode ? '0' : '1'
      dynamicExternal.dataset.externalScene = requiresCode ? 'disabled' : 'register'
      if (requiresCode) dynamicExternal.replaceChildren()
      else initExternal(dynamicExternal)
    }
  }

  function resetExternalCaptcha(form) {
    form.querySelectorAll('[data-external-captcha]').forEach(box => {
      const button = box.querySelector('button')
      if (!button) return
      button.textContent = '开始人机验证'
      button.classList.add('ui-btn-secondary')
      button.disabled = false
    })
  }

  async function requestRegisterCode(button) {
    if (button.disabled) return
    const form = button.closest('form')
    const data = new FormData(form)
    data.set('mode', currentMode(form))
    const original = button.textContent
    button.disabled = true
    let ok = false
    try {
      const response = await fetch('/auth/register-code', { method: 'POST', body: data, credentials: 'same-origin' })
      ok = response.status === 202
      alert(await response.text())
      clearExternal(form)
      form.dataset.externalCaptchaReady = '0'
      resetExternalCaptcha(form)
      form.querySelectorAll('[data-captcha-scene]').forEach(loadLocal)
    } catch (_) {
      button.textContent = original
    } finally {
      if (ok) startResendCountdown(button)
      else if (!resendTimers.has(button)) button.disabled = false
    }
  }

  function bindForm(form) {
    form.addEventListener('submit', event => {
      if (!ready(form)) {
        event.preventDefault()
        alert('请先完成外部人机验证')
      }
    })
  }

  document.addEventListener('DOMContentLoaded', () => {
    document.querySelectorAll('[data-captcha-scene]').forEach(initializeLocal)
    document.querySelectorAll('form').forEach(bindForm)
    document.querySelectorAll('[data-external-captcha]').forEach(box => {
      if (!box.closest('[data-external-dynamic]')) initExternal(box)
    })
    const register = document.querySelector('#registerForm')
    if (register) {
      const mode = register.querySelector('#register-mode')
      if (mode) mode.addEventListener('change', () => setMode(register))
      setMode(register)
    }
    document.querySelectorAll('[data-register-code-button]').forEach(button => button.addEventListener('click', () => requestRegisterCode(button)))
  })

  window.requestRegisterCode = requestRegisterCode
})()
