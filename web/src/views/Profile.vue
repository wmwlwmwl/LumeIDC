<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { ElMessage } from 'element-plus'
import { Cellphone, Message, User } from '@element-plus/icons-vue'
import { fetchProfile, updateProfile, type ProfileData } from '../api/user'
import { http } from '../http/index'
import PublicPageHead from '@/components/public/PublicPageHead.vue'
import { useSession } from '../http/session'

const data = ref<ProfileData | null>(null)
const loadError = ref(false)
const session = useSession()
let emailTimer: ReturnType<typeof setInterval> | null = null
let phoneTimer: ReturnType<typeof setInterval> | null = null

const name = ref('')
const email = ref('')
const emailPassword = ref('')
const emailCode = ref('')
const emailSending = ref(false)
const emailConfirming = ref(false)
const emailCooldown = ref(0)
const savingName = ref(false)

const currentPassword = ref('')
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

async function sendEmailCode() {
  if (!email.value.trim() || !emailPassword.value) {
    ElMessage.warning('请输入新邮箱和当前密码')
    return
  }
  emailSending.value = true
  try {
    await http.post('/user/profile/email/send', {
      email: email.value.trim(),
      current_password: emailPassword.value,
    })
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
    })) as { name: string; email: string }
    emailCode.value = ''
    emailPassword.value = ''
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

async function send() {
  if (!phone.value) {
    ElMessage.warning('请输入手机号')
    return
  }
  sending.value = true
  try {
    const body: Record<string, string> = { phone: phone.value }
    if (data.value?.has_phone) body.current_password = currentPassword.value
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
    })) as { ok: number; msg?: string }
    if (String(res.ok) === '1') {
      ElMessage.success('手机号验证成功')
      code.value = ''
      currentPassword.value = ''
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

      <div class="art-card profile-card">
        <div class="card-title">
          <span class="card-title__icon"><el-icon><Message /></el-icon></span>
          <div>
            <h2>登录邮箱</h2>
            <p>用于登录和接收通知。</p>
          </div>
          <el-tag
            class="card-title__tag"
            size="small"
            :type="data.email_verified ? 'success' : 'info'"
          >
            {{ data.email_verified ? '已验证' : '未验证' }}
          </el-tag>
        </div>
        <el-form label-position="top">
          <el-form-item label="当前邮箱">
            <el-input :model-value="data.email" disabled
              :placeholder="data.email ? '' : '未绑定邮箱'" />
          </el-form-item>
          <el-form-item label="当前密码">
            <el-input
              v-model="emailPassword"
              type="password"
              show-password
              autocomplete="current-password"
              placeholder="验证当前密码"
            />
          </el-form-item>
          <el-form-item label="新邮箱">
            <div class="code-row">
              <el-input
                v-model="email"
                type="email"
                maxlength="160"
                placeholder="name@example.com"
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
          <el-form-item label="邮箱验证码">
            <el-input
              v-model="emailCode"
              maxlength="6"
              inputmode="numeric"
              placeholder="6 位验证码"
              @keyup.enter="confirmEmail"
            />
          </el-form-item>
          <el-button
            type="primary"
            class="profile-submit"
            :loading="emailConfirming"
            @click="confirmEmail"
          >
            确认更换邮箱
          </el-button>
        </el-form>
      </div>

      <div class="art-card profile-card">
        <div class="card-title">
          <span class="card-title__icon"><el-icon><Cellphone /></el-icon></span>
          <div>
            <h2>手机号</h2>
            <p>
              {{
                data.has_phone
                  ? '更换需先验证当前登录密码。'
                  : '绑定后可用于短信登录与安全验证。'
              }}
            </p>
          </div>
          <el-tag
            class="card-title__tag"
            size="small"
            :type="data.phone_verified ? 'success' : 'info'"
          >
            {{ data.has_phone ? (data.phone_verified ? '已验证' : '未验证') : '未绑定' }}
          </el-tag>
        </div>
        <el-form label-position="top">
          <el-form-item label="当前手机号">
            <el-input :model-value="data.has_phone ? data.phone_masked : ''" disabled
              :placeholder="data.has_phone ? '' : '未绑定手机号'" />
          </el-form-item>
          <el-form-item v-if="data.has_phone" label="当前密码">
            <el-input
              v-model="currentPassword"
              type="password"
              show-password
              autocomplete="current-password"
              placeholder="验证当前密码"
            />
          </el-form-item>
          <el-form-item :label="data.has_phone ? '新手机号' : '手机号'">
            <div class="code-row">
              <el-input
                v-model="phone"
                placeholder="请输入手机号"
                inputmode="numeric"
              />
              <el-button :loading="sending" :disabled="cooldown > 0" @click="send">
                {{ cooldown > 0 ? `${cooldown}s` : '获取验证码' }}
              </el-button>
            </div>
          </el-form-item>
          <el-form-item label="短信验证码">
            <el-input
              v-model="code"
              placeholder="6 位验证码"
              inputmode="numeric"
              maxlength="6"
              @keyup.enter="confirm"
            />
          </el-form-item>
          <el-button
            type="primary"
            class="profile-submit"
            :loading="confirming"
            @click="confirm"
          >
            确认{{ data.has_phone ? '更换' : '绑定' }}
          </el-button>
        </el-form>
      </div>
    </div>

    <div v-else-if="loadError" class="profile-state" role="alert">
      <p>账户资料加载失败，请稍后重试。</p>
      <el-button size="small" @click="load">重新加载</el-button>
    </div>

    <div v-else class="profile-stack">
      <el-skeleton :rows="3" animated />
    </div>
  </div>
</template>

<style scoped>
.profile-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
  align-items: start;
}

.profile-card {
  padding: 21px;
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
