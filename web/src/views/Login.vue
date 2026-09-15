<script setup lang="ts">
import { ref, reactive, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { User, Lock, ArrowRight, Refresh } from '@element-plus/icons-vue'
import {
  login,
  fetchCaptcha,
  sendPhoneCode,
  loginByPhoneCode,
  type CaptchaData,
} from '../api/store'
import { useSession } from '../http/session'
import ExternalCaptcha from '../components/ExternalCaptcha.vue'
import PublicAuthCard from '@/components/public/PublicAuthCard.vue'
import PhoneInput from '@/components/phone/PhoneInput.vue'

const router = useRouter()
const route = useRoute()
const session = useSession()

const smsEnabled = computed(() => session.auth.phone_otp_login)
const mode = ref<'password' | 'phone'>('password')

function nextPath(): string {
  const n = String(route.query.next || '/')
  return n.startsWith('/') ? n : '/'
}

/* ---------------- 密码登录（邮箱 / 手机号子 Tab） ---------------- */
const subMode = ref<'email' | 'phone'>('email')
const formRef = ref<FormInstance>()
const form = reactive({ email: '', phone: '', password: '', captchaAnswer: '' })
const pwdPhoneInputRef = ref<InstanceType<typeof PhoneInput>>()
const captcha = ref<CaptchaData>({ enabled: false })
const captchaError = ref(false)
const loading = ref(false)
const extFields = ref<Record<string, string>>({})
const extRequired = ref(false)

const rules: FormRules = {
  email: [
    {
      validator: (_rule, value, callback) => {
        if (subMode.value !== 'email') return callback()
        if (!value) return callback(new Error('请输入邮箱'))
        if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)) return callback(new Error('邮箱格式不正确'))
        callback()
      },
      trigger: 'blur',
    },
  ],
  phone: [
    {
      validator: (_rule, value, callback) => {
        if (subMode.value !== 'phone') return callback()
        if (!value) return callback(new Error('请输入手机号'))
        callback()
      },
      trigger: 'blur',
    },
  ],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
  captchaAnswer: [
    {
      validator: (_rule, value, callback) => {
        if (captcha.value.enabled && !value) callback(new Error('请输入图形验证码'))
        else callback()
      },
      trigger: 'blur',
    },
  ],
}

async function loadCaptcha() {
  captchaError.value = false
  try {
    captcha.value = await fetchCaptcha('login')
    form.captchaAnswer = ''
  } catch {
    captcha.value = { enabled: false }
    captchaError.value = true
  }
}

async function submit() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  if (extRequired.value && !extFields.value.captcha_token) {
    ElMessage.warning('请先完成人机验证')
    return
  }
  const loginId = subMode.value === 'email' ? form.email.trim() : form.phone
  if (subMode.value === 'phone') {
    const r = pwdPhoneInputRef.value?.check()
    if (!r?.ok) {
      ElMessage.warning(r?.msg || '手机号格式不正确')
      return
    }
  }
  loading.value = true
  try {
    const body: Record<string, string> = {
      email: loginId,
      password: form.password,
      next: String(route.query.next || '/'),
    }
    if (captcha.value.enabled)
      Object.assign(body, { captcha_id: captcha.value.id || '', captcha_answer: form.captchaAnswer })
    Object.assign(body, extFields.value)
    await login(body)
    await import('../http/session').then((m) => m.loadSession({ force: true }))
    router.replace(nextPath())
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '登录失败')
    if (captcha.value.enabled) await loadCaptcha()
  } finally {
    loading.value = false
  }
}

/* ---------------- 短信验证码登录 ---------------- */
const phoneFormRef = ref<FormInstance>()
const phoneForm = reactive({ phone: '', code: '', captchaAnswer: '' })
const phoneInputRef = ref<InstanceType<typeof PhoneInput>>()
const phoneCaptcha = ref<CaptchaData>({ enabled: false })
const phoneExtFields = ref<Record<string, string>>({})
const phoneExtRequired = ref(false)
const phoneSending = ref(false)
const phoneLoading = ref(false)
const cooldown = ref(0)
let cooldownTimer: ReturnType<typeof setInterval> | null = null

const phoneRules: FormRules = {
  phone: [{ required: true, message: '请输入手机号', trigger: 'blur' }],
  code: [{ required: true, message: '请输入短信验证码', trigger: 'blur' }],
  captchaAnswer: [
    {
      validator: (_rule, value, callback) => {
        if (phoneCaptcha.value.enabled && !value) callback(new Error('请输入图形验证码'))
        else callback()
      },
      trigger: 'blur',
    },
  ],
}

async function loadPhoneCaptcha() {
  // 外部行为验证启用时不加载本地图形码（后端同样外部优先）
  if (session.auth.external_captcha_phone_login) {
    phoneCaptcha.value = { enabled: false }
    return
  }
  try {
    phoneCaptcha.value = await fetchCaptcha('phone_login_code')
    phoneForm.captchaAnswer = ''
  } catch {
    phoneCaptcha.value = { enabled: false }
  }
}

function startCooldown() {
  cooldown.value = 60
  if (cooldownTimer) clearInterval(cooldownTimer)
  cooldownTimer = setInterval(() => {
    cooldown.value -= 1
    if (cooldown.value <= 0 && cooldownTimer) {
      clearInterval(cooldownTimer)
      cooldownTimer = null
    }
  }, 1000)
}

async function sendCode() {
  if (!phoneForm.phone) {
    ElMessage.warning('请先输入手机号')
    return
  }
  const r = phoneInputRef.value?.check()
  if (!r?.ok) {
    ElMessage.warning(r?.msg || '手机号格式不正确')
    return
  }
  if (phoneCaptcha.value.enabled && !phoneForm.captchaAnswer) {
    ElMessage.warning('请输入图形验证码')
    return
  }
  if (phoneExtRequired.value && !phoneExtFields.value.captcha_token) {
    ElMessage.warning('请先完成人机验证')
    return
  }
  phoneSending.value = true
  try {
    const extra: Record<string, string> = { ...phoneExtFields.value }
    if (phoneCaptcha.value.enabled)
      Object.assign(extra, {
        captcha_id: phoneCaptcha.value.id || '',
        captcha_answer: phoneForm.captchaAnswer,
      })
    await sendPhoneCode(phoneForm.phone, extra)
    ElMessage.success('验证码已发送，请查收短信')
    startCooldown()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '发送失败')
    await loadPhoneCaptcha()
  } finally {
    phoneSending.value = false
  }
}

async function submitPhone() {
  const valid = await phoneFormRef.value?.validate().catch(() => false)
  if (!valid) return
  if (phoneExtRequired.value && !phoneExtFields.value.captcha_token) {
    ElMessage.warning('请先完成人机验证')
    return
  }
  phoneLoading.value = true
  try {
    await loginByPhoneCode(phoneForm.phone, phoneForm.code, String(route.query.next || '/'))
    await import('../http/session').then((m) => m.loadSession({ force: true }))
    router.replace(nextPath())
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '登录失败')
  } finally {
    phoneLoading.value = false
  }
}

onMounted(() => {
  loadCaptcha()
  if (smsEnabled.value) loadPhoneCaptcha()
})
onBeforeUnmount(() => {
  if (cooldownTimer) clearInterval(cooldownTimer)
})
</script>

<template>
  <PublicAuthCard title="欢迎回来" subtitle="登录你的账户，继续管理云服务">
    <div v-if="smsEnabled" class="auth-mode" role="tablist">
      <button
        type="button"
        class="auth-mode__item"
        :class="{ 'is-active': mode === 'password' }"
        @click="mode = 'password'"
      >
        密码登录
      </button>
      <button
        type="button"
        class="auth-mode__item"
        :class="{ 'is-active': mode === 'phone' }"
        @click="mode = 'phone'"
      >
        短信登录
      </button>
    </div>

    <el-form
      v-if="mode === 'password'"
      ref="formRef"
      :model="form"
      :rules="rules"
      label-position="top"
      @submit.prevent="submit"
    >
      <div class="auth-mode auth-mode--sm" role="tablist">
        <button
          type="button"
          class="auth-mode__item"
          :class="{ 'is-active': subMode === 'email' }"
          @click="subMode = 'email'"
        >
          邮箱登录
        </button>
        <button
          type="button"
          class="auth-mode__item"
          :class="{ 'is-active': subMode === 'phone' }"
          @click="subMode = 'phone'"
        >
          手机号登录
        </button>
      </div>
      <el-form-item v-if="subMode === 'email'" label="邮箱" prop="email">
        <el-input
          v-model="form.email"
          size="large"
          type="email"
          placeholder="请输入邮箱"
          autocomplete="username"
          :disabled="loading"
        >
          <template #prefix><el-icon><User /></el-icon></template>
        </el-input>
      </el-form-item>
      <el-form-item v-else label="手机号" prop="phone">
        <PhoneInput ref="pwdPhoneInputRef" v-model="form.phone" :disabled="loading" />
      </el-form-item>
      <el-form-item label="密码" prop="password">
        <el-input
          v-model="form.password"
          size="large"
          type="password"
          show-password
          placeholder="请输入密码"
          autocomplete="current-password"
          :disabled="loading"
          @keyup.enter="submit"
        >
          <template #prefix><el-icon><Lock /></el-icon></template>
        </el-input>
      </el-form-item>
      <el-form-item v-if="captcha.enabled" label="图形验证码" prop="captchaAnswer">
        <div class="auth-captcha">
          <el-input
            v-model="form.captchaAnswer"
            size="large"
            placeholder="请输入图中字符"
            :disabled="loading"
            @keyup.enter="submit"
          />
          <button
            v-if="captcha.image"
            type="button"
            class="auth-captcha__refresh"
            aria-label="刷新验证码"
            @click="loadCaptcha"
          >
            <img :src="captcha.image" alt="图形验证码" />
          </button>
        </div>
      </el-form-item>
      <div v-else-if="captchaError" class="auth-captcha__error" role="alert">
        <span>验证码加载失败</span>
        <button type="button" @click="loadCaptcha"><el-icon><Refresh /></el-icon>重试</button>
      </div>
      <ExternalCaptcha
        v-if="session.auth.external_captcha_login"
        scene="login"
        @update:fields="extFields = $event"
        @update:required="extRequired = $event"
      />
      <el-button type="primary" size="large" class="auth-submit" :loading="loading" @click="submit">
        登录账户 <el-icon><ArrowRight /></el-icon>
      </el-button>
    </el-form>

    <el-form
      v-else
      ref="phoneFormRef"
      :model="phoneForm"
      :rules="phoneRules"
      label-position="top"
      @submit.prevent="submitPhone"
    >
      <el-form-item label="手机号" prop="phone">
        <PhoneInput ref="phoneInputRef" v-model="phoneForm.phone" placeholder="请输入已绑定手机号" :disabled="phoneLoading" />
      </el-form-item>
      <el-form-item v-if="phoneCaptcha.enabled" label="图形验证码" prop="captchaAnswer">
        <div class="auth-captcha">
          <el-input
            v-model="phoneForm.captchaAnswer"
            size="large"
            placeholder="请输入图中字符"
            :disabled="phoneSending"
            @keyup.enter="sendCode"
          />
          <button
            v-if="phoneCaptcha.image"
            type="button"
            class="auth-captcha__refresh"
            aria-label="刷新验证码"
            @click="loadPhoneCaptcha"
          >
            <img :src="phoneCaptcha.image" alt="图形验证码" />
          </button>
        </div>
      </el-form-item>
      <ExternalCaptcha
        v-if="session.auth.external_captcha_phone_login"
        scene="phone_login_code"
        @update:fields="phoneExtFields = $event"
        @update:required="phoneExtRequired = $event"
      />
      <el-form-item label="短信验证码" prop="code">
        <div class="auth-code">
          <el-input
            v-model="phoneForm.code"
            size="large"
            inputmode="numeric"
            placeholder="6 位验证码"
            autocomplete="one-time-code"
            :disabled="phoneLoading"
            @keyup.enter="submitPhone"
          />
          <el-button
            size="large"
            :loading="phoneSending"
            :disabled="cooldown > 0 || phoneLoading"
            @click="sendCode"
          >
            {{ cooldown > 0 ? `${cooldown}s` : '获取验证码' }}
          </el-button>
        </div>
      </el-form-item>
      <el-button
        type="primary"
        size="large"
        class="auth-submit"
        :loading="phoneLoading"
        @click="submitPhone"
      >
        验证码登录 <el-icon><ArrowRight /></el-icon>
      </el-button>
    </el-form>

    <div class="auth-forgot">
      <RouterLink to="/forgot">忘记密码？</RouterLink>
    </div>
    <div class="auth-divider"><span>还没有账户？</span></div>
    <RouterLink to="/register" class="auth-link">
      创建一个新账户 <el-icon><ArrowRight /></el-icon>
    </RouterLink>
    <div class="auth-foot">
      <RouterLink to="/">← 返回首页</RouterLink>
      <span>安全连接 · HMAC Session</span>
    </div>
  </PublicAuthCard>
</template>

<style scoped>
.auth-mode {
  display: flex;
  gap: 6px;
  padding: 4px;
  margin-bottom: 18px;
  background: var(--art-gray-100);
  border-radius: var(--radius-md);
}

/* 密码登录内的 邮箱/手机号 子 Tab：更紧凑，与外层 密码/短信 切换区分 */
.auth-mode--sm {
  margin-bottom: 14px;
}

.auth-mode__item {
  flex: 1;
  height: 34px;
  color: var(--art-gray-600);
  font-size: 14px;
  font-weight: 500;
  background: transparent;
  border: 0;
  border-radius: calc(var(--radius-md) - 2px);
  cursor: pointer;
  transition: color 0.16s ease, background 0.16s ease, box-shadow 0.16s ease;
}

.auth-mode__item.is-active {
  color: var(--theme-color);
  background: var(--default-box-color);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06);
}

.auth-captcha {
  display: flex;
  gap: 10px;
  width: 100%;
}

.auth-captcha__refresh {
  flex-shrink: 0;
  padding: 0;
  line-height: 0;
  background: transparent;
  border: 0;
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.auth-captcha__refresh img {
  height: 40px;
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-sm);
}

.auth-captcha__error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 8px 12px;
  color: var(--el-color-danger);
  font-size: 12px;
  background: var(--el-color-danger-light-9);
  border-radius: var(--radius-sm);
}

.auth-captcha__error button {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  color: var(--el-color-danger);
  font-size: 12px;
  font-weight: 600;
  background: transparent;
  border: 0;
  cursor: pointer;
}

.auth-code {
  display: flex;
  gap: 10px;
  width: 100%;
}

.auth-submit {
  width: 100%;
  min-height: 44px;
  margin-top: 4px;
  justify-content: center;
  gap: 5px;
}

.auth-forgot {
  margin: 14px 0 2px;
  text-align: right;
  font-size: 14px;
}

.auth-forgot a:hover {
  color: var(--theme-color);
}

.auth-divider {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 22px 0 14px;
  color: var(--art-gray-500);
  font-size: 12px;
}

.auth-divider::before,
.auth-divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--art-card-border);
}

.auth-link {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  padding: 11px;
  color: var(--theme-color);
  font-size: 14px;
  font-weight: 600;
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
  transition: background 0.16s ease;
}

.auth-link:hover {
  background: color-mix(in srgb, var(--theme-color) 16%, transparent);
}

.auth-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 24px;
  color: var(--art-gray-500);
  font-size: 12px;
}

.auth-foot a:hover {
  color: var(--theme-color);
}
</style>
