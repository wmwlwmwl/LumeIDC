<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { User, Lock, Key, ArrowRight, Refresh } from '@element-plus/icons-vue'
import LoginLeftView from '../components/core/views/login/LoginLeftView.vue'
import AuthTopBar from '../components/core/views/login/AuthTopBar.vue'
import { adminLogin, fetchAdminCaptcha, type AdminCaptcha } from '../admin/api'
import { useSession } from '../http/session'

const router = useRouter()
const route = useRoute()
const session = useSession()

const formRef = ref<FormInstance>()
const form = reactive({ account: '', password: '', totp: '', captchaAnswer: '' })
const captcha = ref<AdminCaptcha>({ enabled: false })
const captchaError = ref(false)
const totpRequired = ref(false)
const loading = ref(false)

const rules: FormRules = {
  account: [{ required: true, message: '请输入管理员账号', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

async function loadCaptcha() {
  captchaError.value = false
  try {
    captcha.value = await fetchAdminCaptcha()
    form.captchaAnswer = ''
  } catch {
    captcha.value = { enabled: false }
    captchaError.value = true
  }
}

async function submit() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  if (captcha.value.enabled && !form.captchaAnswer) {
    ElMessage.warning('请输入图形验证码')
    return
  }
  loading.value = true
  try {
    const body: Record<string, string> = {
      email: form.account,
      password: form.password,
      totp: form.totp,
    }
    if (captcha.value.enabled) {
      body.captcha_id = captcha.value.id || ''
      body.captcha_answer = form.captchaAnswer
    }
    await adminLogin(body)
    await import('../http/session').then((m) => m.loadSession({ force: true }))
    const next = String(route.query.next || '/')
    router.replace(next.startsWith('/') ? next : '/')
  } catch (err: unknown) {
    const data = (err as { data?: { totp_required?: boolean } }).data
    if (data?.totp_required) {
      totpRequired.value = true
      ElMessage.warning('该账户已启用两步验证，请输入验证码')
    } else {
      ElMessage.error((err as Error).message || '登录失败')
    }
    // 登录失败可能触发/刷新强制验证码，重新拉取。
    await loadCaptcha()
  } finally {
    loading.value = false
  }
}

onMounted(loadCaptcha)
</script>

<template>
  <div class="admin-login">
    <LoginLeftView />

    <div class="admin-login__right">
      <AuthTopBar />

      <div class="admin-login__form">
        <h3 class="admin-login__title">欢迎回来</h3>
        <p class="admin-login__subtitle">{{ session.site.name }} · 管理后台</p>

        <el-form
          ref="formRef"
          :model="form"
          :rules="rules"
          label-position="top"
          class="admin-login__inner"
          @keyup.enter="submit"
        >
          <el-form-item prop="account">
            <el-input
              v-model.trim="form.account"
              size="large"
              placeholder="管理员账号（邮箱）"
              autocomplete="username"
              :disabled="loading"
            >
              <template #prefix><el-icon><User /></el-icon></template>
            </el-input>
          </el-form-item>

          <el-form-item prop="password">
            <el-input
              v-model.trim="form.password"
              size="large"
              type="password"
              show-password
              placeholder="登录密码"
              autocomplete="current-password"
              :disabled="loading"
            >
              <template #prefix><el-icon><Lock /></el-icon></template>
            </el-input>
          </el-form-item>

          <el-form-item v-if="totpRequired" prop="totp">
            <el-input
              v-model.trim="form.totp"
              size="large"
              placeholder="两步验证码"
              inputmode="numeric"
              autocomplete="one-time-code"
              :disabled="loading"
            >
              <template #prefix><el-icon><Key /></el-icon></template>
            </el-input>
          </el-form-item>

          <el-form-item v-if="captcha.enabled" prop="captchaAnswer">
            <div class="admin-login__captcha">
              <el-input
                v-model.trim="form.captchaAnswer"
                size="large"
                placeholder="请输入图中字符"
                :disabled="loading"
              />
              <button
                v-if="captcha.image"
                type="button"
                class="admin-login__captcha-refresh"
                aria-label="刷新验证码"
                @click="loadCaptcha"
              >
                <img :src="captcha.image" alt="图形验证码" />
              </button>
            </div>
          </el-form-item>
          <div v-else-if="captchaError" class="admin-login__captcha-error" role="alert">
            <span>验证码加载失败</span>
            <button type="button" @click="loadCaptcha"><el-icon><Refresh /></el-icon>重试</button>
          </div>

          <el-button
            type="primary"
            size="large"
            class="admin-login__submit"
            :loading="loading"
            @click="submit"
          >
            进入管理后台 <el-icon><ArrowRight /></el-icon>
          </el-button>
        </el-form>

        <div class="admin-login__foot">
          <RouterLink to="/" target="_blank">← 返回前台</RouterLink>
          <span>HMAC Session · Secure</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.admin-login {
  display: flex;
  width: 100%;
  height: 100vh;
}
.admin-login__right {
  position: relative;
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 48px 20px;
  overflow: auto;
}
.admin-login__form {
  width: min(100%, 440px);
}
.admin-login__title {
  margin: 0;
  color: var(--art-gray-900);
  font-size: 30px;
  font-weight: 600;
  letter-spacing: -0.02em;
}
.admin-login__subtitle {
  margin: 10px 0 0;
  color: var(--art-gray-600);
  font-size: 14px;
}
.admin-login__inner {
  margin-top: 25px;
}
.admin-login__captcha {
  display: flex;
  gap: 10px;
  width: 100%;
}
.admin-login__captcha-refresh {
  flex-shrink: 0;
  padding: 0;
  line-height: 0;
  background: transparent;
  border: 0;
  border-radius: var(--radius-sm);
  cursor: pointer;
}
.admin-login__captcha-refresh img {
  height: 40px;
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-sm);
}
.admin-login__captcha-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 8px 12px;
  margin-bottom: 18px;
  color: var(--el-color-danger);
  font-size: 12px;
  background: var(--el-color-danger-light-9);
  border-radius: var(--radius-sm);
}
.admin-login__captcha-error button {
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
.admin-login__submit {
  width: 100%;
  min-height: 44px;
  margin-top: 6px;
  justify-content: center;
  gap: 6px;
}
.admin-login__foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 24px;
  color: var(--art-gray-500);
  font-size: 12px;
}
.admin-login__foot a:hover {
  color: var(--theme-color);
}
@media (max-width: 640px) {
  .admin-login__right {
    padding: 72px 28px 32px;
  }
  .admin-login__title {
    font-size: 26px;
  }
}
</style>
