<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useAdminSettings } from '../admin/useSettings'
import { saveAdminSettings } from '../admin/api'

const { loading, saving, cfg, load, save, flags } = useAdminSettings()

const REGISTRATION_KEYS = [
  'registration_email_enabled',
  'registration_phone_enabled',
  'registration_email_verification_required',
  'registration_phone_verification_required',
  'registration_require_both',
  'login_phone_otp_enabled',
  'profile_change_require_old_email',
  'profile_change_require_old_phone',
]

function saveRegistration() {
  save('registration', flags(REGISTRATION_KEYS))
}

// —— 人机验证：每个场景三选（关闭 / 本地图形码 / 外部验证码）——
type Mode = 'off' | 'local' | 'external'
// 注册、注册发码、找回密码、修改邮箱/手机发验证码、登录、手机验证码登录 支持外部；管理员登录仅 本地/关闭。
const regMode = ref<Mode>('off')
const regCodeMode = ref<Mode>('off')
const forgotMode = ref<Mode>('off')
const profileMode = ref<Mode>('off')
const loginMode = ref<Mode>('off')
const phoneCodeMode = ref<Mode>('off')
const adminLoginMode = ref<'off' | 'local'>('off')

// 本地场景开关键：与上面的 radio 一一对应
const LOCAL_KEYS = [
  'captcha_register_enabled',
  'captcha_login_enabled',
  'captcha_register_code_enabled',
  'captcha_forgot_code_enabled',
  'captcha_profile_code_enabled',
  'captcha_phone_login_code_enabled',
]
// 外部场景开关键（注册发码/找回密码/改绑发码为新增外部场景）
const EXTERNAL_KEYS = [
  'external_captcha_register_enabled',
  'external_captcha_login_enabled',
  'external_captcha_register_code_enabled',
  'external_captcha_forgot_code_enabled',
  'external_captcha_profile_code_enabled',
  'external_captcha_phone_login_code_enabled',
]

function flagOf(key: string): string {
  return cfg[key] === '1' ? '1' : '0'
}

async function init() {
  await load()
  regMode.value = cfg['external_captcha_register_enabled'] === '1'
    ? 'external'
    : cfg['captcha_register_enabled'] === '1'
      ? 'local'
      : 'off'
  loginMode.value = cfg['external_captcha_login_enabled'] === '1'
    ? 'external'
    : cfg['captcha_login_enabled'] === '1'
      ? 'local'
      : 'off'
  regCodeMode.value = cfg['external_captcha_register_code_enabled'] === '1'
    ? 'external'
    : cfg['captcha_register_code_enabled'] === '1'
      ? 'local'
      : 'off'
  forgotMode.value = cfg['external_captcha_forgot_code_enabled'] === '1'
    ? 'external'
    : cfg['captcha_forgot_code_enabled'] === '1'
      ? 'local'
      : 'off'
  profileMode.value = cfg['external_captcha_profile_code_enabled'] === '1'
    ? 'external'
    : cfg['captcha_profile_code_enabled'] === '1'
      ? 'local'
      : 'off'
  phoneCodeMode.value = cfg['external_captcha_phone_login_code_enabled'] === '1'
    ? 'external'
    : cfg['captcha_phone_login_code_enabled'] === '1'
      ? 'local'
      : 'off'
  adminLoginMode.value = cfg['captcha_admin_login_enabled'] === '1' ? 'local' : 'off'
}

async function saveCaptchaSetup() {
  const modes = [regMode.value, regCodeMode.value, forgotMode.value, profileMode.value, loginMode.value, phoneCodeMode.value]
  // 任一场景选择「外部」时必须先配置好服务商
  if (modes.includes('external') && !cfg['captcha_provider']) {
    ElMessage.warning('选择「外部」验证码前，请先配置外部验证码服务商')
    return
  }
  // 本地图形码主开关：任一场景未设为「关闭」即开启（找回密码、发码类等未接入外部的场景由它兜底）。
  const anyOn = modes.some((m) => m !== 'off') || adminLoginMode.value === 'local'
  const local: Record<string, string> = {
    captcha_enabled: anyOn ? '1' : '0',
    captcha_register_enabled: regMode.value === 'local' ? '1' : '0',
    captcha_login_enabled: loginMode.value === 'local' ? '1' : '0',
    captcha_admin_login_enabled: adminLoginMode.value === 'local' ? '1' : '0',
    captcha_register_code_enabled: regCodeMode.value === 'local' ? '1' : '0',
    captcha_forgot_code_enabled: forgotMode.value === 'local' ? '1' : '0',
    captcha_profile_code_enabled: profileMode.value === 'local' ? '1' : '0',
    captcha_phone_login_code_enabled: phoneCodeMode.value === 'local' ? '1' : '0',
  }
  const ext: Record<string, string> = {
    captcha_provider: cfg['captcha_provider'] || '',
    captcha_geetest_id: cfg['captcha_geetest_id'] || '',
    captcha_geetest_key: cfg['captcha_geetest_key'] || '',
    captcha_vaptcha_vid: cfg['captcha_vaptcha_vid'] || '',
    captcha_vaptcha_key: cfg['captcha_vaptcha_key'] || '',
    captcha_corptcha_site_key: cfg['captcha_corptcha_site_key'] || '',
    captcha_corptcha_secret: cfg['captcha_corptcha_secret'] || '',
    // 携带本地场景值供后端互斥校验（同一场景 radio 单值，本地/外部不会同时为 1）
    captcha_register_enabled: regMode.value === 'local' ? '1' : '0',
    captcha_login_enabled: loginMode.value === 'local' ? '1' : '0',
    captcha_enabled: anyOn ? '1' : '0',
    external_captcha_register_enabled: regMode.value === 'external' ? '1' : '0',
    external_captcha_login_enabled: loginMode.value === 'external' ? '1' : '0',
    external_captcha_register_code_enabled: regCodeMode.value === 'external' ? '1' : '0',
    external_captcha_forgot_code_enabled: forgotMode.value === 'external' ? '1' : '0',
    external_captcha_profile_code_enabled: profileMode.value === 'external' ? '1' : '0',
    external_captcha_phone_login_code_enabled: phoneCodeMode.value === 'external' ? '1' : '0',
  }
  saving.value = 'captcha'
  try {
    // 先存本地（把外部场景的本地开关关掉），再存外部；任一失败即中止，避免半套配置。
    const r1 = await saveAdminSettings('captcha', local)
    if (!(r1.ok === true || String(r1.ok) === '1')) {
      ElMessage.error(r1.msg || '保存失败')
      return
    }
    const r2 = await saveAdminSettings('external_captcha', ext)
    if (!(r2.ok === true || String(r2.ok) === '1')) {
      ElMessage.error(r2.msg || '保存失败')
      return
    }
    ElMessage.success('已保存')
    await init()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    saving.value = ''
  }
}

init()
</script>

<template>
  <div class="art-full-height" v-loading="loading">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>登录与验证</h4>
            <p>注册 / 登录方式，以及人机验证（本地图形码与第三方验证码）。</p>
          </div>
        </div>
      </template>

      <el-divider content-position="left">注册与登录</el-divider>
      <div class="admin-check-grid">
        <el-checkbox v-model="cfg['registration_email_enabled']" true-value="1" false-value="0">允许邮箱注册</el-checkbox>
        <el-checkbox v-model="cfg['registration_phone_enabled']" true-value="1" false-value="0">允许手机号注册</el-checkbox>
        <el-checkbox v-model="cfg['registration_email_verification_required']" true-value="1" false-value="0">邮箱注册需验证码</el-checkbox>
        <el-checkbox v-model="cfg['registration_phone_verification_required']" true-value="1" false-value="0">手机注册需验证码</el-checkbox>
        <el-checkbox v-model="cfg['registration_require_both']" true-value="1" false-value="0">邮箱+手机号同时注册（不分注册方式，两项都必填）</el-checkbox>
        <el-checkbox v-model="cfg['login_phone_otp_enabled']" true-value="1" false-value="0">允许手机验证码登录</el-checkbox>
        <el-checkbox v-model="cfg['profile_change_require_old_email']" true-value="1" false-value="0">修改邮箱需先验证原邮箱（关闭=可直接修改）</el-checkbox>
        <el-checkbox v-model="cfg['profile_change_require_old_phone']" true-value="1" false-value="0">修改手机号需先验证原手机（关闭=可直接修改）</el-checkbox>
      </div>
      <div class="admin-section__actions">
        <el-button type="primary" :loading="saving === 'registration'" @click="saveRegistration">保存注册登录设置</el-button>
      </div>

      <el-divider content-position="left">人机验证</el-divider>
      <p class="mb-3 text-xs text-g-500">
        每个场景可分别选择验证方式（关闭 / 本地图形码 / 外部验证码）；「外部验证码」需先在下方配置服务商。
      </p>

      <div class="captcha-scene-row">
        <span class="captcha-scene-label">注册</span>
        <el-radio-group v-model="regMode">
          <el-radio value="off">关闭</el-radio>
          <el-radio value="local">本地图形码</el-radio>
          <el-radio value="external">外部验证码</el-radio>
        </el-radio-group>
      </div>
      <div class="captcha-scene-row">
        <span class="captcha-scene-label">注册发验证码</span>
        <el-radio-group v-model="regCodeMode">
          <el-radio value="off">关闭</el-radio>
          <el-radio value="local">本地图形码</el-radio>
          <el-radio value="external">外部验证码</el-radio>
        </el-radio-group>
      </div>
      <div class="captcha-scene-row">
        <span class="captcha-scene-label">找回密码</span>
        <el-radio-group v-model="forgotMode">
          <el-radio value="off">关闭</el-radio>
          <el-radio value="local">本地图形码</el-radio>
          <el-radio value="external">外部验证码</el-radio>
        </el-radio-group>
      </div>
      <div class="captcha-scene-row">
        <span class="captcha-scene-label">修改邮箱/手机号发验证码</span>
        <el-radio-group v-model="profileMode">
          <el-radio value="off">关闭</el-radio>
          <el-radio value="local">本地图形码</el-radio>
          <el-radio value="external">外部验证码</el-radio>
        </el-radio-group>
      </div>
      <div class="captcha-scene-row">
        <span class="captcha-scene-label">登录</span>
        <el-radio-group v-model="loginMode">
          <el-radio value="off">关闭</el-radio>
          <el-radio value="local">本地图形码</el-radio>
          <el-radio value="external">外部验证码</el-radio>
        </el-radio-group>
      </div>
      <div class="captcha-scene-row">
        <span class="captcha-scene-label">手机验证码登录</span>
        <el-radio-group v-model="phoneCodeMode">
          <el-radio value="off">关闭</el-radio>
          <el-radio value="local">本地图形码</el-radio>
          <el-radio value="external">外部验证码</el-radio>
        </el-radio-group>
      </div>
      <div class="captcha-scene-row">
        <span class="captcha-scene-label">管理员登录</span>
        <el-radio-group v-model="adminLoginMode">
          <el-radio value="off">关闭</el-radio>
          <el-radio value="local">本地图形码</el-radio>
        </el-radio-group>
      </div>

      <!-- 外部验证码服务商（任一场景选「外部」时需配置） -->
      <div class="admin-form-grid mt-3">
        <el-form-item label="外部验证码服务商">
          <el-select v-model="cfg['captcha_provider']" clearable class="w-full">
            <el-option label="Geetest" value="geetest" />
            <el-option label="Vaptcha" value="vaptcha" />
            <el-option label="Corptcha" value="corptcha" />
          </el-select>
        </el-form-item>
      </div>
      <div v-if="cfg['captcha_provider']" class="admin-form-grid">
        <template v-if="cfg['captcha_provider'] === 'geetest'">
          <el-form-item label="Geetest ID"><el-input v-model="cfg['captcha_geetest_id']" /></el-form-item>
          <el-form-item label="Geetest Key（留空保持不变）">
            <el-input v-model="cfg['captcha_geetest_key']" type="password" show-password />
          </el-form-item>
        </template>
        <template v-if="cfg['captcha_provider'] === 'vaptcha'">
          <el-form-item label="Vaptcha VID"><el-input v-model="cfg['captcha_vaptcha_vid']" /></el-form-item>
          <el-form-item label="Vaptcha Key（留空保持不变）">
            <el-input v-model="cfg['captcha_vaptcha_key']" type="password" show-password />
          </el-form-item>
        </template>
        <template v-if="cfg['captcha_provider'] === 'corptcha'">
          <el-form-item label="Corptcha Site Key"><el-input v-model="cfg['captcha_corptcha_site_key']" /></el-form-item>
          <el-form-item label="Corptcha Secret（留空保持不变）">
            <el-input v-model="cfg['captcha_corptcha_secret']" type="password" show-password />
          </el-form-item>
        </template>
      </div>

      <div class="admin-section__actions">
        <el-button type="primary" :loading="saving === 'captcha'" @click="saveCaptchaSetup">保存人机验证</el-button>
      </div>
    </ElCard>
  </div>
</template>

<style scoped>
.captcha-scene-row {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 7px 0;
}

.captcha-scene-label {
  width: 200px;
  flex-shrink: 0;
  color: var(--art-gray-700);
  font-size: 13px;
}
</style>