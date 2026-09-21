<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Search, RefreshRight } from '@element-plus/icons-vue'
import { fetchServices, renewService, type ServiceLite } from '@/api/user'
import { formatDate, formatMoney } from '@/utils/format'
import PublicPageHead from '@/components/public/PublicPageHead.vue'
import ArtStatsCard from '@/components/core/cards/art-stats-card/index.vue'

const router = useRouter()
const list = ref<ServiceLite[]>([])
const loading = ref(false)
const loadError = ref(false)
const keyword = ref('')
const tab = ref('all')
const page = ref(1)
const pageSize = ref(12)

const activeCount = computed(() => list.value.filter((s) => s.status === 1).length)
const expiringCount = computed(() => list.value.filter((s) => s.expiring_soon).length)

// 续费人工处理中（上游涨价/欠费等待充值）：钱已收、到期时间已延长，再续一次会重复付费。
function renewingOf(row: ServiceLite) {
  return row.transition === 'renew_pending'
}

const tabs = computed(() => [
  { key: 'all', label: '全部', count: list.value.length },
  { key: 'pending', label: '待开通', count: list.value.filter((s) => s.status === 0).length },
  { key: 'active', label: '运行中', count: activeCount.value },
  { key: 'stopped', label: '已停机', count: list.value.filter((s) => s.status === 2).length },
  { key: 'expiring', label: '即将到期', count: expiringCount.value },
])

const filtered = computed(() => {
  let out = list.value
  if (tab.value === 'pending') out = out.filter((s) => s.status === 0)
  else if (tab.value === 'active') out = out.filter((s) => s.status === 1)
  else if (tab.value === 'stopped') out = out.filter((s) => s.status === 2)
  else if (tab.value === 'expiring') out = out.filter((s) => s.expiring_soon)
  const q = keyword.value.trim().toLowerCase()
  if (!q) return out
  return out.filter(
    (s) =>
      s.name.toLowerCase().includes(q) ||
      (s.hostname || '').toLowerCase().includes(q) ||
      (s.ip || '').toLowerCase().includes(q) ||
      (s.os || '').toLowerCase().includes(q),
  )
})

const total = computed(() => filtered.value.length)
const paged = computed(() => filtered.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value))

watch([tab, keyword], () => {
  page.value = 1
})

async function load() {
  loading.value = true
  loadError.value = false
  try {
    list.value = await fetchServices()
  } catch (err: unknown) {
    loadError.value = true
    ElMessage.error((err as Error).message || '读取服务失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

const statusType: Record<number, 'info' | 'success' | 'warning' | 'danger'> = {
  0: 'warning', // 待开通
  1: 'success', // 激活
  2: 'info', // 已停机
  3: 'danger', // 已删除
}

function daysText(s: ServiceLite): string {
  if (s.days_left > 0) return `${s.days_left} 天后到期`
  if (s.days_left === 0) return '今天到期'
  return '已到期'
}

const renewDialog = ref(false)
const renewTarget = ref<ServiceLite | null>(null)
const renewCycle = ref('monthly')
const renewCoupon = ref('')
const renewBusy = ref(false)

function openRenew(s: ServiceLite) {
  renewTarget.value = s
  renewCycle.value = s.default_cycle || 'monthly'
  renewCoupon.value = ''
  renewDialog.value = true
}

async function doRenew() {
  if (!renewTarget.value) return
  renewBusy.value = true
  try {
    const result = await renewService(renewTarget.value.id, renewCycle.value, renewCoupon.value.trim())
    if (result.redirect) {
      location.href = result.redirect
      return
    }
    // HTTP 200 也可能业务失败（ok=0），失败时保留弹窗让用户重试
    if (String(result.ok) !== '1') {
      ElMessage.error(result.msg || '续费失败')
      return
    }
    renewDialog.value = false
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '续费失败')
  } finally {
    renewBusy.value = false
  }
}
</script>

<template>
  <div>
    <PublicPageHead title="我的服务" subtitle="运行中的云资源都在这里统一管理。">
      <template #extra>
        <RouterLink to="/cart" class="svc-buy">购买新产品</RouterLink>
      </template>
    </PublicPageHead>

    <ElRow :gutter="20">
      <ElCol :xs="24" :sm="8">
        <ArtStatsCard
          class="mb-4"
          icon="ri:server-line"
          icon-style="bg-primary"
          title="服务总数"
          :count="list.length"
          description="全部服务实例"
        />
      </ElCol>
      <ElCol :xs="24" :sm="8">
        <ArtStatsCard
          class="mb-4"
          icon="ri:play-circle-line"
          icon-style="bg-secondary"
          title="运行中"
          :count="activeCount"
          description="正常运行"
        />
      </ElCol>
      <ElCol :xs="24" :sm="8">
        <ArtStatsCard
          class="mb-4"
          icon="ri:time-line"
          icon-style="bg-warning"
          title="即将到期"
          :count="expiringCount"
          description="7 天内到期"
        />
      </ElCol>
    </ElRow>

    <div class="art-card svc-toolbar">
      <div class="svc-tabs">
        <button
          v-for="t in tabs"
          :key="t.key"
          type="button"
          class="svc-tab"
          :class="{ 'is-active': tab === t.key }"
          :aria-pressed="tab === t.key"
          @click="tab = t.key"
        >
          {{ t.label }} <em>{{ t.count }}</em>
        </button>
      </div>
      <div class="svc-actions">
        <el-input
          v-model="keyword"
          aria-label="搜索服务"
          placeholder="搜索名称 / 主机名 / IP"
          clearable
          class="svc-search"
        >
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
        <el-button :icon="RefreshRight" :loading="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <div v-loading="loading" class="svc-grid">
      <article v-for="row in paged" :key="row.id" class="art-card svc-card" :class="`is-s${row.status}`">
        <div class="svc-card__head">
          <span class="svc-card__dot" />
          <RouterLink :to="`/services/${row.id}`" class="svc-card__name">{{ row.name }}</RouterLink>
          <el-tag
            :type="renewingOf(row) ? 'warning' : statusType[row.status] || 'info'"
            size="small"
            effect="light"
          >{{ renewingOf(row) ? '续费处理中' : row.status_text }}</el-tag>
        </div>

        <div class="svc-card__rows">
          <div><span>主机名</span><b>{{ row.hostname || '-' }}</b></div>
          <div>
            <span>IP / 系统</span>
            <b>
              <em v-if="row.ip" class="svc-ip">{{ row.ip }}</em>
              <template v-else>-</template>
              <small v-if="row.os">{{ row.os }}</small>
            </b>
          </div>
          <div><span>月价</span><b>￥{{ row.monthly ? formatMoney(row.monthly) : '-' }}</b></div>
          <div>
            <span>到期</span>
            <b :class="{ 'is-soon': row.expiring_soon }">
              {{ row.expires_at ? formatDate(row.expires_at) : '-' }}<small>{{ daysText(row) }}</small>
            </b>
          </div>
        </div>

        <div class="svc-card__ops">
          <el-button size="small" type="primary" @click="router.push(`/services/${row.id}`)">管理</el-button>
          <el-button
            size="small"
            :disabled="renewingOf(row)"
            :title="renewingOf(row) ? '本次续费正在人工处理中，处理完成前无法再次续费' : ''"
            @click="openRenew(row)"
          >续费</el-button>
        </div>
      </article>
    </div>

    <div v-if="loadError" class="svc-state" role="alert">
      <p>服务列表加载失败，请稍后重试。</p>
      <el-button size="small" @click="load">重新加载</el-button>
    </div>
    <el-empty
      v-else-if="!filtered.length && !loading"
      :description="keyword || tab !== 'all' ? '没有匹配的服务' : '暂无服务'"
    />

    <div v-if="total > pageSize" class="svc-pager">
      <el-pagination
        layout="total, prev, pager, next"
        :total="total"
        :page-size="pageSize"
        :current-page="page"
        background
        @current-change="(p: number) => (page = p)"
      />
    </div>

    <el-dialog v-model="renewDialog" title="续费服务" width="400px">
      <p v-if="renewTarget" class="svc-renew-hint">为「{{ renewTarget.name }}」选择续费周期</p>
      <el-form label-position="top">
        <el-form-item label="续费周期">
          <el-select v-model="renewCycle" class="w-full">
            <el-option v-if="renewTarget?.show_monthly" value="monthly" label="按月付" />
            <el-option v-if="renewTarget?.show_q" value="quarterly" label="按季付" />
            <el-option v-if="renewTarget?.show_y" value="yearly" label="按年付" />
          </el-select>
        </el-form-item>
        <el-form-item label="优惠码（选填）">
          <el-input v-model="renewCoupon" placeholder="有续费优惠码可在此输入" clearable />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="renewDialog = false">取消</el-button>
        <el-button type="primary" :loading="renewBusy" @click="doRenew">确认续费</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.svc-buy {
  display: inline-flex;
  align-items: center;
  padding: 9px 14px;
  color: var(--theme-color);
  font-size: 12px;
  font-weight: 600;
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
  transition: background 0.16s ease;
}

.svc-buy:hover {
  background: color-mix(in srgb, var(--theme-color) 16%, transparent);
}

.svc-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;
  padding: 12px 14px;
}

.svc-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.svc-tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 7px 16px;
  color: var(--art-gray-600);
  font-size: 13px;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: 999px;
  cursor: pointer;
  transition: color 0.16s ease, background 0.16s ease, border-color 0.16s ease;
}

.svc-tab em {
  color: var(--art-gray-500);
  font-size: 11px;
  font-style: normal;
}

.svc-tab:hover {
  color: var(--theme-color);
  background: var(--theme-color-soft);
  border-color: color-mix(in srgb, var(--theme-color) 40%, var(--art-card-border));
}

.svc-tab.is-active {
  color: var(--theme-color-contrast);
  background: var(--theme-color);
  border-color: var(--theme-color);
}

.svc-tab.is-active em {
  color: color-mix(in srgb, var(--theme-color-contrast) 82%, transparent);
}

.svc-actions {
  display: flex;
  gap: 10px;
  flex-shrink: 0;
}

.svc-search {
  width: 260px;
}

.svc-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 14px;
}

@media (max-width: 1280px) {
  .svc-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@media (max-width: 960px) {
  .svc-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

.svc-card {
  display: flex;
  flex-direction: column;
  padding: 18px 20px;
  border-left: 3px solid var(--art-gray-300);
  transition: transform 0.2s ease, box-shadow 0.2s ease;
}

.svc-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 12px 28px color-mix(in srgb, var(--art-gray-900) 8%, transparent);
}

.svc-card.is-s1 {
  border-left-color: var(--el-color-success);
}

.svc-card.is-s0 {
  border-left-color: var(--el-color-warning);
}

.svc-card.is-s2 {
  border-left-color: var(--art-gray-400);
}

.svc-card.is-s3 {
  border-left-color: var(--el-color-danger);
}

.svc-card__head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.svc-card__dot {
  width: 8px;
  height: 8px;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--art-gray-400);
}

.svc-card.is-s1 .svc-card__dot {
  background: var(--el-color-success);
}

.svc-card.is-s0 .svc-card__dot {
  background: var(--el-color-warning);
}

.svc-card.is-s2 .svc-card__dot {
  background: var(--art-gray-400);
}

.svc-card.is-s3 .svc-card__dot {
  background: var(--el-color-danger);
}

.svc-card__name {
  overflow: hidden;
  flex: 1;
  min-width: 0;
  color: var(--art-gray-900);
  font-size: 15px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
  border-radius: var(--radius-sm);
}

.svc-card__name:hover {
  color: var(--theme-color);
}

.svc-card__rows {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin: 16px 0;
  padding: 14px 0;
  border-top: 1px solid var(--art-card-border);
  border-bottom: 1px solid var(--art-card-border);
}

.svc-card__rows > div {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  font-size: 13px;
}

.svc-card__rows span {
  flex-shrink: 0;
  color: var(--art-gray-500);
}

.svc-card__rows b {
  overflow: hidden;
  color: var(--art-gray-700);
  font-weight: 500;
  text-align: right;
  text-overflow: ellipsis;
}

.svc-card__rows b small {
  display: block;
  color: var(--art-gray-400);
  font-size: 11px;
}

.svc-card__rows b.is-soon,
.svc-card__rows b.is-soon small {
  color: var(--el-color-warning);
}

.svc-ip {
  color: var(--art-gray-700);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-style: normal;
  font-size: 12px;
}

.svc-card__ops {
  display: flex;
  gap: 8px;
  margin-top: auto;
}

.svc-pager {
  display: flex;
  justify-content: center;
  margin-top: 22px;
}

.svc-renew-hint {
  margin: 0 0 14px;
  color: var(--art-gray-500);
  font-size: 12px;
}

.svc-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 44px 20px;
  text-align: center;
}

.svc-state p {
  margin: 0;
  color: var(--art-gray-600);
  font-size: 14px;
}

@media (max-width: 720px) {
  .svc-toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .svc-actions {
    flex-direction: column;
  }

  .svc-search {
    width: 100%;
  }

  .svc-grid {
    grid-template-columns: 1fr;
  }
}
</style>
