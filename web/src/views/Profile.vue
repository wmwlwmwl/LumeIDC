<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { ElMessage } from 'element-plus'
import { Cellphone, Message, User } from '@element-plus/icons-vue'
import { fetchProfile, updateProfile, type ProfileData } from '../api/user'
import { fetchCaptcha, type CaptchaData } from '../api/store'
import { http } from '../http/index'
import PublicPageHead from '@/components/public/PublicPageHead.vue'
import { useSession } from '../http/session'
import PhoneInput from '@/components/phone/PhoneInput.vue'
import ExternalCaptcha from '../components/ExternalCaptcha.vue'
import { hasCaptchaResult } from '../components/captcha-registry'

const data = ref<ProfileData | null>(null)
const loadError = ref(false)
const session = useSession()
const phoneInputRef = ref<InstanceType<typeof PhoneInput>>()
let emailTimer: ReturnType<typeof setInterval> | null = null
let phoneTimer: ReturnType<typeof setInterval> | null = null
let emailOldTimer: ReturnType<typeof setInterval> | null = null
let phoneOldTimer: ReturnType<typeof setInterval> | null = null

const name = ref('')
const email = ref('')
const emailCode = ref('')
const emailSending = ref(false)
const emailConfirming = ref(false)
const emailCooldown = ref(0)
const savingName = ref(false)

// 两步换绑：第一步「验证原渠道」→ step_token → 第二步「绑定新渠道」
const emailStep = ref<'old' | 'new'>('old')
const phoneStep = ref<'old' | 'new'>('old')
const emailOldCode = ref('')
const emailStepToken = ref('')
const emailOldSending = ref(false)
const emailOldCooldown = ref(0)
const phoneOldCode = ref('')
const phoneStepToken = ref('')
const phoneOldSending = ref(false)
const phoneOldCooldown = ref(0)

const phone = ref('')
const code = ref('')
const sending = ref(false)
const confirming = ref(false)
const cooldown = ref(0)

async function load() {
  loadError.value = false
  try {
    data.value = await fetchProfile()
    name.value = data.value.name || ''
    email.value = ''
    // 原渠道存在且开关开启时走两步换绑：先验证原邮箱/原手机；开关关闭=可直接改
    emailStep.value = data.value.email && session.auth.require_old_email_change ? 'old' : 'new'
    phoneStep.value = data.value.has_phone && session.auth.require_old_phone_change ? 'old' : 'new'
    emailStepToken.value = ''
    phoneStepToken.value = ''
    if (!data.value.has_phone) phone.value = ''
  } catch (err: unknown) {
    data.value = null
    loadError.value = true
    ElMessage.error((err as Error).message || '读取资料失败')
  }
}

onBeforeUnmount(() => {
  if (emailTimer) clearInterval(emailTimer)
  if (phoneTimer) clearInterval(phoneTimer)
  if (emailOldTimer) clearInterval(emailOldTimer)
  if (phoneOldTimer) clearInterval(phoneOldTimer)
  if (emailVerifyTimer) clearInterval(emailVerifyTimer)
  if (phoneVerifyTimer) clearInterval(phoneVerifyTimer)
})

async function saveName() {
  if (!name.value.trim()) {
    ElMessage.warning('请填写账户名称')
    return
  }
  savingName.value = true
  try {
    data.value = await updateProfile({
      name: name.value.trim(),
      email: data.value?.email || '',
      current_password: '',
    })
    if (session.user) session.user.name = data.value.name
    name.value = data.value.name
    ElMessage.success('账户名称已保存')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    savingName.value = false
  }
}

function startEmailCountdown() {
  emailCooldown.value = 60
  if (emailTimer) clearInterval(emailTimer)
  emailTimer = setInterval(() => {
    emailCooldown.value -= 1
    if (emailCooldown.value <= 0 && emailTimer) {
      clearInterval(emailTimer)
      emailTimer = null
    }
  }, 1000)
}

// —— 邮箱两步换绑：第一步验证原邮箱 ——
function startEmailOldCountdown() {
  emailOldCooldown.value = 60
  if (emailOldTimer) clearInterval(emailOldTimer)
  emailOldTimer = setInterval(() => {
    emailOldCooldown.value -= 1
    if (emailOldCooldown.value <= 0 && emailOldTimer) {
      clearInterval(emailOldTimer)
      emailOldTimer = null
    }
  }, 1000)
}

async function sendOldEmailCode() {
  if (!(await profileVerifyReady())) return
  emailOldSending.value = true
  try {
    const res = (await http.post('/user/profile/email/old-send', profileVerifyExtra(), {
      silent401: true,
    })) as { ok: number; msg?: string }
    if (String(res.ok) === '1') {
      ElMessage.success('验证码已发送，请查收原邮箱')
      startEmailOldCountdown()
    } else {
      ElMessage.error(res.msg || '发送失败')
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '发送失败')
  } finally {
    emailOldSending.value = false
  }
}

async function verifyOldEmail() {
  if (!emailOldCode.value.trim()) {
    ElMessage.warning('请输入原邮箱验证码')
    return
  }
  emailOldSending.value = true
  try {
    const res = (await http.post('/user/profile/email/verify-old', {
      code: emailOldCode.value.trim(),
    }, { silent401: true })) as { ok: number; step_token?: string; msg?: string }
    if (String(res.ok) === '1' && res.step_token) {
      emailStepToken.value = res.step_token
      emailOldCode.value = ''
      emailStep.value = 'new'
      resetProfileVerifyForStep()
      ElMessage.success('原邮箱验证通过')
    } else {
      ElMessage.error(res.msg || '验证失败')
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '验证失败')
  } finally {
    emailOldSending.value = false
  }
}

// —— 邮箱两步换绑：第二步绑定新邮箱 ——
async function sendEmailCode() {
  if (!email.value.trim()) {
    ElMessage.warning('请输入新邮箱')
    return
  }
  if (!(await profileVerifyReady())) return
  emailSending.value = true
  try {
    const body: Record<string, string> = {
      email: email.value.trim(),
      step_token: emailStepToken.value,
      ...profileVerifyExtra(),
    }
    await http.post('/user/profile/email/send', body, { silent401: true })
    ElMessage.success('验证码已发送，请查收新邮箱')
    startEmailCountdown()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '验证码发送失败')
  } finally {
    emailSending.value = false
  }
}

async function confirmEmail() {
  if (!emailCode.value.trim()) {
    ElMessage.warning('请输入邮箱验证码')
    return
  }
  emailConfirming.value = true
  try {
    const res = (await http.post('/user/profile/email/confirm', {
      name: name.value.trim(),
      email: email.value.trim(),
      code: emailCode.value.trim(),
      step_token: emailStepToken.value,
    })) as { name: string; email: string }
    emailCode.value = ''
    emailStep.value = 'old'
    resetProfileVerifyForStep()
    if (session.user) {
      session.user.name = res.name
      session.user.email = res.email
    }
    await load()
    ElMessage.success('邮箱已验证并更新')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '邮箱验证失败')
  } finally {
    emailConfirming.value = false
  }
}

function startCountdown() {
  cooldown.value = 60
  if (phoneTimer) clearInterval(phoneTimer)
  phoneTimer = setInterval(() => {
    cooldown.value -= 1
    if (cooldown.value <= 0 && phoneTimer) {
      clearInterval(phoneTimer)
      phoneTimer = null
    }
  }, 1000)
}

// —— 手机两步换绑：第一步验证原手机 ——
function startPhoneOldCountdown() {
  phoneOldCooldown.value = 60
  if (phoneOldTimer) clearInterval(phoneOldTimer)
  phoneOldTimer = setInterval(() => {
    phoneOldCooldown.value -= 1
    if (phoneOldCooldown.value <= 0 && phoneOldTimer) {
      clearInterval(phoneOldTimer)
      phoneOldTimer = null
    }
  }, 1000)
}

async function sendOldPhoneCode() {
  if (!(await profileVerifyReady())) return
  phoneOldSending.value = true
  try {
    const res = (await http.post('/user/profile/phone/old-send', profileVerifyExtra(), {
      silent401: true,
    })) as { ok: number; msg?: string }
    if (String(res.ok) === '1') {
      ElMessage.success('验证码已发送，请查收原手机短信')
      startPhoneOldCountdown()
    } else {
      ElMessage.error(res.msg || '发送失败')
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '发送失败')
  } finally {
    phoneOldSending.value = false
  }
}

async function verifyOldPhone() {
  if (!phoneOldCode.value.trim()) {
    ElMessage.warning('请输入原手机验证码')
    return
  }
  phoneOldSending.value = true
  try {
    const res = (await http.post('/user/profile/phone/verify-old', {
      code: phoneOldCode.value.trim(),
    }, { silent401: true })) as { ok: number; step_token?: string; msg?: string }
    if (String(res.ok) === '1' && res.step_token) {
      phoneStepToken.value = res.step_token
      phoneOldCode.value = ''
      phoneStep.value = 'new'
      resetProfileVerifyForStep()
      ElMessage.success('原手机验证通过')
    } else {
      ElMessage.error(res.msg || '验证失败')
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '验证失败')
  } finally {
    phoneOldSending.value = false
  }
}

async function send() {
  if (!phone.value) {
    ElMessage.warning('请输入手机号')
    return
  }
  const r = phoneInputRef.value?.check()
  if (!r?.ok) {
    ElMessage.warning(r?.msg || '手机号格式不正确')
    return
  }
  if (!(await profileVerifyReady())) return
  sending.value = true
  try {
    const body: Record<string, string> = { phone: phone.value, step_token: phoneStepToken.value, ...profileVerifyExtra() }
    const res = (await http.post('/user/profile/phone/send', body, {
      silent401: true,
    })) as { ok: number; msg?: string }
    if (String(res.ok) === '1') {
      ElMessage.success('验证码已发送')
      startCountdown()
    } else {
      ElMessage.error(res.msg || '发送失败')
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '发送失败')
  } finally {
    sending.value = false
  }
}

async function confirm() {
  confirming.value = true
  try {
    const res = (await http.post('/user/profile/phone/confirm', {
      code: code.value,
      step_token: phoneStepToken.value,
    })) as { ok: number; msg?: string }
    if (String(res.ok) === '1') {
      ElMessage.success('手机号验证成功')
      code.value = ''
      phoneStep.value = 'old'
      resetProfileVerifyForStep()
      await load()
    } else {
      ElMessage.error(res.msg || '验证失败')
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '验证失败')
  } finally {
    confirming.value = false
  }
}

// —— 换绑弹窗：打开时重置到第一步并清理上一次的状态 ——
const emailDialog = ref(false)
const phoneDialog = ref(false)

function openEmailDialog() {
  emailValueReset()
  void loadProfileCaptcha()
  emailDialog.value = true
}

function emailValueReset() {
  clearProfileVerify()
  emailStep.value = data.value?.email && session.auth.require_old_email_change ? 'old' : 'new'
  emailOldCode.value = ''
  emailStepToken.value = ''
  emailCode.value = ''
  email.value = ''
  emailCooldown.value = 0
}

function openPhoneDialog() {
  phoneValueReset()
  void loadProfileCaptcha()
  phoneDialog.value = true
}

function phoneValueReset() {
  clearProfileVerify()
  phoneStep.value = data.value?.has_phone && session.auth.require_old_phone_change ? 'old' : 'new'
  phoneOldCode.value = ''
  phoneStepToken.value = ''
  code.value = ''
  phone.value = ''
  cooldown.value = 0
}

// —— 验证当前邮箱/手机（未验证状态补验） ——
const emailVerifyDialog = ref(false)
const phoneVerifyDialog = ref(false)
const emailVerifyCode = ref('')
const phoneVerifyCode = ref('')
const emailVerifySending = ref(false)
const phoneVerifySending = ref(false)
const emailVerifyCooldown = ref(0)
const phoneVerifyCooldown = ref(0)
let emailVerifyTimer: ReturnType<typeof setInterval> | null = null
let phoneVerifyTimer: ReturnType<typeof setInterval> | null = null

// —— 修改邮箱/手机号发验证码前的人机验证（profile_code 场景，本地或外部由设置页控制） ——
const profileCaptcha = ref<CaptchaData>({ enabled: false })
const profileCaptchaAnswer = ref('')
const profileExtFields = ref<Record<string, string>>({})
const profileExtRequired = ref(false)
// 外部验证组件重挂载用的 key（步骤切换时需重新完成验证）
const profileExtKey = ref(0)

async function loadProfileCaptcha() {
  // 选择「外部验证码」时无需本地图形码（后端同样外部优先）。
  if (session.auth.profile_code_external) {
    profileCaptcha.value = { enabled: false }
    return
  }
  try {
    profileCaptcha.value = await fetchCaptcha('profile_code')
    profileCaptchaAnswer.value = ''
  } catch {
    profileCaptcha.value = { enabled: false }
  }
}

// 发码前校验人机验证是否完成；未启用验证时直接放行。
async function profileVerifyReady(): Promise<boolean> {
  if (session.auth.profile_code_external) {
    if (profileExtRequired.value && !hasCaptchaResult(profileExtFields.value)) {
      ElMessage.warning('请先完成人机验证')
      return false
    }
  } else {
    // 等待本地图形码加载完成再判定，避免“未加载=视为通过”导致后端又要求验证。
    if (!profileCaptcha.value.enabled) await loadProfileCaptcha()
    if (profileCaptcha.value.enabled && !profileCaptchaAnswer.value.trim()) {
      ElMessage.warning('请输入图形验证码')
      return false
    }
  }
  return true
}

// 人机验证载荷字段（本地 _code 命名，后端 checkCaptcha 统一回退）。
function profileVerifyExtra(): Record<string, string> {
  const extra: Record<string, string> = { ...profileExtFields.value }
  if (profileCaptcha.value.enabled) {
    extra.captcha_id_code = profileCaptcha.value.id || ''
    extra.captcha_answer_code = profileCaptchaAnswer.value
  }
  return extra
}

// 清空人机验证状态：图形码输入、外部验证字段，并让外部验证组件重挂载（需重新完成验证）。
function clearProfileVerify() {
  profileCaptchaAnswer.value = ''
  profileExtFields.value = {}
  profileExtRequired.value = false
  profileExtKey.value += 1
}

// 切换换绑步骤后重置人机验证：清空输入并换一张新图形码（旧图已被消费）。
function resetProfileVerifyForStep() {
  clearProfileVerify()
  void loadProfileCaptcha()
}

function openEmailVerify() {
  void loadProfileCaptcha()
  emailVerifyDialog.value = true
}

function openPhoneVerify() {
  void loadProfileCaptcha()
  phoneVerifyDialog.value = true
}

function startEmailVerifyCountdown() {
  emailVerifyCooldown.value = 60
  if (emailVerifyTimer) clearInterval(emailVerifyTimer)
  emailVerifyTimer = setInterval(() => {
    emailVerifyCooldown.value -= 1
    if (emailVerifyCooldown.value <= 0 && emailVerifyTimer) {
      clearInterval(emailVerifyTimer)
      emailVerifyTimer = null
    }
  }, 1000)
}

function startPhoneVerifyCountdown() {
  phoneVerifyCooldown.value = 60
  if (phoneVerifyTimer) clearInterval(phoneVerifyTimer)
  phoneVerifyTimer = setInterval(() => {
    phoneVerifyCooldown.value -= 1
    if (phoneVerifyCooldown.value <= 0 && phoneVerifyTimer) {
      clearInterval(phoneVerifyTimer)
      phoneVerifyTimer = null
    }
  }, 1000)
}

async function sendVerifyEmail() {
  if (!(await profileVerifyReady())) return
  emailVerifySending.value = true
  try {
    const res = (await http.post('/user/profile/email/verify-send', profileVerifyExtra(), {
      silent401: true,
    })) as { ok: number; msg?: string }
    if (String(res.ok) === '1') {
      ElMessage.success('验证码已发送，请查收邮箱')
      startEmailVerifyCountdown()
    } else {
      ElMessage.error(res.msg || '发送失败')
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '发送失败')
  } finally {
    emailVerifySending.value = false
  }
}

async function confirmVerifyEmail() {
  if (!emailVerifyCode.value.trim()) {
    ElMessage.warning('请输入邮箱验证码')
    return
  }
  emailVerifySending.value = true
  try {
    await http.post('/user/profile/email/verify-confirm', {
      code: emailVerifyCode.value.trim(),
    }, { silent401: true })
    emailVerifyDialog.value = false
    emailVerifyCode.value = ''
    ElMessage.success('邮箱已验证')
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '验证失败')
  } finally {
    emailVerifySending.value = false
  }
}

async function sendVerifyPhone() {
  if (!(await profileVerifyReady())) return
  phoneVerifySending.value = true
  try {
    const res = (await http.post('/user/profile/phone/verify-send', profileVerifyExtra(), {
      silent401: true,
    })) as { ok: number; msg?: string }
    if (String(res.ok) === '1') {
      ElMessage.success('验证码已发送，请查收手机短信')
      startPhoneVerifyCountdown()
    } else {
      ElMessage.error(res.msg || '发送失败')
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '发送失败')
  } finally {
    phoneVerifySending.value = false
  }
}

async function confirmVerifyPhone() {
  if (!phoneVerifyCode.value.trim()) {
    ElMessage.warning('请输入手机验证码')
    return
  }
  phoneVerifySending.value = true
  try {
    await http.post('/user/profile/phone/verify-confirm', {
      code: phoneVerifyCode.value.trim(),
    }, { silent401: true })
    phoneVerifyDialog.value = false
    phoneVerifyCode.value = ''
    ElMessage.success('手机号已验证')
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '验证失败')
  } finally {
    phoneVerifySending.value = false
  }
}

onMounted(load)
</script>

<template>
  <div>
    <PublicPageHead title="资料与手机号" subtitle="管理账户基本资料、邮箱和手机号。" />

    <div v-if="data" class="profile-grid">
      <div class="art-card profile-card profile-card--name">
        <div class="card-title">
          <span class="card-title__icon"><el-icon><User /></el-icon></span>
          <div>
            <h2>账户名称</h2>
            <p>用于账户识别和通知联系。</p>
          </div>
        </div>
        <div class="name-row">
          <el-input
            v-model="name"
            maxlength="80"
            aria-label="账户名称"
            placeholder="请输入账户名称"
            @keyup.enter="saveName"
          />
          <el-button
            type="primary"
            class="profile-submit"
            :loading="savingName"
            @click="saveName"
          >
            保存名称
          </el-button>
        </div>
      </div>

      <div class="art-card profile-card profile-card--email">
        <div class="card-title">
          <span class="card-title__icon"><el-icon><Message /></el-icon></span>
          <div>
            <h2>邮箱绑定</h2>
            <p>用于登录和接收通知。</p>
          </div>
          <el-button
            class="card-title__tag"
            type="primary"
            plain
            size="small"
            @click="openEmailDialog"
          >
            {{ data.email ? '修改' : '绑定' }}
          </el-button>
        </div>
        <div class="bind-row">
          <span class="bind-row__label">绑定邮箱</span>
          <span class="bind-row__value">{{ data.email || '未绑定' }}</span>
          <el-tag
            v-if="data.email"
            size="small"
            :type="data.email_verified ? 'success' : 'info'"
          >
            {{ data.email_verified ? '已验证' : '未验证' }}
          </el-tag>
          <el-button
            v-if="data.email && !data.email_verified"
            size="small"
            type="warning"
            plain
            @click="openEmailVerify"
          >
            去验证
          </el-button>
        </div>
      </div>

      <div class="art-card profile-card profile-card--phone">
        <div class="card-title">
          <span class="card-title__icon"><el-icon><Cellphone /></el-icon></span>
          <div>
            <h2>手机绑定</h2>
            <p>绑定后可用于短信登录与安全验证。</p>
          </div>
          <el-button
            class="card-title__tag"
            type="primary"
            plain
            size="small"
            @click="openPhoneDialog"
          >
            {{ data.has_phone ? '修改' : '绑定' }}
          </el-button>
        </div>
        <div class="bind-row">
          <span class="bind-row__label">绑定手机</span>
          <span class="bind-row__value">{{ data.has_phone ? data.phone_masked : '未绑定' }}</span>
          <el-tag
            v-if="data.has_phone"
            size="small"
            :type="data.phone_verified ? 'success' : 'info'"
          >
            {{ data.phone_verified ? '已验证' : '未验证' }}
          </el-tag>
          <el-button
            v-if="data.has_phone && !data.phone_verified"
            size="small"
            type="warning"
            plain
            @click="openPhoneVerify"
          >
            去验证
          </el-button>
        </div>
      </div>
    </div>

    <div v-else-if="loadError" class="profile-state" role="alert">
      <p>账户资料加载失败，请稍后重试。</p>
      <el-button size="small" @click="load">重新加载</el-button>
    </div>

    <div v-else class="profile-stack">
      <el-skeleton :rows="3" animated />
    </div>

    <!-- 修改邮箱绑定 -->
    <el-dialog v-model="emailDialog" width="420px" @closed="emailValueReset">
      <template #header>
        <h4 class="dialog-title">修改邮箱绑定</h4>
      </template>
      <el-form label-position="top">
        <!-- 第一步：验证原邮箱；第二步：绑定新邮箱 -->
        <p v-if="data?.email && emailStep === 'old'" class="dialog-step">第一步：验证原邮箱（验证码发送到 {{ data.email }}）</p>
        <el-form-item v-else label="新邮箱">
          <el-input v-model="email" type="email" maxlength="160" placeholder="请输入邮箱" />
        </el-form-item>
        <!-- 修改邮箱/手机号发验证码前的人机验证（profile_code 场景）：两步发码前各需完成一次 -->
        <el-form-item v-if="!session.auth.profile_code_external && profileCaptcha.enabled" label="图形验证码">
          <div class="auth-captcha">
            <el-input v-model="profileCaptchaAnswer" placeholder="请输入图中字符" />
            <button
              v-if="profileCaptcha.image"
              type="button"
              class="auth-captcha__refresh"
              aria-label="刷新验证码"
              @click="loadProfileCaptcha"
            >
              <img :src="profileCaptcha.image" alt="图形验证码" />
            </button>
          </div>
        </el-form-item>
        <ExternalCaptcha
          v-if="session.auth.profile_code_external"
          :key="profileExtKey"
          scene="profile_code"
          @update:fields="profileExtFields = $event"
          @update:required="profileExtRequired = $event"
        />
        <el-form-item v-if="data?.email && emailStep === 'old'" label="原邮箱验证码">
          <div class="code-row">
            <el-input
              v-model="emailOldCode"
              maxlength="6"
              inputmode="numeric"
              placeholder="6 位验证码"
              @keyup.enter="verifyOldEmail"
            />
            <el-button
              :loading="emailOldSending"
              :disabled="emailOldCooldown > 0"
              @click="sendOldEmailCode"
            >
              {{ emailOldCooldown > 0 ? `${emailOldCooldown}s` : '获取验证码' }}
            </el-button>
          </div>
        </el-form-item>
        <el-form-item v-else label="邮箱验证码">
          <div class="code-row">
            <el-input
              v-model="emailCode"
              maxlength="6"
              inputmode="numeric"
              placeholder="6 位验证码"
              @keyup.enter="confirmEmail"
            />
            <el-button
              :loading="emailSending"
              :disabled="emailCooldown > 0"
              @click="sendEmailCode"
            >
              {{ emailCooldown > 0 ? `${emailCooldown}s` : '发送验证码' }}
            </el-button>
          </div>
        </el-form-item>
        <el-button
          v-if="data?.email && emailStep === 'old'"
          type="primary"
          class="profile-submit"
          :loading="emailOldSending"
          @click="verifyOldEmail"
        >
          验证原邮箱
        </el-button>
        <el-button v-else type="primary" class="profile-submit" :loading="emailConfirming" @click="confirmEmail">
          确认{{ data?.email ? '更换' : '绑定' }}邮箱
        </el-button>
      </el-form>
    </el-dialog>

    <!-- 修改手机绑定 -->
    <el-dialog v-model="phoneDialog" width="420px" @closed="phoneValueReset">
      <template #header>
        <h4 class="dialog-title">修改手机绑定</h4>
      </template>
      <el-form label-position="top">
        <!-- 第一步：验证原手机；第二步：绑定/更换新手机 -->
        <p v-if="data?.has_phone && phoneStep === 'old'" class="dialog-step">第一步：验证原手机（验证码发送到 {{ data.phone_masked }}）</p>
        <el-form-item v-else label="新手机号">
          <PhoneInput ref="phoneInputRef" v-model="phone" />
        </el-form-item>
        <!-- 修改邮箱/手机号发验证码前的人机验证（profile_code 场景）：两步发码前各需完成一次 -->
        <el-form-item v-if="!session.auth.profile_code_external && profileCaptcha.enabled" label="图形验证码">
          <div class="auth-captcha">
            <el-input v-model="profileCaptchaAnswer" placeholder="请输入图中字符" />
            <button
              v-if="profileCaptcha.image"
              type="button"
              class="auth-captcha__refresh"
              aria-label="刷新验证码"
              @click="loadProfileCaptcha"
            >
              <img :src="profileCaptcha.image" alt="图形验证码" />
            </button>
          </div>
        </el-form-item>
        <ExternalCaptcha
          v-if="session.auth.profile_code_external"
          :key="profileExtKey"
          scene="profile_code"
          @update:fields="profileExtFields = $event"
          @update:required="profileExtRequired = $event"
        />
        <el-form-item v-if="data?.has_phone && phoneStep === 'old'" label="原手机验证码">
          <div class="code-row">
            <el-input
              v-model="phoneOldCode"
              placeholder="6 位验证码"
              inputmode="numeric"
              maxlength="6"
              @keyup.enter="verifyOldPhone"
            />
            <el-button
              :loading="phoneOldSending"
              :disabled="phoneOldCooldown > 0"
              @click="sendOldPhoneCode"
            >
              {{ phoneOldCooldown > 0 ? `${phoneOldCooldown}s` : '获取验证码' }}
            </el-button>
          </div>
        </el-form-item>
        <el-form-item v-else label="短信验证码">
          <div class="code-row">
            <el-input
              v-model="code"
              placeholder="6 位验证码"
              inputmode="numeric"
              maxlength="6"
              @keyup.enter="confirm"
            />
            <el-button :loading="sending" :disabled="cooldown > 0" @click="send">
              {{ cooldown > 0 ? `${cooldown}s` : '获取验证码' }}
            </el-button>
          </div>
        </el-form-item>
        <el-button
          v-if="data?.has_phone && phoneStep === 'old'"
          type="primary"
          class="profile-submit"
          :loading="phoneOldSending"
          @click="verifyOldPhone"
        >
          验证原手机
        </el-button>
        <el-button v-else type="primary" class="profile-submit" :loading="confirming" @click="confirm">
          确认{{ data?.has_phone ? '更换' : '绑定' }}手机
        </el-button>
      </el-form>
    </el-dialog>

    <!-- 验证当前邮箱 -->
    <el-dialog v-model="emailVerifyDialog" width="420px">
      <template #header>
        <h4 class="dialog-title">验证邮箱</h4>
      </template>
      <p class="dialog-step">验证码将发送到 {{ data?.email }}</p>
      <el-form label-position="top">
        <!-- 修改邮箱/手机号发验证码前的人机验证（profile_code 场景） -->
        <el-form-item v-if="!session.auth.profile_code_external && profileCaptcha.enabled" label="图形验证码">
          <div class="auth-captcha">
            <el-input v-model="profileCaptchaAnswer" placeholder="请输入图中字符" />
            <button
              v-if="profileCaptcha.image"
              type="button"
              class="auth-captcha__refresh"
              aria-label="刷新验证码"
              @click="loadProfileCaptcha"
            >
              <img :src="profileCaptcha.image" alt="图形验证码" />
            </button>
          </div>
        </el-form-item>
        <ExternalCaptcha
          v-if="session.auth.profile_code_external"
          :key="profileExtKey"
          scene="profile_code"
          @update:fields="profileExtFields = $event"
          @update:required="profileExtRequired = $event"
        />
        <el-form-item label="邮箱验证码">
          <div class="code-row">
            <el-input
              v-model="emailVerifyCode"
              maxlength="6"
              inputmode="numeric"
              placeholder="6 位验证码"
              @keyup.enter="confirmVerifyEmail"
            />
            <el-button
              :loading="emailVerifySending"
              :disabled="emailVerifyCooldown > 0"
              @click="sendVerifyEmail"
            >
              {{ emailVerifyCooldown > 0 ? `${emailVerifyCooldown}s` : '获取验证码' }}
            </el-button>
          </div>
        </el-form-item>
        <el-button type="primary" class="profile-submit" :loading="emailVerifySending" @click="confirmVerifyEmail">
          确认验证
        </el-button>
      </el-form>
    </el-dialog>

    <!-- 验证当前手机 -->
    <el-dialog v-model="phoneVerifyDialog" width="420px">
      <template #header>
        <h4 class="dialog-title">验证手机号</h4>
      </template>
      <p class="dialog-step">验证码将发送到 {{ data?.phone_masked }}</p>
      <el-form label-position="top">
        <!-- 修改邮箱/手机号发验证码前的人机验证（profile_code 场景） -->
        <el-form-item v-if="!session.auth.profile_code_external && profileCaptcha.enabled" label="图形验证码">
          <div class="auth-captcha">
            <el-input v-model="profileCaptchaAnswer" placeholder="请输入图中字符" />
            <button
              v-if="profileCaptcha.image"
              type="button"
              class="auth-captcha__refresh"
              aria-label="刷新验证码"
              @click="loadProfileCaptcha"
            >
              <img :src="profileCaptcha.image" alt="图形验证码" />
            </button>
          </div>
        </el-form-item>
        <ExternalCaptcha
          v-if="session.auth.profile_code_external"
          :key="profileExtKey"
          scene="profile_code"
          @update:fields="profileExtFields = $event"
          @update:required="profileExtRequired = $event"
        />
        <el-form-item label="手机验证码">
          <div class="code-row">
            <el-input
              v-model="phoneVerifyCode"
              maxlength="6"
              inputmode="numeric"
              placeholder="6 位验证码"
              @keyup.enter="confirmVerifyPhone"
            />
            <el-button
              :loading="phoneVerifySending"
              :disabled="phoneVerifyCooldown > 0"
              @click="sendVerifyPhone"
            >
              {{ phoneVerifyCooldown > 0 ? `${phoneVerifyCooldown}s` : '获取验证码' }}
            </el-button>
          </div>
        </el-form-item>
        <el-button type="primary" class="profile-submit" :loading="phoneVerifySending" @click="confirmVerifyPhone">
          确认验证
        </el-button>
      </el-form>
    </el-dialog>
  </div>
</template>

<style scoped>
/* 压缩弹窗标题与正文的默认留白（EP 默认 header 下边距 + 全局 body 上下 25px 叠加过大） */
:deep(.el-dialog__header) {
  padding-bottom: 0;
}

:deep(.el-dialog__body) {
  padding-top: 14px !important;
}

/* 弹窗标题：标题下方一条分隔横线 */
.dialog-title {
  margin: 0;
  padding-bottom: 12px;
  color: var(--art-gray-900);
  font-size: 16px;
  font-weight: 600;
  border-bottom: 1px solid var(--art-card-border);
}

/* 图形验证码：输入框 + 验证码图片（点击图片刷新），与注册/找回页一致 */
.auth-captcha {
  display: flex;
  gap: 10px;
  width: 100%;
}

.auth-captcha .el-input {
  flex: 1;
  min-width: 0;
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

.profile-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
  align-items: start;
}

.profile-card {
  padding: 21px;
}

/* 绑定信息行（邮箱/手机卡片） */
.bind-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-top: 16px;
  border-top: 1px solid var(--art-card-border);
}

.bind-row__label {
  flex-shrink: 0;
  color: var(--art-gray-500);
  font-size: 12px;
}

.bind-row__value {
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
  color: var(--art-gray-900);
  font-size: 14px;
  font-weight: 500;
}

/* 换绑弹窗内的步骤提示 */
.dialog-step {
  margin: 0 0 14px;
  padding: 9px 12px;
  color: var(--theme-color);
  font-size: 12px;
  background: var(--theme-color-soft);
  border-radius: var(--radius-sm);
}

/* 账户名称：横向一行（标题 + 输入框 + 保存按钮） */
.profile-card--name {
  grid-column: 1 / -1;
  display: flex;
  align-items: center;
  gap: 24px;
}

.profile-card--name .card-title {
  flex-shrink: 0;
  margin-bottom: 0;
}

.name-row {
  display: flex;
  flex: 1;
  gap: 10px;
  min-width: 0;
}

.name-row .el-input {
  flex: 1;
  min-width: 0;
}

.name-row .profile-submit {
  flex-shrink: 0;
  min-width: 120px;
}

.card-title {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 18px;
}

.card-title__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  color: var(--theme-color);
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
}

.card-title h2 {
  margin: 0;
  color: var(--art-gray-800);
  font-size: 15px;
  font-weight: 600;
}

.card-title p {
  margin: 4px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
}

.card-title__tag {
  margin-left: auto;
  flex-shrink: 0;
}

.code-row {
  display: flex;
  flex-wrap: wrap;
  width: 100%;
  gap: 9px;
}

.code-row .el-input {
  flex: 1 1 160px;
  min-width: 0;
}

.profile-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 48px 20px;
  text-align: center;
}

.profile-state p {
  margin: 0;
  color: var(--art-gray-600);
  font-size: 14px;
}

.profile-submit {
  min-width: 140px;
}

.profile-stack {
  display: grid;
  gap: 14px;
}

@media (max-width: 900px) {
  .profile-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .profile-card--name {
    flex-direction: column;
    align-items: stretch;
    gap: 16px;
  }

  .name-row {
    flex-direction: column;
  }

  .name-row .profile-submit {
    width: 100%;
  }
}
</style>
