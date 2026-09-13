<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { fetchUpdateState, checkUpdate, applyUpdate, restartUpdate, type UpdateInfo } from '../admin/api'

const loading = ref(true)
const currentVersion = ref('')
const pendingRestart = ref(false)
const disabled = ref(false)

const checking = ref(false)
const applying = ref(false)
const restarting = ref(false)
const info = ref<UpdateInfo | null>(null)

async function load() {
  loading.value = true
  try {
    const s = await fetchUpdateState()
    currentVersion.value = s.current_version || ''
    pendingRestart.value = !!s.pending_restart
    disabled.value = !!s.disabled
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '读取更新状态失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function doCheck() {
  checking.value = true
  try {
    info.value = await checkUpdate()
    if (info.value.available) ElMessage.success(`发现新版本 ${info.value.version}`)
    else ElMessage.info(info.value.hint || '当前已是最新版本')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '检查更新失败')
  } finally {
    checking.value = false
  }
}

async function doApply() {
  const ok = await ElMessageBox.confirm(
    '将下载并替换当前二进制文件（替换后需重启生效）。确认继续？',
    '应用更新',
    { type: 'warning', confirmButtonText: '下载并替换' },
  ).catch(() => null)
  if (!ok) return
  applying.value = true
  try {
    const msg = await applyUpdate()
    ElMessage.success(msg)
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '更新失败')
  } finally {
    applying.value = false
  }
}

async function doRestart() {
  const ok = await ElMessageBox.confirm(
    '将就地重启应用以生效新版本；重启期间后台短暂不可用，且需要重新登录。确认重启？',
    '重启应用',
    { type: 'warning', confirmButtonText: '立即重启' },
  ).catch(() => null)
  if (!ok) return
  restarting.value = true
  try {
    await restartUpdate() // 进程随即被替换，响应可能不完整
  } catch {
    /* 重启导致的连接中断属预期 */
  }
  ElMessage.info('正在重启，请稍后重新打开后台')
  // 轮询等待服务恢复
  let tries = 0
  const timer = window.setInterval(async () => {
    tries += 1
    try {
      await fetchUpdateState()
      window.clearInterval(timer)
      location.reload()
    } catch {
      if (tries >= 30) {
        window.clearInterval(timer)
        ElMessage.warning('暂未检测到服务恢复，请稍后手动刷新')
      }
    }
  }, 2000)
}
</script>

<template>
  <div class="art-full-height" v-loading="loading">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>系统更新</h4>
            <p>检查并应用新版本（从 GitHub Releases 拉取）。</p>
          </div>
        </div>
      </template>

      <el-alert
        v-if="disabled"
        type="info"
        :closable="false"
        show-icon
        title="当前系统不支持在线更新"
        description="在线更新仅在 Linux 服务器上可用；其它平台请手动替换二进制文件。"
        class="mb-4"
      />

      <div class="flex flex-wrap items-center justify-between gap-4">
        <div>
          <div class="text-xs text-g-500">当前版本</div>
          <div class="mt-1 text-xl font-bold text-g-900">{{ currentVersion || '-' }}</div>
        </div>
        <el-button type="primary" :loading="checking" :disabled="disabled" @click="doCheck">检查更新</el-button>
      </div>
      <p v-if="pendingRestart" class="mt-3 rounded-lg bg-warning/10 px-3 py-2.5 text-xs leading-relaxed text-warning">
        已替换二进制文件，<strong>重启后新版本才会生效</strong>。
      </p>

      <template v-if="info">
        <el-divider content-position="left">更新结果</el-divider>
        <div class="flex flex-wrap items-center gap-2.5">
          <strong class="text-sm text-g-800">
            {{ info.available ? `可更新到 ${info.version}` : '已是最新版本' }}
          </strong>
          <el-tag v-if="info.published_at" size="small" type="info" effect="light">发布于 {{ info.published_at }}</el-tag>
        </div>
        <p v-if="info.hint && !info.available" class="m-0 mt-2 text-xs text-g-500">{{ info.hint }}</p>
        <pre v-if="info.notes" class="admin-update-notes">{{ info.notes }}</pre>

        <div class="mt-4 flex flex-wrap gap-2.5">
          <el-button type="primary" :loading="applying" :disabled="!info.available || disabled" @click="doApply">下载并替换</el-button>
          <el-button type="danger" plain :loading="restarting" :disabled="!pendingRestart || disabled" @click="doRestart">重启生效</el-button>
        </div>
        <p class="mt-3 text-xs text-g-400">替换二进制不会自动重启；重启会短暂中断服务并需要重新登录后台。</p>
      </template>

      <div v-else-if="pendingRestart" class="mt-4">
        <el-button type="danger" plain :loading="restarting" :disabled="disabled" @click="doRestart">重启生效</el-button>
      </div>
    </ElCard>
  </div>
</template>

<style scoped>
.admin-update-notes {
  margin: 12px 0 0;
  max-height: 260px;
  padding: 12px;
  overflow: auto;
  color: var(--art-gray-600);
  font-family: inherit;
  font-size: 12px;
  line-height: 1.7;
  white-space: pre-wrap;
  background: var(--art-gray-100);
  border-radius: 9px;
}
</style>
