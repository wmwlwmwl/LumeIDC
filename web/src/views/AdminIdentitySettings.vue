<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { http, type ApiResult } from '../http/index'
import { useAdminSettings } from '../admin/useSettings'

const { loading, saving, cfg, load, save, flags } = useAdminSettings()

// 自动实名服务商由后端注册表驱动（GET /admin/verification-providers）：新增服务商零前端改动。
interface VerificationField { key: string; label: string; secret?: boolean }
interface VerificationProvider { key: string; name: string; fields: VerificationField[] }

const providers = ref<VerificationProvider[]>([])
const idProvider = computed(() => cfg['verification_provider'] || '')
const providerFields = computed(() => providers.value.find(p => p.key === idProvider.value)?.fields || [])

async function loadProviders() {
  try {
    const res = await http.get<ApiResult & { list: VerificationProvider[] }>('/verification-providers')
    if (res.ok) providers.value = res.list || []
  } catch {
    // 列表失败仅影响切换服务商，既有配置仍可展示与保存
  }
}

function saveManual() {
  save('manual_identity', flags(['manual_identity_enabled', 'manual_identity_requires_verified_phone']))
}
function saveAutomatic() {
  // 已选服务商但字段清单未拉到时拒绝保存：避免空字段覆盖已存配置
  if (idProvider.value && providerFields.value.length === 0) {
    ElMessage.warning('服务商清单未加载完成，请稍后重试')
    return
  }
  const body: Record<string, string> = { verification_provider: cfg['verification_provider'] || '' }
  for (const f of providerFields.value) body[f.key] = cfg[f.key] || ''
  save('automatic_identity', body)
}

load()
loadProviders()
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
            <el-option v-for="p in providers" :key="p.key" :label="p.name" :value="p.key" />
          </el-select>
        </el-form-item>
      </div>
      <div v-if="idProvider" class="admin-form-grid">
        <el-form-item v-for="f in providerFields" :key="f.key" :label="f.secret ? f.label + '（留空保持不变）' : f.label">
          <el-input v-if="f.secret" v-model="cfg[f.key]" type="password" show-password />
          <el-input v-else v-model="cfg[f.key]" />
        </el-form-item>
      </div>
      <div class="admin-section__actions">
        <el-button type="primary" :loading="saving === 'automatic_identity'" @click="saveAutomatic">保存自动实名</el-button>
      </div>
    </ElCard>
  </div>
</template>
