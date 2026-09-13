<script setup lang="ts">
/**
 * ZJMF 实例信息卡：只读运行信息表（IP/端口/系统/带宽等）+ 刷新按钮。
 */
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { CopyDocument, InfoFilled } from '@element-plus/icons-vue'
import { refreshServiceHost, type DetailData } from '../../../api/user'

const props = defineProps<{ data: DetailData; serviceId: number }>()
const emit = defineEmits<{ refresh: [] }>()

function copy(value: string) {
  void navigator.clipboard
    ?.writeText(value)
    .then(() => ElMessage.success('已复制'))
    .catch(() => ElMessage.error('复制失败，请手动复制'))
}

const refreshingHost = ref(false)
async function refreshHost() {
  refreshingHost.value = true
  try {
    const res = await refreshServiceHost(props.serviceId)
    if (String(res.ok) === '1') { ElMessage.success('实例信息已刷新'); emit('refresh') }
    else ElMessage.error(res.msg || '刷新失败')
  } catch (e: unknown) { ElMessage.error((e as Error).message || '刷新失败') }
  finally { refreshingHost.value = false }
}
</script>

<template>
  <section class="zjmf-card">
    <div class="zjmf-card__header">
      <div class="zjmf-card__title">
        <h2>实例信息</h2>
        <p>实例运行信息</p>
      </div>
      <el-button size="small" text type="primary" :loading="refreshingHost" @click="refreshHost">刷新信息</el-button>
    </div>

    <div v-if="data.host" class="zjmf-info-table">
      <div class="zjmf-info-row">
        <span class="zjmf-info-label">状态</span>
        <b class="zjmf-info-value">{{ data.host.status || data.svc.status_text }}</b>
      </div>
      <div v-if="data.host.username" class="zjmf-info-row">
        <span class="zjmf-info-label">用户名</span>
        <b class="zjmf-info-value zjmf-mono">{{ data.host.username }}</b>
      </div>
      <div v-if="data.host.password" class="zjmf-info-row">
        <span class="zjmf-info-label">密码</span>
        <b class="zjmf-info-value zjmf-mono">{{ data.host.password }}</b>
        <el-button class="zjmf-copy-btn" size="small" text @click="copy(data.host.password)">
          <el-icon :size="13"><CopyDocument /></el-icon>
          复制
        </el-button>
      </div>
      <div v-if="data.host.ip" class="zjmf-info-row">
        <span class="zjmf-info-label">实例 IP</span>
        <b class="zjmf-info-value zjmf-mono">{{ data.host.ip }}</b>
      </div>
      <div v-for="ip in data.host.additional_ips || []" :key="ip" class="zjmf-info-row">
        <span class="zjmf-info-label">附加 IP</span>
        <b class="zjmf-info-value zjmf-mono">{{ ip }}</b>
      </div>
      <div v-if="data.host.port" class="zjmf-info-row">
        <span class="zjmf-info-label">端口</span>
        <b class="zjmf-info-value zjmf-mono">{{ data.host.port }}</b>
      </div>
      <div v-if="data.host.os" class="zjmf-info-row">
        <span class="zjmf-info-label">系统名称</span>
        <b class="zjmf-info-value">{{ data.host.os }}</b>
      </div>
      <div v-if="data.host.os_version" class="zjmf-info-row">
        <span class="zjmf-info-label">系统版本</span>
        <b class="zjmf-info-value">{{ data.host.os_version }}</b>
      </div>
      <div v-if="data.host.bw_limit" class="zjmf-info-row">
        <span class="zjmf-info-label">带宽</span>
        <b class="zjmf-info-value">
          {{ data.host.bw_limit }}{{ data.host.bw_usage ? `（已用 ${data.host.bw_usage}）` : '' }}
        </b>
      </div>
      <div v-if="data.host.datacenter" class="zjmf-info-row">
        <span class="zjmf-info-label">数据中心</span>
        <b class="zjmf-info-value">{{ data.host.datacenter }}</b>
      </div>
    </div>
    <div v-else class="zjmf-empty-hint">
      <el-icon size="16"><InfoFilled /></el-icon>
      <span>尚未获取实例信息，点击「刷新信息」重新获取。</span>
    </div>
  </section>
</template>

<style scoped>
.zjmf-card {
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: var(--custom-radius);
  padding: 20px 22px;
  box-shadow: 0 1px 3px rgba(34, 48, 83, 0.04), 0 1px 2px rgba(34, 48, 83, 0.02);
}
.zjmf-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--art-card-border);
}
.zjmf-card__title h2 {
  margin: 4px 0 0;
  color: var(--art-gray-900);
  font-size: 15px;
  font-weight: 650;
  line-height: 1.3;
}
.zjmf-card__title p {
  margin: 4px 0 0;
  color: var(--art-gray-400);
  font-size: 11.5px;
}

/* --- 空实例提示 --- */
.zjmf-empty-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 14px 0 4px;
  padding: 12px 14px;
  color: var(--art-gray-500);
  font-size: 12px;
  background: var(--art-gray-50);
  border: 1px solid var(--art-card-border);
  border-radius: 8px;
}

/* --- 实例信息表格 --- */
.zjmf-info-table {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 28px;
}
.zjmf-info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 10px 0;
  font-size: 12.5px;
  border-bottom: 1px dashed var(--art-card-border);
}
.zjmf-info-row:last-child {
  border-bottom: none;
}
.zjmf-info-label {
  flex: 0 0 auto;
  color: var(--art-gray-500);
  font-weight: 450;
}
.zjmf-info-value {
  max-width: 65%;
  color: var(--art-gray-800);
  font-weight: 600;
  text-align: right;
  overflow-wrap: anywhere;
  word-break: break-all;
}
.zjmf-mono {
  font-family: ui-monospace, SFMono-Regular, Consolas, 'Liberation Mono', monospace;
  font-size: 12px;
}
.zjmf-copy-btn {
  flex: 0 0 auto;
  margin-left: 4px;
  padding: 2px 6px;
  color: var(--theme-color-deep);
  font-size: 11px;
}
.zjmf-copy-btn:hover {
  color: var(--theme-color);
}

@media (max-width: 560px) {
  .zjmf-info-table {
    grid-template-columns: 1fr;
  }
}
</style>
