<script setup lang="ts">
import { ref, computed, useSlots } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { InfoFilled } from '@element-plus/icons-vue'
import { consoleAction, refreshServiceHost, type DetailData } from '../../api/user'

const props = defineProps<{ data: DetailData }>()
const slots = useSlots()
const emit = defineEmits<{ refresh: [] }>()

const busy = ref(false)
const refreshingHost = ref(false)
const activeView = ref('overview')

const password = computed(() => props.data.host?.password || '')
// 上游站点状态为「运行中 / 已关闭」两态（见 easypanel.HostOverview）
const hostOnline = computed(() => props.data.host?.status === '运行中')

async function refreshHost() {
  refreshingHost.value = true
  try {
    const res = await refreshServiceHost(props.data.svc.id)
    if (String(res.ok) === '1') {
      ElMessage.success('实例信息已刷新')
      emit('refresh')
    } else {
      ElMessage.error(res.msg || '刷新失败')
    }
  } catch (e: unknown) {
    ElMessage.error((e as Error).message || '刷新失败')
  } finally {
    refreshingHost.value = false
  }
}

function copy() {
  void navigator.clipboard
    ?.writeText(password.value)
    .then(() => ElMessage.success('已复制'))
    .catch(() => ElMessage.error('复制失败，请手动复制'))
}

async function resetPassword() {
  const value = await ElMessageBox.prompt('留空自动生成', '重置站点密码', {
    confirmButtonText: '重置',
    cancelButtonText: '取消',
    inputValue: '',
  }).catch(() => null)
  if (value === null) return
  busy.value = true
  try {
    const res = await consoleAction(props.data.svc.id, { do: 'crack_pass', password: value.value })
    if (String(res.ok) === '1') ElMessage.success(res.msg || '密码已重置')
    else ElMessage.error(res.msg || '重置失败')
  } catch (e: unknown) {
    ElMessage.error((e as Error).message || '重置失败')
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <div class="easypanel-shell art-card">
    <nav class="easypanel-tabs" aria-label="实例功能">
      <button type="button" :class="{ 'is-active': activeView === 'overview' }" @click="activeView = 'overview'">概要</button>
      <button v-if="slots.invoices" type="button" :class="{ 'is-active': activeView === 'invoices' }" @click="activeView = 'invoices'">账单记录</button>
    </nav>

    <div v-if="activeView === 'overview'" class="easypanel-body">
      <section v-if="!data.host" class="easypanel-card art-card">
        <div class="easypanel-card__header">
          <div class="easypanel-card__title">
            <h2>站点信息</h2>
            <p>站点账号与资源配置</p>
          </div>
          <el-button size="small" text type="primary" :loading="refreshingHost" @click="refreshHost">刷新信息</el-button>
        </div>
        <div class="easypanel-empty-hint">
          <el-icon size="16"><InfoFilled /></el-icon>
          <span>尚未获取站点信息，点击「刷新信息」重新获取。</span>
        </div>
      </section>

      <div v-else class="easypanel-grid">
        <section class="easypanel-card art-card">
          <div class="easypanel-card__header">
            <div class="easypanel-card__title">
              <h2>账户信息</h2>
              <p>站点登录凭据</p>
            </div>
            <el-tag v-if="data.host.status" size="small" :type="hostOnline ? 'success' : 'info'">{{ data.host.status }}</el-tag>
          </div>
          <div class="easypanel-info-list">
            <div>
              <span>站点账号</span>
              <b>{{ data.host.username || '-' }}</b>
            </div>
            <div>
              <span>站点密码</span>
              <b>{{ password || '-' }}</b>
              <el-button v-if="password" size="small" text @click="copy">复制</el-button>
            </div>
            <div v-if="data.host.panel_url">
              <span>面板地址</span>
              <b>
                <a class="easypanel-panel-link" :href="data.host.panel_url" target="_blank" rel="noopener">{{ data.host.panel_url }}</a>
              </b>
            </div>
          </div>
          <div class="easypanel-card__footer">
            <span class="easypanel-footer-label">站点面板</span>
            <div class="easypanel-footer-actions">
              <el-button size="small" plain :loading="busy" @click="resetPassword">重置站点密码</el-button>
              <form v-if="data.host.panel_url" :action="data.host.panel_url" method="post" target="_blank" rel="noopener">
                <input type="hidden" name="username" :value="data.host.username || ''">
                <input type="hidden" name="passwd" :value="password">
                <el-button native-type="submit" type="primary" size="small">登录主机面板</el-button>
              </form>
            </div>
          </div>
        </section>

        <section class="easypanel-card art-card">
          <div class="easypanel-card__header">
            <div class="easypanel-card__title">
              <h2>空间与数据库</h2>
              <p>站点资源配置</p>
            </div>
            <el-button size="small" text type="primary" :loading="refreshingHost" @click="refreshHost">刷新信息</el-button>
          </div>
          <div class="easypanel-info-list">
            <div v-if="data.host.os">
              <span>运行环境</span>
              <b>{{ (data.host.os || '').toUpperCase() }}</b>
            </div>
            <div>
              <span>网页空间</span>
              <b>{{ data.host.web_quota || '-' }}</b>
            </div>
            <div v-if="data.host.db_name">
              <span>数据库</span>
              <b>{{ data.host.db_name }}{{ data.host.db_quota ? `（${data.host.db_quota}）` : '' }} · 已用 {{ data.host.db_used || '-' }}</b>
            </div>
            <div>
              <span>FTP</span>
              <b>{{ data.host.ftp ? '已开启' : '未开启' }}</b>
            </div>
            <div v-if="data.host.domain">
              <span>可绑域名</span>
              <b>{{ data.host.domain }}</b>
            </div>
            <div v-if="data.host.flow_limit">
              <span>流量限制</span>
              <b>{{ data.host.flow_limit }}</b>
            </div>
            <div v-if="data.host.speed_limit">
              <span>速度限制</span>
              <b>{{ data.host.speed_limit }}</b>
            </div>
            <div v-if="data.host.create_time">
              <span>创建时间</span>
              <b>{{ data.host.create_time }}</b>
            </div>
          </div>
        </section>
      </div>
    </div>
    <section v-if="activeView === 'invoices'" class="easypanel-invoices">
      <slot name="invoices" />
    </section>
  </div>
</template>
<style scoped>
.easypanel-shell {
  margin-top: 16px;
  overflow: hidden;
}
.easypanel-tabs {
  display: flex;
  align-items: center;
  gap: 4px;
  min-height: 51px;
  padding: 8px 12px;
  background: var(--art-gray-50);
  border-bottom: 1px solid var(--art-card-border);
  overflow-x: auto;
}
.easypanel-tabs button {
  flex: 0 0 auto;
  height: 34px;
  padding: 0 16px;
  color: var(--art-gray-500);
  font-size: 12px;
  font-weight: 500;
  background: transparent;
  border: 0;
  border-radius: 7px;
  cursor: pointer;
  transition: color 0.2s ease, background-color 0.2s ease, border-color 0.2s ease;
}
.easypanel-tabs button:hover {
  color: var(--theme-color-deep);
  background: var(--art-hover-color);
}
.easypanel-tabs button.is-active {
  color: var(--theme-color-deep);
  font-weight: 600;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
}
.easypanel-body {
  padding: 16px;
}
/* 账单视图：插槽内容在 ServiceDetail.vue 挂 art-card 成浮卡，与内容区一致留白 */
.easypanel-invoices {
  margin: 16px;
}
.easypanel-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.1fr);
  gap: 16px;
  align-items: stretch;
}
/* 盒样式（背景/描边/圆角/阴影）交由全局 art-card 按 data-box-mode 接管 */
.easypanel-card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 20px 22px;
}
.easypanel-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--art-card-border);
}
.easypanel-card__title h2 {
  margin: 4px 0 0;
  color: var(--art-gray-900);
  font-size: 15px;
  font-weight: 650;
  line-height: 1.3;
}
.easypanel-card__title p {
  margin: 4px 0 0;
  color: var(--art-gray-400);
  font-size: 11.5px;
}
.easypanel-info-list > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 10px 0;
  font-size: 12.5px;
  border-bottom: 1px dashed var(--art-card-border);
}
.easypanel-info-list > div:last-child {
  border-bottom: 0;
}
.easypanel-info-list span {
  flex: 0 0 auto;
  color: var(--art-gray-500);
}
.easypanel-info-list b {
  max-width: 65%;
  color: var(--art-gray-800);
  font-weight: 600;
  text-align: right;
  overflow-wrap: anywhere;
  word-break: break-all;
}
.easypanel-card__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: auto;
  padding-top: 14px;
  border-top: 1px solid var(--art-card-border);
}
.easypanel-footer-label {
  color: var(--art-gray-400);
  font-size: 12px;
}
.easypanel-footer-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.easypanel-footer-actions form {
  margin: 0;
}
.easypanel-empty-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 14px 0 0;
  padding: 12px 14px;
  color: var(--art-gray-500);
  font-size: 12px;
  background: var(--art-gray-50);
  border: 1px solid var(--art-card-border);
  border-radius: 8px;
}
.easypanel-panel-link {
  color: var(--theme-color-deep);
  text-decoration: none;
}
.easypanel-panel-link:hover {
  text-decoration: underline;
}
@media (max-width: 820px) {
  .easypanel-grid {
    grid-template-columns: 1fr;
  }
}
</style>
