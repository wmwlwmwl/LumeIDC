<script setup lang="ts">
import { computed } from 'vue'
import { useAdminSettings } from '../admin/useSettings'

const { loading, saving, cfg, load, save, flags } = useAdminSettings()

// 自动实名供应商字段（按选择显隐）
const idFields: Record<string, { key: string; label: string }[]> = {
  baidu_face: [
    { key: 'verification_baidu_api_key', label: '百度 API Key' },
    { key: 'verification_baidu_plan_id', label: '百度方案 ID' },
  ],
  leaf_face: [
    { key: 'verification_leaf_app_id', label: '叶子 App ID' },
    { key: 'verification_leaf_api_base', label: '接口地址' },
  ],
  smapi: [
    { key: 'verification_smapi_app_key', label: 'App Key' },
    { key: 'verification_smapi_api_url', label: '接口地址' },
    { key: 'verification_smapi_product_code', label: '产品编码' },
  ],
  stay33: [
    { key: 'verification_stay33_api_key', label: 'API Key' },
    { key: 'verification_stay33_api_url', label: '接口地址' },
    { key: 'verification_stay33_biz_code', label: '业务编码' },
  ],
}
const idSecrets: Record<string, { key: string; label: string }> = {
  baidu_face: { key: 'verification_baidu_secret_key', label: '百度 Secret Key' },
  leaf_face: { key: 'verification_leaf_app_secret', label: '叶子 App Secret' },
  smapi: { key: 'verification_smapi_secret_key', label: 'Secret Key' },
  stay33: { key: 'verification_stay33_secret_key', label: 'Secret Key' },
}
const idProvider = computed(() => cfg['verification_provider'] || '')

function saveManual() {
  save('manual_identity', flags(['manual_identity_enabled', 'manual_identity_requires_verified_phone']))
}
function saveAutomatic() {
  save('automatic_identity', {
    verification_provider: cfg['verification_provider'] || '',
    verification_baidu_api_key: cfg['verification_baidu_api_key'] || '',
    verification_baidu_plan_id: cfg['verification_baidu_plan_id'] || '',
    verification_baidu_secret_key: cfg['verification_baidu_secret_key'] || '',
    verification_leaf_app_id: cfg['verification_leaf_app_id'] || '',
    verification_leaf_api_base: cfg['verification_leaf_api_base'] || '',
    verification_leaf_app_secret: cfg['verification_leaf_app_secret'] || '',
    verification_smapi_app_key: cfg['verification_smapi_app_key'] || '',
    verification_smapi_api_url: cfg['verification_smapi_api_url'] || '',
    verification_smapi_product_code: cfg['verification_smapi_product_code'] || '',
    verification_smapi_secret_key: cfg['verification_smapi_secret_key'] || '',
    verification_stay33_api_key: cfg['verification_stay33_api_key'] || '',
    verification_stay33_api_url: cfg['verification_stay33_api_url'] || '',
    verification_stay33_biz_code: cfg['verification_stay33_biz_code'] || '',
    verification_stay33_secret_key: cfg['verification_stay33_secret_key'] || '',
  })
}

load()
</script>

<template>
  <div class="art-full-height" v-loading="loading">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>实名认证</h4>
            <p>人工审核开关与第三方自动实名供应商配置。</p>
          </div>
        </div>
      </template>

      <el-divider content-position="left">人工审核</el-divider>
      <div class="admin-check-grid">
        <el-checkbox v-model="cfg['manual_identity_enabled']" true-value="1" false-value="0">启用人工实名</el-checkbox>
        <el-checkbox v-model="cfg['manual_identity_requires_verified_phone']" true-value="1" false-value="0">要求已验证手机号</el-checkbox>
      </div>
      <div class="admin-section__actions">
        <el-button type="primary" :loading="saving === 'manual_identity'" @click="saveManual">保存人工实名</el-button>
      </div>

      <el-divider content-position="left">自动实名（第三方）</el-divider>
      <div class="admin-form-grid">
        <el-form-item label="服务商">
          <el-select v-model="cfg['verification_provider']" clearable class="w-full">
            <el-option label="百度人脸" value="baidu_face" />
            <el-option label="叶子人脸" value="leaf_face" />
            <el-option label="SMAPI" value="smapi" />
            <el-option label="Stay33" value="stay33" />
          </el-select>
        </el-form-item>
      </div>
      <div v-if="idProvider" class="admin-form-grid">
        <el-form-item v-for="f in idFields[idProvider] || []" :key="f.key" :label="f.label">
          <el-input v-model="cfg[f.key]" />
        </el-form-item>
        <el-form-item v-if="idSecrets[idProvider]" :label="idSecrets[idProvider].label + '（留空保持不变）'">
          <el-input v-model="cfg[idSecrets[idProvider].key]" type="password" show-password />
        </el-form-item>
      </div>
      <div class="admin-section__actions">
        <el-button type="primary" :loading="saving === 'automatic_identity'" @click="saveAutomatic">保存自动实名</el-button>
      </div>
    </ElCard>
  </div>
</template>
