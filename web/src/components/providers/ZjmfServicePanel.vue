<script setup lang="ts">
import { computed, onMounted, ref, useSlots, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowRight, InfoFilled } from '@element-plus/icons-vue'
import ServiceBlocks from '../ServiceBlocks.vue'
import ZjmfMetrics from './zjmf/ZjmfMetrics.vue'
import ZjmfInstanceInfo from './zjmf/ZjmfInstanceInfo.vue'
import ZjmfOpsDialogs from './zjmf/ZjmfOpsDialogs.vue'
import { consoleAction, fetchPower, fetchUsage, fetchTraffic, exitRescueService, type DetailData, type PowerInfo, type UsageInfo, type TrafficDay } from '../../api/user'

const props = defineProps<{ data: DetailData }>()
const slots = useSlots()
const emit = defineEmits<{ refresh: [] }>()
const serviceId = computed(() => props.data.svc.id)
// 后台代管视图（路由 meta.admin）：控制台页挂在后台 SPA 的 hash 路由下，
// 链接改为当前页的 hash 路径，避免跳去前台登录页。
const route = useRoute()
const consoleHref = computed(() =>
  route.meta.admin ? `#/services/${serviceId.value}/console` : `/services/${serviceId.value}/console`,
)
const hasInstance = computed(() => props.data.svc.status === 1 || props.data.svc.status === 2)
const powering = ref(false)
const rescueOn = ref(false)
const powerState = ref<PowerInfo | null>(null)
const usage = ref<UsageInfo | null>(null)
const trafficDays = ref<TrafficDay[]>([])
const activeView = ref('overview')

const opsDialogs = ref<InstanceType<typeof ZjmfOpsDialogs> | null>(null)

// 面板整体数据的请求代次：服务切换后丢弃旧实例的响应，防止旧数据覆盖新面板
let loadRequestId = 0

async function load() {
  const requestId = ++loadRequestId
  usage.value = null
  trafficDays.value = []
  powerState.value = null
  if (!hasInstance.value) return
  const id = serviceId.value
  const [u, traffic, power] = await Promise.allSettled([
    fetchUsage(id),
    fetchTraffic(id),
    fetchPower(id),
  ])
  if (requestId !== loadRequestId) return
  usage.value = u.status === 'fulfilled' ? u.value : null
  trafficDays.value = traffic.status === 'fulfilled' ? traffic.value : []
  powerState.value = power.status === 'fulfilled' ? power.value : null
}

onMounted(load)
watch(() => serviceId.value, load)

async function power(do_: 'on' | 'off' | 'reboot' | 'hard_off' | 'hard_reboot') {
  const labels = {
    on: '开机',
    off: '关机',
    reboot: '重启',
    hard_off: '强制关机',
    hard_reboot: '强制重启',
  }
  if (
    !(await ElMessageBox.confirm(`确认执行「${labels[do_]}」？`, '控制台操作', {
      confirmButtonText: '执行',
    }).catch(() => null))
  )
    return
  powering.value = true
  try {
    const res = await consoleAction(serviceId.value, { do: do_ })
    String(res.ok) === '1' ? ElMessage.success(res.msg || '操作成功') : ElMessage.error(res.msg || '操作失败')
    // 电源操作后后台刷新实时状态，状态标签不再停留在旧值
    if (String(res.ok) === '1') void fetchPower(serviceId.value).then((s) => { powerState.value = s }).catch(() => {})
  } catch (e: unknown) {
    ElMessage.error((e as Error).message || '操作失败')
  } finally {
    powering.value = false
  }
}

async function doExitRescue() {
  try {
    const res = await exitRescueService(serviceId.value)
    if (String(res.ok) === '1') {
      ElMessage.success(res.msg || '已退出救援模式')
      rescueOn.value = false
      await load()
    } else {
      ElMessage.error(res.msg || '退出失败')
    }
  } catch (e: unknown) {
    ElMessage.error((e as Error).message || '退出失败')
  }
}

const powerBtns = [
  { k: 'on' as const, l: '开机', type: 'success' as const, plain: false },
  { k: 'off' as const, l: '关机', type: 'danger' as const, plain: false },
  { k: 'reboot' as const, l: '重启', type: 'warning' as const, plain: false },
  { k: 'hard_off' as const, l: '强制关机', type: 'danger' as const, plain: true },
  { k: 'hard_reboot' as const, l: '强制重启', type: 'warning' as const, plain: true },
]

const moduleTabs = computed(() => [
  { key: 'nat_acl', label: 'NAT 转发' },
  { key: 'nat_web', label: '共享建站' },
  { key: 'security_groups', label: '安全组' },
  { key: 'setting', label: '实例设置' },
  { key: 'snapshot', label: '快照/备份' },
].filter((tab) => props.data.modules?.includes(tab.key)))

const tabs = computed(() => [
  { key: 'overview', label: '概要' },
  ...moduleTabs.value,
  ...(slots.invoices ? [{ key: 'invoices', label: '账单记录' }] : []),
])
</script>

<template>
  <div class="zjmf-panel">
    <div class="zjmf-shell">
      <nav class="zjmf-tabs" aria-label="实例功能">
      <button
        v-for="tab in tabs"
        :key="tab.key"
        type="button"
        class="zjmf-tab"
        :class="{ 'is-active': activeView === tab.key }"
        @click="activeView = tab.key"
      >
         {{ tab.label }}
       </button>
      </nav>

      <div v-if="activeView === 'overview'" class="zjmf-primary-grid">
      <!-- ========== 实例控制台 ========== -->
      <section class="zjmf-card zjmf-card--console">
        <div class="zjmf-card__header">
          <div class="zjmf-card__title">
            <h2>实例控制台</h2>
            <p>开关机、系统和远程维护</p>
          </div>
          <el-tag
            v-if="powerState"
            class="zjmf-status-tag"
            size="small"
            :type="powerState.status === 'on' ? 'success' : powerState.status === 'fault' ? 'danger' : 'info'"
          >
            <span class="zjmf-status-dot" :class="`is-${powerState.status}`"></span>
            {{
              powerState.desc ||
              ({ on: '运行中', off: '已关机', operating: '操作中', fault: '异常' } as Record<string, string>)[
                powerState.status
              ] ||
              '状态未知'
            }}
          </el-tag>
        </div>

        <div v-if="!hasInstance" class="zjmf-empty-hint">
          <el-icon size="16"><InfoFilled /></el-icon>
          <span>服务正在开通中，开通完成后可使用实例控制功能。</span>
        </div>

        <template v-else>
          <div class="zjmf-op-section">
            <div class="zjmf-op-label">电源控制</div>
            <div class="zjmf-op-btns">
              <el-button
                v-for="btn in powerBtns"
                :key="btn.k"
                :type="btn.type"
                :plain="btn.plain"
                size="default"
                :loading="powering"
                @click="power(btn.k)"
              >
                {{ btn.l }}
              </el-button>
            </div>
          </div>

          <div class="zjmf-op-section">
            <div class="zjmf-op-label">系统维护</div>
            <div class="zjmf-op-btns">
              <el-button @click="opsDialogs?.openReinstall()">
                重装系统
              </el-button>
              <el-button @click="opsDialogs?.openPassDialog()">重置密码</el-button>
              <el-button v-if="!rescueOn" @click="opsDialogs?.openRescue()">救援模式</el-button>
              <el-button v-else type="warning" @click="doExitRescue">
                退出救援
              </el-button>
            </div>
          </div>

          <el-alert
            v-if="rescueOn"
            title="当前处于救援模式，实例已挂载救援系统。"
            type="warning"
            :closable="false"
            show-icon
            class="zjmf-rescue-alert"
          />
        </template>

        <div class="zjmf-card__footer">
          <span class="zjmf-footer-label">远程控制台</span>
          <a :href="consoleHref" target="_blank" rel="noopener" class="zjmf-footer-link">
            打开 VNC 控制台
            <el-icon :size="12"><ArrowRight /></el-icon>
          </a>
        </div>
      </section>

      <!-- ========== 实例信息 ========== -->
      <ZjmfInstanceInfo v-if="hasInstance" :data="data" :service-id="serviceId" @refresh="emit('refresh')" />
      </div>

    <!-- ========== 监控 ========== -->
      <ZjmfMetrics
        v-if="activeView === 'overview' && hasInstance"
        :service-id="serviceId"
        :has-instance="hasInstance"
        :usage="usage"
        :traffic-days="trafficDays"
      />

    <!-- ========== 上游模块方块 ========== -->
      <div v-if="activeView === 'invoices'" class="zjmf-invoices">
        <slot name="invoices" />
      </div>

      <ServiceBlocks
        v-if="['nat_acl', 'nat_web', 'security_groups', 'setting', 'snapshot'].includes(activeView) && data.modules?.length"
        :service-id="serviceId"
        :areas="data.modules"
        :active-tab="activeView"
        :show-tabs="false"
        class="zjmf-blocks"
      />
    </div>

    <!-- ========== 维护弹窗组 ========== -->
    <ZjmfOpsDialogs
      ref="opsDialogs"
      :service-id="serviceId"
      :power-state="powerState"
      @op-done="load"
      @rescue-change="(on) => (rescueOn = on)"
    />
  </div>
</template>

<style scoped>
/* ================================================================
   Art Design Pro 风格 — ZJMF 供应商面板（主框架）
   ================================================================ */

/* --- 全局面板 --- */
.zjmf-panel {
  display: flex;
  flex-direction: column;
  gap: 16px;
  margin-top: 16px;
}

/* --- 主布局网格：左控制台 + 右实例信息 --- */
.zjmf-primary-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.1fr) minmax(320px, 0.9fr);
  gap: 16px;
  align-items: stretch;
}

.zjmf-shell {
  overflow: hidden;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: var(--custom-radius);
  box-shadow: 0 1px 3px rgba(34, 48, 83, 0.04), 0 1px 2px rgba(34, 48, 83, 0.02);
}

/* --- 卡片基底 --- */
.zjmf-card {
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: var(--custom-radius);
  padding: 20px 22px;
  box-shadow: 0 1px 3px rgba(34, 48, 83, 0.04), 0 1px 2px rgba(34, 48, 83, 0.02);
  transition: box-shadow 0.2s ease;
}
.zjmf-card:hover {
  box-shadow: 0 4px 12px rgba(34, 48, 83, 0.06), 0 2px 4px rgba(34, 48, 83, 0.03);
}

/* --- 卡片头部 --- */
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

/* --- 顶部功能导航 --- */
.zjmf-tabs {
  display: flex;
  align-items: center;
  gap: 4px;
  min-height: 51px;
  padding: 8px 12px 0;
  background: var(--art-gray-50);
  border-bottom: 1px solid var(--art-card-border);
  overflow-x: auto;
}
.zjmf-tab {
  flex: 0 0 auto;
  height: 42px;
  padding: 0 18px;
  color: var(--art-gray-500);
  font-size: 12px;
  font-weight: 500;
  background: transparent;
  border: 0;
  border-radius: 7px 7px 0 0;
  cursor: pointer;
  transition: color 0.2s ease, background-color 0.2s ease, box-shadow 0.2s ease;
}
.zjmf-tab:hover {
  color: var(--theme-color-deep);
  background: var(--art-gray-50);
}
.zjmf-tab.is-active {
  color: var(--theme-color-deep);
  font-weight: 600;
  background: var(--default-box-color);
  box-shadow: 0 -1px 0 var(--art-card-border), 1px 0 0 var(--art-card-border), -1px 0 0 var(--art-card-border);
}

/* --- 状态标签 --- */
.zjmf-status-tag {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-top: 2px;
  font-weight: 500;
}
.zjmf-status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--art-gray-400);
}
.zjmf-status-dot.is-on {
  background: var(--el-color-success);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--el-color-success) 20%, transparent);
}
.zjmf-status-dot.is-operating {
  background: var(--el-color-warning);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--el-color-warning) 20%, transparent);
}
.zjmf-status-dot.is-fault {
  background: var(--el-color-danger);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--el-color-danger) 20%, transparent);
}
.zjmf-status-dot.is-off {
  background: var(--art-gray-400);
}

/* --- 操作区 --- */
.zjmf-op-section {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  margin-top: 16px;
}
.zjmf-op-label {
  width: 72px;
  flex: 0 0 auto;
  padding-top: 8px;
  color: var(--art-gray-500);
  font-size: 12px;
  font-weight: 500;
}
.zjmf-op-btns {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.zjmf-op-btns .el-button {
  font-weight: 500;
  border-radius: 8px;
}

/* --- 救援提示 --- */
.zjmf-rescue-alert {
  margin-top: 14px;
}

/* --- 卡片底部链接 --- */
.zjmf-card__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 18px;
  padding-top: 14px;
  border-top: 1px solid var(--art-card-border);
}
.zjmf-footer-label {
  color: var(--art-gray-400);
  font-size: 12px;
}
.zjmf-footer-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--theme-color-deep);
  font-size: 12px;
  font-weight: 600;
  text-decoration: none;
  transition: color 0.15s ease;
}
.zjmf-footer-link:hover {
  color: var(--theme-color);
  text-decoration: underline;
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

/* --- 上游模块间距 --- */
.zjmf-blocks {
  margin-top: 0;
}

/* ================================================================
   响应式
   ================================================================ */
@media (max-width: 820px) {
  .zjmf-primary-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 560px) {
  .zjmf-op-section {
    flex-direction: column;
    gap: 6px;
  }
  .zjmf-op-label {
    width: auto;
    padding-top: 0;
  }
}
</style>
