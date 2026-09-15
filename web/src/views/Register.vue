<script setup lang="ts">
import { ref, reactive, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { User, Lock, Message, ArrowRight, Refresh } from '@element-plus/icons-vue'
import { register, fetchCaptcha, sendRegisterCode, type CaptchaData } from '../api/store'
import { useSession } from '../http/session'
import ExternalCaptcha from '../components/ExternalCaptcha.vue'
import PublicAuthCard from '@/components/public/PublicAuthCard.vue'
import PhoneInput from '@/components/phone/PhoneInput.vue'

const router = useRouter()
const session = useSession()
const phoneInputRef = ref<InstanceType<typeof PhoneInput>>()

const emailOn = computed(() => session.auth.email_registration)
const phoneOn = computed(() => session.auth.phone_registration)
const bothOn = computed(() => emailOn.value && phoneOn.value)
const anyOn = computed(() => emailOn.value || phoneOn.value)
// 同时注册模式：后端开关开启且「邮箱+手机号」两个允许开关均开启
const requireBoth = computed(() => emailOn.value && phoneOn.value && session.auth.require_both_registration)

const regMode = ref<'email' | 'phone'>(emailOn.value ? 'email' : 'phone')
const emailVerify = computed(() => session.auth.email_verification_required)
const phoneVerify = computed(() => session.auth.phone_verification_required)
// 单模式（邮箱或手机）下是否需要验证码
const needVerify = computed(() => (regMode.value === 'email' ? emailVerify.value : phoneVerify.value))
// 同时注册模式下邮箱/手机各自是否需要验证码
const needEmailCode = computed(() => requireBoth.value && emailVerify.value)
const needPhoneCode = computed(() => requireBoth.value && phoneVerify.value)
const anyVerify = computed(() => emailVerify.value || phoneVerify.value)

const formRef = ref<FormInstance>()
const form = reactive({
  email: '',
  phone: '',
  name: '',
  password: '',
  confirm: '',
  code: '',
  email_code: '',
  phone_code: '',
  captchaAnswer: '',
  codeCaptchaAnswer: '',
})
const captcha = ref<CaptchaData>({ enabled: false }) // register 场景图形码（提交）
const codeCaptcha = ref<CaptchaData>({ enabled: false }) // email_code/phone_code 场景图形码（发送验证码）
const captchaError = ref(false)
const loading = ref(false)
const sending = ref(false) // 单模式发送中
const emailSending = ref(false)
const phoneSending = ref(false)
const cooldown = ref(0)
const emailCooldown = ref(0)
const phoneCooldown = ref(0)
const extFields = ref<Record<string, string>>({})
const extRequired = ref(false)
// 注册发码前的外部验证（scene=register_code，由「注册发码」行控制），与注册提交外部验证分离。
const codeExtFields = ref<Record<string, string>>({})
const codeExtRequired = ref(false)
let cooldownTimer: ReturnType<typeof setInterval> | null = null
let emailTimer: ReturnType<typeof setInterval> | null = null
let phoneTimer: ReturnType<typeof setInterval> | null = null

// rules 为常量引用（不随注册方式变化），避免 el-form 监听 rules 变化触发全量校验、
// 导致切换注册方式时空表单全部标红；渠道差异用 validator 在运行时判定。
const rules: FormRules = {
  email: [
    {
      validator: (_rule, value, callback) => {
        if (!requireBoth.value && regMode.value !== 'email') return callback()
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
        if (!requireBoth.value && regMode.value !== 'phone') return callback()
        if (!value) return callback(new Error('请输入手机号'))
        callback()
      },
      trigger: 'blur',
    },
  ],
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
  code: [
    {
      validator: (_rule, value, callback) => {
        if (requireBoth.value || !needVerify.value) return callback()
        if (!value) return callback(new Error('请输入验证码'))
        callback()
      },
      trigger: 'blur',
    },
  ],
  email_code: [
    {
      validator: (_rule, value, callback) => {
        if (!needEmailCode.value) return callback()
        if (!value) return callback(new Error('请输入邮箱验证码'))
        callback()
      },
      trigger: 'blur',
    },
  ],
  phone_code: [
    {
      validator: (_rule, value, callback) => {
        if (!needPhoneCode.value) return callback()
        if (!value) return callback(new Error('请输入短信验证码'))
        callback()
      },
      trigger: 'blur',
    },
  ],
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
}

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

// 加载「发送验证码」前的图形验证码（register_code 场景，由「注册发码」开关控制）。
// 注册发码选择「外部验证码」时无需本地图形码（后端同样外部优先）。
async function loadCodeCaptcha(scene: 'register_code' = 'register_code') {
  if (session.auth.register_code_external) {
    codeCaptcha.value = { enabled: false }
    return
  }
  try {
    codeCaptcha.value = await fetchCaptcha(scene)
    form.codeCaptchaAnswer = ''
  } catch {
    codeCaptcha.value = { enabled: false }
  }
}

// 按当前注册方式与是否需要验证码，刷新对应验证码。
async function refreshCaptchas() {
  if (!anyOn.value) return
  if (requireBoth.value) {
    // 同时注册：任一验证码需求开启则加载「发送验证码前」的图形验证码，让验证码区直接可见；
    // 都关则用注册图形码
    if (anyVerify.value) {
      captcha.value = { enabled: false }
      await loadCodeCaptcha()
    } else {
      codeCaptcha.value = { enabled: false }
      await loadCaptcha()
    }
    return
  }
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

function startCooldown(timer: 'single' | 'email' | 'phone') {
  if (timer === 'single') {
    cooldown.value = 60
    if (cooldownTimer) clearInterval(cooldownTimer)
    cooldownTimer = setInterval(() => {
      cooldown.value -= 1
      if (cooldown.value <= 0 && cooldownTimer) {
        clearInterval(cooldownTimer)
        cooldownTimer = null
      }
    }, 1000)
    return
  }
  const isEmail = timer === 'email'
  const cd = isEmail ? emailCooldown : phoneCooldown
  cd.value = 60
  if (isEmail) {
    if (emailTimer) clearInterval(emailTimer)
    emailTimer = setInterval(() => {
      emailCooldown.value -= 1
      if (emailCooldown.value <= 0 && emailTimer) {
        clearInterval(emailTimer)
        emailTimer = null
      }
    }, 1000)
  } else {
    if (phoneTimer) clearInterval(phoneTimer)
    phoneTimer = setInterval(() => {
      phoneCooldown.value -= 1
      if (phoneCooldown.value <= 0 && phoneTimer) {
        clearInterval(phoneTimer)
        phoneTimer = null
      }
    }, 1000)
  }
}

onBeforeUnmount(() => {
  if (cooldownTimer) clearInterval(cooldownTimer)
  if (emailTimer) clearInterval(emailTimer)
  if (phoneTimer) clearInterval(phoneTimer)
})

// 验证码发送前的人机验证载荷：外部「注册发码」场景字段优先，本地图形码字段兜底
function codeExtra(): Record<string, string> {
  const extra: Record<string, string> = { ...codeExtFields.value }
  if (codeCaptcha.value.enabled) {
    extra.captcha_id_code = codeCaptcha.value.id || ''
    extra.captcha_answer_code = form.codeCaptchaAnswer
  }
  return extra
}

// 注册发码选择「外部验证码」时，发送前须已完成该外部验证
function externalCodeReady(): boolean {
  if (!session.auth.register_code_external) return true
  if (codeExtRequired.value && !codeExtFields.value.captcha_token) {
    ElMessage.warning('请先完成人机验证')
    return false
  }
  return true
}

// 单模式：邮箱/手机验证码发送
async function sendCode() {
  const targetVal = regMode.value === 'email' ? form.email : form.phone
  if (!targetVal) {
    ElMessage.warning(regMode.value === 'email' ? '请先填写邮箱' : '请先填写手机号')
    return
  }
  if (!externalCodeReady()) return
  if (regMode.value === 'phone') {
    const r = phoneInputRef.value?.check()
    if (!r?.ok) {
      ElMessage.warning(r?.msg || '手机号格式不正确')
      return
    }
  }
  const scene = 'register_code'
  if (!codeCaptcha.value.enabled) await loadCodeCaptcha(scene)
  if (codeCaptcha.value.enabled && !form.codeCaptchaAnswer) {
    ElMessage.warning('请输入图形验证码')
    return
  }
  sending.value = true
  try {
    await sendRegisterCode(regMode.value, targetVal, codeExtra())
    ElMessage.success(regMode.value === 'email' ? '验证码已发送，请查收邮件' : '验证码已发送，请查收短信')
    startCooldown('single')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '发送失败')
    if (needVerify.value) await loadCodeCaptcha(scene)
  } finally {
    sending.value = false
  }
}

// 同时注册模式：邮箱验证码发送
async function sendEmailCode() {
  if (!form.email) {
    ElMessage.warning('请先填写邮箱')
    return
  }
  if (!externalCodeReady()) return
  if (!codeCaptcha.value.enabled) await loadCodeCaptcha()
  if (codeCaptcha.value.enabled && !form.codeCaptchaAnswer) {
    ElMessage.warning('请输入图形验证码')
    return
  }
  emailSending.value = true
  try {
    await sendRegisterCode('email', form.email, codeExtra())
    ElMessage.success('验证码已发送，请查收邮件')
    startCooldown('email')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '发送失败')
    await loadCodeCaptcha()
  } finally {
    emailSending.value = false
  }
}

// 同时注册模式：手机验证码发送
async function sendPhoneCode() {
  if (!form.phone) {
    ElMessage.warning('请先填写手机号')
    return
  }
  if (!externalCodeReady()) return
  const r = phoneInputRef.value?.check()
  if (!r?.ok) {
    ElMessage.warning(r?.msg || '手机号格式不正确')
    return
  }
  if (!codeCaptcha.value.enabled) await loadCodeCaptcha()
  if (codeCaptcha.value.enabled && !form.codeCaptchaAnswer) {
    ElMessage.warning('请输入图形验证码')
    return
  }
  phoneSending.value = true
  try {
    await sendRegisterCode('phone', form.phone, codeExtra())
    ElMessage.success('验证码已发送，请查收短信')
    startCooldown('phone')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '发送失败')
    await loadCodeCaptcha()
  } finally {
    phoneSending.value = false
  }
}

async function submit() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  if (requireBoth.value || regMode.value === 'phone') {
    const r = phoneInputRef.value?.check()
    if (!r?.ok) {
      ElMessage.warning(r?.msg || '手机号格式不正确')
      return
    }
  }
  if (extRequired.value && !extFields.value.captcha_token) {
    ElMessage.warning('请先完成人机验证')
    return
  }
  loading.value = true
  try {
    const body: Record<string, string> = {
      mode: requireBoth.value ? 'both' : regMode.value,
      name: form.name,
      password: form.password,
      password_confirm: form.confirm,
    }
    if (requireBoth.value) {
      body.email = form.email
      body.phone = form.phone
      if (needEmailCode.value) body.email_code = form.email_code
      if (needPhoneCode.value) body.phone_code = form.phone_code
    } else if (regMode.value === 'email') {
      body.email = form.email
      if (needVerify.value) body.code = form.code
    } else {
      body.phone = form.phone
      if (needVerify.value) body.code = form.code
    }
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
      <div v-if="requireBoth" class="auth-both-hint">
        注册将同时绑定邮箱与手机号
      </div>
      <div v-else-if="bothOn" class="auth-mode" role="tablist">
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
        :class="['auth-form', { 'auth-form--stack': requireBoth }]"
        :model="form"
        :rules="rules"
        label-position="top"
        @submit.prevent="submit"
      >
        <el-form-item v-if="requireBoth || regMode === 'email'" label="邮箱" prop="email">
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
        <el-form-item v-if="requireBoth || regMode === 'phone'" label="手机号" prop="phone">
          <PhoneInput ref="phoneInputRef" v-model="form.phone" :disabled="loading" />
        </el-form-item>
        <el-form-item label="显示名称（选填）">
          <!-- nickname 为 HTML 规范 autofill 令牌，EP 2.11 引用的 TS AutoFill 联合类型未收录 -->
          <el-input
            v-model="form.name"
            size="large"
            placeholder="请输入显示名称"
            :autocomplete="('nickname' as any)"
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

        <!-- 同时注册模式：邮箱/手机验证码各自独立（按开关要求显示） -->
        <template v-if="requireBoth">
          <template v-if="needEmailCode || needPhoneCode">
            <el-form-item v-if="!session.auth.register_code_external && codeCaptcha.enabled" label="图形验证码" prop="codeCaptchaAnswer">
              <div class="auth-captcha">
                <el-input
                  v-model="form.codeCaptchaAnswer"
                  size="large"
                  placeholder="请输入图中字符"
                  :disabled="emailSending || phoneSending"
                />
                <button
                  v-if="codeCaptcha.image"
                  type="button"
                  class="auth-captcha__refresh"
                  aria-label="刷新验证码"
                  @click="() => loadCodeCaptcha()"
                >
                  <img :src="codeCaptcha.image" alt="图形验证码" />
                </button>
              </div>
            </el-form-item>
            <el-form-item v-if="needEmailCode" label="邮箱验证码" prop="email_code">
              <div class="auth-code">
                <el-input
                  v-model="form.email_code"
                  size="large"
                  inputmode="numeric"
                  placeholder="6 位验证码"
                  autocomplete="one-time-code"
                  :disabled="loading"
                />
                <el-button
                  size="large"
                  :loading="emailSending"
                  :disabled="emailCooldown > 0 || loading"
                  @click="sendEmailCode"
                >
                  {{ emailCooldown > 0 ? `${emailCooldown}s` : '获取邮箱验证码' }}
                </el-button>
              </div>
            </el-form-item>
            <el-form-item v-if="needPhoneCode" label="短信验证码" prop="phone_code">
              <div class="auth-code">
                <el-input
                  v-model="form.phone_code"
                  size="large"
                  inputmode="numeric"
                  placeholder="6 位验证码"
                  autocomplete="one-time-code"
                  :disabled="loading"
                />
                <el-button
                  size="large"
                  :loading="phoneSending"
                  :disabled="phoneCooldown > 0 || loading"
                  @click="sendPhoneCode"
                >
                  {{ phoneCooldown > 0 ? `${phoneCooldown}s` : '获取短信验证码' }}
                </el-button>
              </div>
            </el-form-item>
          </template>
          <el-form-item v-else-if="!session.auth.external_captcha_register && captcha.enabled" label="图形验证码" prop="captchaAnswer">
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
        </template>

        <!-- 单模式（邮箱或手机）验证码区 -->
        <template v-else>
          <template v-if="needVerify">
            <el-form-item v-if="!session.auth.register_code_external && codeCaptcha.enabled" label="图形验证码" prop="codeCaptchaAnswer">
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
                  @click="() => loadCodeCaptcha()"
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
          <el-form-item v-else-if="!session.auth.external_captcha_register && captcha.enabled" label="图形验证码" prop="captchaAnswer">
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
        </template>

        <!-- 外部「注册发码」人机验证：发送注册验证码前完成（scene=register_code） -->
        <ExternalCaptcha
          v-if="session.auth.register_code_external && (needVerify || requireBoth)"
          scene="register_code"
          class="auth-full"
          @update:fields="codeExtFields = $event"
          @update:required="codeExtRequired = $event"
        />

        <!-- 外部「注册场景」人机验证：启用时替代上述本地图形码，发送验证码与注册提交共用 -->
        <ExternalCaptcha
          v-if="session.auth.external_captcha_register"
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

.auth-both-hint {
  margin-bottom: 18px;
  padding: 10px 12px;
  color: var(--theme-color);
  font-size: 14px;
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
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

  /* 同时注册模式：字段单列堆叠（邮箱、手机号、验证码…逐行展示） */
  .auth-form--stack {
    display: block;
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