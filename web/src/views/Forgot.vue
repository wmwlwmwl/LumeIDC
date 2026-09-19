<script setup lang="ts">
import { ref, reactive, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { User, Lock, Message, ArrowRight, Refresh } from '@element-plus/icons-vue'
import { fetchCaptcha, sendForgotCode, resetPassword, type CaptchaData } from '../api/store'
import { useSession } from '../http/session'
import PublicAuthCard from '@/components/public/PublicAuthCard.vue'
import PhoneInput from '@/components/phone/PhoneInput.vue'
import ExternalCaptcha from '../components/ExternalCaptcha.vue'
import { hasCaptchaResult } from '../components/captcha-registry'

const router = useRouter()
const phoneInputRef = ref<InstanceType<typeof PhoneInput>>()
const session = useSession()

const mode = ref<'email' | 'phone'>('email')

const formRef = ref<FormInstance>()
const form = reactive({ email: '', phone: '', code: '', password: '', confirm: '', captchaAnswer: '' })
const captcha = ref<CaptchaData>({ enabled: false })
const extFields = ref<Record<string, string>>({})
const extRequired = ref(false)
const sending = ref(false)
const resetting = ref(false)
const cooldown = ref(0)
let cooldownTimer: ReturnType<typeof setInterval> | null = null

// 当前模式的账号目标（邮箱原文 / 手机号完整 E.164）
const account = computed(() => (mode.value === 'email' ? form.email.trim() : form.phone))
// 找回密码发码的人机验证统一走 forgot_code 场景（与渠道无关，由设置页「找回密码」行控制）。
const scene = 'forgot_code' as const

// rules 为常量引用（不随 mode 变化），避免 el-form 监听 rules 变化触发全量校验、
// 导致切换找回方式时空表单全部标红；渠道差异用 validator 在运行时判定。
const rules: FormRules = {
  email: [
    {
      validator: (_rule, value, callback) => {
        if (mode.value !== 'email') return callback()
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
        if (mode.value !== 'phone') return callback()
        if (!value) return callback(new Error('请输入手机号'))
        callback()
      },
      trigger: 'blur',
    },
  ],
  code: [{ required: true, message: '请输入验证码', trigger: 'blur' }],
  password: [{ required: true, min: 8, message: '新密码至少 8 位', trigger: 'blur' }],
  confirm: [
    {
      validator: (_rule, value, callback) => {
        if (value !== form.password) callback(new Error('两次输入的密码不一致'))
        else callback()
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
}

// 切换找回方式时，刷新对应场景的图形验证码并清空验证码输入
watch(mode, () => {
  form.code = ''
  loadCaptcha()
})

async function loadCaptcha() {
  // 找回密码选择「外部验证码」时无需本地图形码（后端同样外部优先）。
  if (session.auth.forgot_code_external) {
    captcha.value = { enabled: false }
    return
  }
  try {
    captcha.value = await fetchCaptcha(scene)
    form.captchaAnswer = ''
  } catch {
    captcha.value = { enabled: false }
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

/* 发送验证码：邮箱/手机按当前模式，图形验证码字段随请求携带 */
async function send() {
  if (!account.value) {
    ElMessage.warning(mode.value === 'email' ? '请先填写邮箱' : '请先填写手机号')
    return
  }
  if (mode.value === 'phone') {
    const r = phoneInputRef.value?.check()
    if (!r?.ok) {
      ElMessage.warning(r?.msg || '手机号格式不正确')
      return
    }
  }
  if (session.auth.forgot_code_external) {
    if (extRequired.value && !hasCaptchaResult(extFields.value)) {
      ElMessage.warning('请先完成人机验证')
      return
    }
  } else {
    if (!captcha.value.enabled) await loadCaptcha()
    if (captcha.value.enabled && !form.captchaAnswer) {
      ElMessage.warning('请输入图形验证码')
      return
    }
  }
  sending.value = true
  try {
    const extra: Record<string, string> = { ...extFields.value }
    if (captcha.value.enabled) {
      Object.assign(extra, {
        captcha_id_code: captcha.value.id || '',
        captcha_answer_code: form.captchaAnswer,
      })
    }
    await sendForgotCode(account.value, extra)
    ElMessage.success('验证码已发送，请查收邮箱或短信')
    startCooldown()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '发送失败')
    await loadCaptcha()
  } finally {
    sending.value = false
  }
}

async function submit() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  if (mode.value === 'phone') {
    const r = phoneInputRef.value?.check()
    if (!r?.ok) {
      ElMessage.warning(r?.msg || '手机号格式不正确')
      return
    }
  }
  resetting.value = true
  try {
    await resetPassword(account.value, form.code, form.password)
    ElMessage.success('密码已重置，请重新登录')
    router.push('/login')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '重置失败')
  } finally {
    resetting.value = false
  }
}

onMounted(() => {
  loadCaptcha()
})
onBeforeUnmount(() => {
  if (cooldownTimer) clearInterval(cooldownTimer)
})
</script>

<template>
  <PublicAuthCard title="找回密码" subtitle="通过邮箱或短信验证码重置密码">
    <div class="auth-mode" role="tablist">
      <button
        type="button"
        class="auth-mode__item"
        :class="{ 'is-active': mode === 'email' }"
        @click="mode = 'email'"
      >
        邮箱找回
      </button>
      <button
        type="button"
        class="auth-mode__item"
        :class="{ 'is-active': mode === 'phone' }"
        @click="mode = 'phone'"
      >
        手机找回
      </button>
    </div>

    <el-form
      ref="formRef"
      :model="form"
      :rules="rules"
      label-position="top"
      @submit.prevent="submit"
    >
      <el-form-item v-if="mode === 'email'" label="邮箱" prop="email">
        <el-input
          v-model="form.email"
          size="large"
          type="email"
          placeholder="请输入已注册的邮箱"
          autocomplete="username"
          :disabled="resetting"
        >
          <template #prefix><el-icon><Message /></el-icon></template>
        </el-input>
      </el-form-item>
      <el-form-item v-else label="手机号" prop="phone">
        <PhoneInput ref="phoneInputRef" v-model="form.phone" />
      </el-form-item>
      <el-form-item v-if="captcha.enabled && !session.auth.forgot_code_external" label="图形验证码" prop="captchaAnswer">
        <div class="auth-captcha">
          <el-input
            v-model="form.captchaAnswer"
            size="large"
            placeholder="请输入图中字符"
            :disabled="sending"
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
      <el-form-item label="短信或邮箱验证码" prop="code">
        <div class="auth-code">
          <el-input
            v-model="form.code"
            size="large"
            placeholder="6 位验证码"
            autocomplete="one-time-code"
            :disabled="resetting"
          />
          <el-button
            size="large"
            :loading="sending"
            :disabled="cooldown > 0 || resetting"
            @click="send"
          >
            {{ cooldown > 0 ? `${cooldown}s` : '获取验证码' }}
          </el-button>
        </div>
      </el-form-item>
      <!-- 找回密码选择「外部验证码」时，发码前完成外部人机验证 -->
      <ExternalCaptcha
        v-if="session.auth.forgot_code_external"
        scene="forgot_code"
        class="auth-full"
        @update:fields="extFields = $event"
        @update:required="extRequired = $event"
      />
      <el-form-item label="新密码" prop="password">
        <el-input
          v-model="form.password"
          size="large"
          type="password"
          show-password
          placeholder="至少 8 位"
          autocomplete="new-password"
          :disabled="resetting"
        >
          <template #prefix><el-icon><Lock /></el-icon></template>
        </el-input>
      </el-form-item>
      <el-form-item label="确认新密码" prop="confirm">
        <el-input
          v-model="form.confirm"
          size="large"
          type="password"
          show-password
          placeholder="再次输入新密码"
          autocomplete="new-password"
          :disabled="resetting"
          @keyup.enter="submit"
        >
          <template #prefix><el-icon><Lock /></el-icon></template>
        </el-input>
      </el-form-item>
      <el-button type="primary" size="large" class="auth-submit" :loading="resetting" @click="submit">
        重置密码 <el-icon><ArrowRight /></el-icon>
      </el-button>
    </el-form>

    <div class="auth-foot">
      <RouterLink to="/login">← 返回登录</RouterLink>
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