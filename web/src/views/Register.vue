<script setup lang="ts">
import { ref, reactive, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { User, Lock, Message, Iphone, ArrowRight, Refresh } from '@element-plus/icons-vue'
import { register, fetchCaptcha, sendRegisterCode, type CaptchaData } from '../api/store'
import { useSession } from '../http/session'
import ExternalCaptcha from '../components/ExternalCaptcha.vue'
import PublicAuthCard from '@/components/public/PublicAuthCard.vue'

const router = useRouter()
const session = useSession()

const emailOn = computed(() => session.auth.email_registration)
const phoneOn = computed(() => session.auth.phone_registration)
const bothOn = computed(() => emailOn.value && phoneOn.value)
const anyOn = computed(() => emailOn.value || phoneOn.value)

const regMode = ref<'email' | 'phone'>(emailOn.value ? 'email' : 'phone')
const needVerify = computed(() =>
  regMode.value === 'email'
    ? session.auth.email_verification_required
    : session.auth.phone_verification_required,
)

const formRef = ref<FormInstance>()
const form = reactive({
  email: '',
  phone: '',
  name: '',
  password: '',
  confirm: '',
  code: '',
  captchaAnswer: '',
  codeCaptchaAnswer: '',
})
const captcha = ref<CaptchaData>({ enabled: false })
const codeCaptcha = ref<CaptchaData>({ enabled: false })
const captchaError = ref(false)
const loading = ref(false)
const sending = ref(false)
const cooldown = ref(0)
const extFields = ref<Record<string, string>>({})
const extRequired = ref(false)
let cooldownTimer: ReturnType<typeof setInterval> | null = null

const rules = computed<FormRules>(() => ({
  email:
    regMode.value === 'email'
      ? [
          { required: true, message: '请输入邮箱', trigger: 'blur' },
          { type: 'email', message: '邮箱格式不正确', trigger: 'blur' },
        ]
      : [],
  phone:
    regMode.value === 'phone'
      ? [{ required: true, message: '请输入手机号', trigger: 'blur' }]
      : [],
  password: [{ required: true, min: 8, message: '密码至少 8 位', trigger: 'blur' }],
  confirm: [
    { required: true, message: '请再次输入密码', trigger: 'blur' },
    {
      validator: (_rule, value, callback) => {
        if (value !== form.password) callback(new Error('两次输入的密码不一致'))
        else callback()
      },
      trigger: 'blur',
    },
  ],
  code: needVerify.value ? [{ required: true, message: '请输入验证码', trigger: 'blur' }] : [],
  captchaAnswer: [
    {
      validator: (_rule, value, callback) => {
        if (captcha.value.enabled && !value) callback(new Error('请输入图形验证码'))
        else callback()
      },
      trigger: 'blur',
    },
  ],
  codeCaptchaAnswer: [
    {
      validator: (_rule, value, callback) => {
        if (codeCaptcha.value.enabled && !value) callback(new Error('请输入图形验证码'))
        else callback()
      },
      trigger: 'blur',
    },
  ],
}))

async function loadCaptcha() {
  captchaError.value = false
  try {
    captcha.value = await fetchCaptcha('register')
    form.captchaAnswer = ''
  } catch {
    captcha.value = { enabled: false }
    captchaError.value = true
  }
}

async function loadCodeCaptcha() {
  try {
    codeCaptcha.value = await fetchCaptcha(regMode.value === 'email' ? 'email_code' : 'phone_code')
    form.codeCaptchaAnswer = ''
  } catch {
    codeCaptcha.value = { enabled: false }
  }
}

// 按当前注册方式与是否需要验证码，刷新对应验证码。
async function refreshCaptchas() {
  if (!anyOn.value) return
  if (needVerify.value) {
    captcha.value = { enabled: false }
    await loadCodeCaptcha()
  } else {
    codeCaptcha.value = { enabled: false }
    await loadCaptcha()
  }
}

onMounted(refreshCaptchas)
watch(regMode, () => refreshCaptchas())

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

onBeforeUnmount(() => {
  if (cooldownTimer) clearInterval(cooldownTimer)
})

async function sendCode() {
  const targetVal = regMode.value === 'email' ? form.email : form.phone
  if (!targetVal) {
    ElMessage.warning(regMode.value === 'email' ? '请先填写邮箱' : '请先填写手机号')
    return
  }
  if (codeCaptcha.value.enabled && !form.codeCaptchaAnswer) {
    ElMessage.warning('请输入图形验证码')
    return
  }
  sending.value = true
  try {
    const extra: Record<string, string> = {}
    if (codeCaptcha.value.enabled)
      Object.assign(extra, {
        captcha_id_code: codeCaptcha.value.id || '',
        captcha_answer_code: form.codeCaptchaAnswer,
      })
    await sendRegisterCode(regMode.value, targetVal, extra)
    ElMessage.success(regMode.value === 'email' ? '验证码已发送，请查收邮件' : '验证码已发送，请查收短信')
    startCooldown()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '发送失败')
    if (needVerify.value) await loadCodeCaptcha()
  } finally {
    sending.value = false
  }
}

async function submit() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  if (extRequired.value && !extFields.value.captcha_token) {
    ElMessage.warning('请先完成人机验证')
    return
  }
  loading.value = true
  try {
    const body: Record<string, string> = {
      mode: regMode.value,
      name: form.name,
      password: form.password,
      password_confirm: form.confirm,
    }
    if (regMode.value === 'email') body.email = form.email
    else body.phone = form.phone
    if (needVerify.value) body.code = form.code
    if (captcha.value.enabled)
      Object.assign(body, { captcha_id: captcha.value.id || '', captcha_answer: form.captchaAnswer })
    Object.assign(body, extFields.value)
    await register(body)
    await import('../http/session').then((m) => m.loadSession({ force: true }))
    ElMessage.success('注册成功，正在进入系统…')
    router.replace('/')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '注册失败')
    if (captcha.value.enabled) await loadCaptcha()
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <PublicAuthCard title="创建账户" subtitle="注册后即可开始使用云基础设施服务" wide>
    <el-alert
      v-if="!anyOn"
      type="info"
      :closable="false"
      show-icon
      title="当前站点未开放注册"
      description="请联系管理员或稍后再试。"
    />

    <template v-else>
      <div v-if="bothOn" class="auth-mode" role="tablist">
        <button
          type="button"
          class="auth-mode__item"
          :class="{ 'is-active': regMode === 'email' }"
          @click="regMode = 'email'"
        >
          邮箱注册
        </button>
        <button
          type="button"
          class="auth-mode__item"
          :class="{ 'is-active': regMode === 'phone' }"
          @click="regMode = 'phone'"
        >
          手机号注册
        </button>
      </div>

      <el-form
        ref="formRef"
        class="auth-form"
        :model="form"
        :rules="rules"
        label-position="top"
        @submit.prevent="submit"
      >
        <el-form-item v-if="regMode === 'email'" label="邮箱" prop="email">
          <el-input
            v-model="form.email"
            size="large"
            type="email"
            placeholder="请输入邮箱"
            autocomplete="email"
            :disabled="loading"
          >
            <template #prefix><el-icon><Message /></el-icon></template>
          </el-input>
        </el-form-item>
        <el-form-item v-else label="手机号" prop="phone">
          <el-input
            v-model="form.phone"
            size="large"
            type="tel"
            placeholder="请输入手机号"
            autocomplete="tel"
            :disabled="loading"
          >
            <template #prefix><el-icon><Iphone /></el-icon></template>
          </el-input>
        </el-form-item>
        <el-form-item label="显示名称（选填）">
          <el-input
            v-model="form.name"
            size="large"
            placeholder="请输入显示名称"
            autocomplete="nickname"
            :disabled="loading"
          >
            <template #prefix><el-icon><User /></el-icon></template>
          </el-input>
        </el-form-item>
        <el-form-item label="密码" prop="password">
          <el-input
            v-model="form.password"
            size="large"
            type="password"
            show-password
            placeholder="至少 8 位"
            autocomplete="new-password"
            :disabled="loading"
          >
            <template #prefix><el-icon><Lock /></el-icon></template>
          </el-input>
        </el-form-item>
        <el-form-item label="确认密码" prop="confirm">
          <el-input
            v-model="form.confirm"
            size="large"
            type="password"
            show-password
            placeholder="再次输入密码"
            autocomplete="new-password"
            :disabled="loading"
            @keyup.enter="submit"
          >
            <template #prefix><el-icon><Lock /></el-icon></template>
          </el-input>
        </el-form-item>

        <template v-if="needVerify">
          <el-form-item v-if="codeCaptcha.enabled" label="图形验证码" prop="codeCaptchaAnswer">
            <div class="auth-captcha">
              <el-input
                v-model="form.codeCaptchaAnswer"
                size="large"
                placeholder="请输入图中字符"
                :disabled="sending"
                @keyup.enter="sendCode"
              />
              <button
                v-if="codeCaptcha.image"
                type="button"
                class="auth-captcha__refresh"
                aria-label="刷新验证码"
                @click="loadCodeCaptcha"
              >
                <img :src="codeCaptcha.image" alt="图形验证码" />
              </button>
            </div>
          </el-form-item>
          <el-form-item :label="regMode === 'email' ? '邮箱验证码' : '短信验证码'" prop="code">
            <div class="auth-code">
              <el-input
                v-model="form.code"
                size="large"
                inputmode="numeric"
                placeholder="6 位验证码"
                autocomplete="one-time-code"
                :disabled="loading"
                @keyup.enter="submit"
              />
              <el-button size="large" :loading="sending" :disabled="cooldown > 0 || loading" @click="sendCode">
                {{ cooldown > 0 ? `${cooldown}s` : '获取验证码' }}
              </el-button>
            </div>
          </el-form-item>
        </template>
        <el-form-item v-else-if="captcha.enabled" label="图形验证码" prop="captchaAnswer">
          <div class="auth-captcha">
            <el-input
              v-model="form.captchaAnswer"
              size="large"
              placeholder="请输入图中字符"
              :disabled="loading"
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
        <div v-else-if="captchaError" class="auth-captcha__error auth-full" role="alert">
          <span>验证码加载失败</span>
          <button type="button" @click="loadCaptcha"><el-icon><Refresh /></el-icon>重试</button>
        </div>

        <ExternalCaptcha
          v-if="session.auth.external_captcha_register && !needVerify"
          scene="register"
          class="auth-full"
          @update:fields="extFields = $event"
          @update:required="extRequired = $event"
        />

        <el-button type="primary" size="large" class="auth-submit" :loading="loading" @click="submit">
          创建账户 <el-icon><ArrowRight /></el-icon>
        </el-button>
      </el-form>
    </template>

    <div class="auth-divider"><span>已经有账户？</span></div>
    <RouterLink to="/login" class="auth-link">返回登录 <el-icon><ArrowRight /></el-icon></RouterLink>
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

.auth-mode__item {
  flex: 1;
  height: 34px;
  color: var(--art-gray-600);
  font-size: 13px;
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

@media (min-width: 720px) {
  .auth-form {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    column-gap: 16px;
    align-items: start;
  }

  .auth-form .auth-full,
  .auth-form .auth-submit {
    grid-column: 1 / -1;
  }
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
  font-size: 13px;
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
