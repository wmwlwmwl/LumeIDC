<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import ArtStatsCard from '../components/core/cards/art-stats-card/index.vue'
import ArtLineChart from '../components/core/charts/art-line-chart/index.vue'
import ArtBarChart from '../components/core/charts/art-bar-chart/index.vue'
import ArtDataListCard from '../components/core/cards/art-data-list-card/index.vue'
import {
  fetchDashboard,
  fetchOrders,
  type AdminCounts,
  type AdminTrendPoint,
  type OrderItem,
} from '../admin/api'

const router = useRouter()
const counts = ref<AdminCounts>({ users: 0, orders: 0, services: 0 })
const trends = ref<AdminTrendPoint[]>([])
const orders = ref<OrderItem[]>([])
const loading = ref(false)

onMounted(async () => {
  loading.value = true
  try {
    const [dash, ord] = await Promise.all([
      fetchDashboard(),
      fetchOrders('', 1).catch(() => ({ list: [] as OrderItem[], total: 0, profit: '0.00' })),
    ])
    counts.value = dash.counts
    trends.value = dash.trends
    orders.value = ord.list
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '看板加载失败')
  } finally {
    loading.value = false
  }
})

const stats = [
  { key: 'users', des: '注册用户', icon: 'ri:user-3-line', iconStyle: 'bg-primary', to: '/users' },
  { key: 'orders', des: '已支付订单', icon: 'ri:file-list-3-line', iconStyle: 'bg-secondary', to: '/orders' },
  { key: 'services', des: '运行中服务', icon: 'ri:server-line', iconStyle: 'bg-warning', to: '/services' },
] as const

const quickLinks = [
  { to: '/products/new', title: '新增产品', desc: '创建新的售卖套餐', icon: 'ri:apps-2-line' },
  { to: '/orders', title: '处理订单', desc: '查看支付与退款记录', icon: 'ri:file-list-3-line' },
  { to: '/verifications', title: '实名审核', desc: '处理待审身份申请', icon: 'ri:shield-check-line' },
  { to: '/site', title: '站点设置', desc: '品牌、联系与后台路径', icon: 'ri:global-line' },
]

const orderSeries = computed(() => trends.value.map((t) => t.orders))

// X 轴日期标签（后端按 MM-DD 返回）
const trendLabels = computed(() => trends.value.map((t) => t.date))

const weekOrders = computed(() => orderSeries.value.reduce((a, n) => a + n, 0))
const orderGrowth = computed(() => {
  const n = trends.value.length
  if (n < 2) return 0
  const prev = trends.value[n - 2].orders
  const cur = trends.value[n - 1].orders
  if (prev === 0) return cur > 0 ? 100 : 0
  return Math.round(((cur - prev) / prev) * 100)
})

const revenueSeries = computed(() => trends.value.map((t) => Number(t.revenue.toFixed(2))))
const weekRevenue = computed(() => revenueSeries.value.reduce((a, n) => a + n, 0))
const revenueGrowth = computed(() => {
  const n = trends.value.length
  if (n < 2) return 0
  const prev = trends.value[n - 2].revenue
  const cur = trends.value[n - 1].revenue
  if (prev === 0) return cur > 0 ? 100 : 0
  return Math.round(((cur - prev) / prev) * 100)
})

const recentList = computed(() =>
  orders.value.slice(0, 5).map((o) => ({
    title: `${o.email || '用户'} · ￥${o.amount}`,
    status: `${o.cycle} · ${o.status}`,
    time: o.profit && o.profit !== '0' && o.profit !== '0.00' ? `利润 ￥${o.profit}` : '',
    class: 'bg-theme/12 text-theme',
    icon: 'ri:file-list-3-line',
  })),
)
</script>

<template>
  <div class="art-full-height" v-loading="loading">
    <ElRow :gutter="20">
      <ElCol v-for="s in stats" :key="s.key" :xs="24" :sm="12" :md="8">
        <ArtStatsCard
          class="mb-5 cursor-pointer"
          :icon="s.icon"
          :icon-style="s.iconStyle"
          :title="s.des"
          :count="counts[s.key]"
          description="点击查看详情"
          @click="router.push(s.to)"
        />
      </ElCol>
    </ElRow>

    <ElRow :gutter="20">
      <!-- 用 inline display 覆盖 Element Plus 的 .el-col-*.is-guttered{display:block}
           （其特异性 0,2,0 高于 Tailwind 的 .flex），否则卡片不会被拉伸到行高 -->
      <ElCol :xs="24" :md="12" :lg="8" style="display: flex">
        <div class="art-card flex flex-1 flex-col p-5 mb-5">
          <div class="art-card-header shrink-0">
            <div class="title">
              <h4>近 7 日已支付订单</h4>
              <p>
                共 {{ weekOrders }} 单
                <span :class="orderGrowth >= 0 ? 'text-success' : 'text-danger'">
                  {{ orderGrowth > 0 ? '+' : '' }}{{ orderGrowth }}%
                </span>
              </p>
            </div>
          </div>
          <ArtLineChart
            class="flex-1 min-h-[16rem]"
            height="100%"
            :data="orderSeries"
            :xAxisData="trendLabels"
            :showAreaColor="true"
            :showAxisLine="false"
          />
        </div>
      </ElCol>
      <ElCol :xs="24" :md="12" :lg="8" style="display: flex">
        <div class="art-card flex flex-1 flex-col p-5 mb-5">
          <div class="art-card-header shrink-0">
            <div class="title">
              <h4>近 7 日收入（元）</h4>
              <p>
                共 ￥{{ weekRevenue.toFixed(2) }}
                <span :class="revenueGrowth >= 0 ? 'text-success' : 'text-danger'">
                  {{ revenueGrowth > 0 ? '+' : '' }}{{ revenueGrowth }}%
                </span>
              </p>
            </div>
          </div>
          <ArtBarChart
            class="flex-1 min-h-[16rem]"
            height="100%"
            :data="revenueSeries"
            :xAxisData="trendLabels"
            barWidth="50%"
            :showAxisLine="false"
          />
        </div>
      </ElCol>
      <ElCol :xs="24" :lg="8">
        <ArtDataListCard
          class="mb-5"
          title="最新订单"
          subtitle="最近 5 笔成交"
          :list="recentList"
          :max-count="5"
          show-more-button
          @more="router.push('/orders')"
        />
      </ElCol>
    </ElRow>

    <div class="art-card mb-5 p-5">
      <div class="art-card-header">
        <div class="title">
          <h4>快捷入口</h4>
          <p>常用管理操作</p>
        </div>
      </div>
      <ElRow :gutter="12" class="mt-4">
        <ElCol v-for="q in quickLinks" :key="q.to" :xs="24" :sm="12" :md="6">
          <div
            class="flex-c gap-3 p-3 rounded-lg cursor-pointer hover:bg-g-200/60"
            @click="router.push(q.to)"
          >
            <span class="size-9 flex-cc rounded-lg bg-theme/10 text-theme">
              <ArtSvgIcon :icon="q.icon" />
            </span>
            <div class="min-w-0">
              <h4 class="m-0 text-sm font-medium text-g-800">{{ q.title }}</h4>
              <p class="m-0 mt-1 text-xs text-g-500 truncate">{{ q.desc }}</p>
            </div>
          </div>
        </ElCol>
      </ElRow>
    </div>
  </div>
</template>
