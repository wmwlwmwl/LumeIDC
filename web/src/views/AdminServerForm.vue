<script setup lang="ts">
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { fetchAdminServerForm, saveAdminServer, type AdminServerFormData, type CredentialField } from '../admin/api'

const route = useRoute()
const router = useRouter()

const id = computed(() => {
  const v = route.params.id
  return v ? Number(v) : undefined
})

const loading = ref(false)
const saving = ref(false)
const data = ref<AdminServerFormData | null>(null)

const form = reactive<Record<string, string>>({
  name: '',
  provider: 'zjmf',
  disabled: '0',
  profit_type: '0',
  profit_value: '0',
  api_url: '',
  api_username: '',
  api_key: '',
  // 新建时默认与库中默认值一致：启用、10 分钟。
  retry_later_enabled: '1',
  retry_later_minutes: '10',
})

// 当前供应商的凭据字段（由后端 provider 声明，前端只负责渲染）
const credFields = computed<CredentialField[]>(() => {
  const all = data.value?.credential_fields || {}
  return all[form.provider] || []
})

async function load() {
  loading.value = true
  try {
    const d = await fetchAdminServerForm(id.value)
    data.value = d
    if (d.server) {
      form.name = d.server.name || ''
      form.provider = d.server.provider || 'zjmf'
      form.disabled = d.server.disabled ? '1' : '0'
      form.profit_type = String(d.server.profit_type ?? 0)
      form.profit_value = String(d.server.profit_value ?? 0)
      form.retry_later_enabled = d.server.retry_later_enabled === false ? '0' : '1'
      form.retry_later_minutes = String(d.server.retry_later_minutes ?? 10)
      form.api_url = d.server.api_url || ''
      form.api_username = d.server.api_username || ''
    }
    // 凭据值按字段名回填（api_url / api_username / api_key …）
    for (const [k, v] of Object.entries(d.values || {})) form[k] = v
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '读取服务器失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)
watch(id, load)

async function save() {
  if (!form.name.trim()) {
    ElMessage.warning('请填写服务器名称')
    return
  }
  if (!form.api_url.trim()) {
    ElMessage.warning('请填写 API 地址')
    return
  }
  for (const f of credFields.value) {
    if (f.required && !String(form[f.name] || '').trim()) {
      ElMessage.warning(`请填写${f.label}`)
      return
    }
  }
  // 与服务端同一口径：间隔过小会让上游被高频空转，过大则充值后迟迟不通。
  if (form.retry_later_enabled === '1') {
    const minutes = Number(form.retry_later_minutes)
    if (!Number.isInteger(minutes) || minutes < 1 || minutes > 1440) {
      ElMessage.warning('重试间隔需在 1~1440 分钟之间')
      return
    }
  }
  saving.value = true
  try {
    await saveAdminServer(id.value, { ...form })
    ElMessage.success('已保存')
    router.push('/servers')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="art-full-height" v-loading="loading">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>{{ id ? '编辑上游服务器' : '添加上游服务器' }}</h4>
            <p>配置供应商连接、状态与默认利润。</p>
          </div>
        </div>
      </template>

      <el-form label-position="top">
        <el-divider content-position="left">连接配置</el-divider>
        <el-form-item label="服务器名称" required>
          <el-input v-model="form.name" placeholder="如 ZJMF 主节点" />
        </el-form-item>
        <el-form-item label="上游类型">
          <el-select v-model="form.provider" class="w-full">
            <el-option v-for="p in data?.providers || []" :key="p.code" :value="p.code" :label="p.name" />
          </el-select>
        </el-form-item>

        <!-- 供应商凭据字段：按 provider 声明动态渲染 -->
        <template v-if="credFields.length">
          <el-form-item v-for="f in credFields" :key="f.name" :label="f.label" :required="f.required">
            <el-input
              v-model="form[f.name]"
              :type="f.secret ? 'password' : 'text'"
              :show-password="f.secret"
              :placeholder="f.placeholder || ''"
              :autocomplete="f.secret ? 'new-password' : 'off'"
            />
          </el-form-item>
        </template>

        <el-divider content-position="left">运行状态</el-divider>
        <el-form-item label="运行状态">
          <el-select v-model="form.disabled" style="max-width: 200px">
            <el-option label="启用" value="0" />
            <el-option label="停用" value="1" />
          </el-select>
        </el-form-item>

        <el-divider content-position="left">默认利润</el-divider>
        <p class="mb-3 mt-0 text-xs text-g-500">该上游下产品未单独设利润时，使用此默认值。</p>
        <div class="grid gap-4 sm:grid-cols-2">
          <el-form-item label="利润方式">
            <el-select v-model="form.profit_type" class="w-full">
              <el-option label="百分比（%）" value="0" />
              <el-option label="固定金额（元）" value="1" />
            </el-select>
          </el-form-item>
          <el-form-item label="利润值">
            <el-input v-model="form.profit_value" type="number" placeholder="如 30 或 5" />
          </el-form-item>
        </div>

        <el-divider content-position="left">重试策略</el-divider>
        <p class="mb-3 mt-0 text-xs text-g-500">
          上游余额不足这类"等外部条件"的失败：开启后按下方间隔自动重试（不消耗重试次数），充值到账后自动完成；关闭则立即转人工复核。
        </p>
        <div class="grid gap-4 sm:grid-cols-2">
          <el-form-item label="自动重试">
            <el-select v-model="form.retry_later_enabled" class="w-full" style="max-width: 200px">
              <el-option label="开启" value="1" />
              <el-option label="关闭（转人工）" value="0" />
            </el-select>
          </el-form-item>
          <el-form-item label="重试间隔（分钟）">
            <el-input
              v-model="form.retry_later_minutes"
              type="number"
              min="1"
              max="1440"
              :disabled="form.retry_later_enabled === '0'"
              placeholder="1 ~ 1440"
            />
          </el-form-item>
        </div>

        <div class="flex gap-2">
          <el-button type="primary" :loading="saving" @click="save">保存服务器</el-button>
          <el-button @click="router.push('/servers')">取消</el-button>
        </div>
      </el-form>
    </ElCard>
  </div>
</template>


