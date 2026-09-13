<script setup lang="ts">
import { useAdminSettings } from '../admin/useSettings'

const { loading, saving, cfg, load, save, flags } = useAdminSettings()

const LOCAL_CAPTCHA_KEYS = [
  'captcha_enabled',
  'captcha_register_enabled',
  'captcha_login_enabled',
  'captcha_admin_login_enabled',
  'captcha_email_code_enabled',
  'captcha_phone_code_enabled',
  'captcha_password_reset_enabled',
]
const EXTERNAL_CAPTCHA_KEYS = [
  'external_captcha_register_enabled',
  'external_captcha_login_enabled',
  'external_captcha_phone_login_code_enabled',
]
const REGISTRATION_KEYS = [
  'registration_email_enabled',
  'registration_phone_enabled',
  'registration_email_verification_required',
  'registration_phone_verification_required',
  'registration_show_all_methods',
  'login_phone_otp_enabled',
]

function saveLocalCaptcha() {
  save('captcha', flags(LOCAL_CAPTCHA_KEYS))
}
function saveExternalCaptcha() {
  save('external_captcha', {
    captcha_provider: cfg['captcha_provider'] || '',
    captcha_geetest_id: cfg['captcha_geetest_id'] || '',
    captcha_geetest_key: cfg['captcha_geetest_key'] || '',
    captcha_vaptcha_vid: cfg['captcha_vaptcha_vid'] || '',
    captcha_vaptcha_key: cfg['captcha_vaptcha_key'] || '',
    captcha_corptcha_site_key: cfg['captcha_corptcha_site_key'] || '',
    captcha_corptcha_secret: cfg['captcha_corptcha_secret'] || '',
    ...flags(EXTERNAL_CAPTCHA_KEYS),
  })
}
function saveRegistration() {
  save('registration', flags(REGISTRATION_KEYS))
}

load()
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
        <el-checkbox v-model="cfg['registration_show_all_methods']" true-value="1" false-value="0">展示全部注册方式</el-checkbox>
        <el-checkbox v-model="cfg['login_phone_otp_enabled']" true-value="1" false-value="0">允许手机验证码登录</el-checkbox>
      </div>
      <div class="admin-section__actions">
        <el-button type="primary" :loading="saving === 'registration'" @click="saveRegistration">保存注册登录设置</el-button>
      </div>

      <el-divider content-position="left">图形验证码（本地）</el-divider>
      <p class="mb-3 text-xs text-g-500">本地图形验证码（本站生成）；与「外部验证码」同一场景不可同时启用。</p>
      <el-checkbox v-model="cfg['captcha_enabled']" true-value="1" false-value="0" class="mb-3 block">启用图形验证码</el-checkbox>
      <div class="admin-check-grid">
        <el-checkbox v-model="cfg['captcha_register_enabled']" true-value="1" false-value="0">注册</el-checkbox>
        <el-checkbox v-model="cfg['captcha_login_enabled']" true-value="1" false-value="0">登录</el-checkbox>
        <el-checkbox v-model="cfg['captcha_admin_login_enabled']" true-value="1" false-value="0">管理员登录</el-checkbox>
        <el-checkbox v-model="cfg['captcha_email_code_enabled']" true-value="1" false-value="0">邮箱验证码</el-checkbox>
        <el-checkbox v-model="cfg['captcha_phone_code_enabled']" true-value="1" false-value="0">手机验证码</el-checkbox>
        <el-checkbox v-model="cfg['captcha_password_reset_enabled']" true-value="1" false-value="0">找回密码</el-checkbox>
      </div>
      <div class="admin-section__actions">
        <el-button type="primary" :loading="saving === 'captcha'" @click="saveLocalCaptcha">保存图形验证码</el-button>
      </div>

      <el-divider content-position="left">外部验证码</el-divider>
      <p class="mb-3 text-xs text-g-500">第三方人机验证（Geetest / Vaptcha / Corptcha）。</p>
      <div class="admin-form-grid">
        <el-form-item label="服务商">
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
      <div class="admin-check-grid mt-2">
        <el-checkbox v-model="cfg['external_captcha_register_enabled']" true-value="1" false-value="0">注册场景启用</el-checkbox>
        <el-checkbox v-model="cfg['external_captcha_login_enabled']" true-value="1" false-value="0">登录场景启用</el-checkbox>
        <el-checkbox v-model="cfg['external_captcha_phone_login_code_enabled']" true-value="1" false-value="0">手机验证码登录启用</el-checkbox>
      </div>
      <div class="admin-section__actions">
        <el-button type="primary" :loading="saving === 'external_captcha'" @click="saveExternalCaptcha">保存外部验证码</el-button>
      </div>
    </ElCard>
  </div>
</template>
