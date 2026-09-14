<script setup lang="ts">
/**
 * ZJMF 实例监控卡：流量概览 + 四宫格资源图表 + 每日流量。
 * 自含指标时间窗与图表拉取（含请求代次防竞态），服务切换时自动重载。
 */
import { computed, onMounted, ref, watch } from 'vue'
import ArtLineChart from '../../core/charts/art-line-chart/index.vue'
import { fetchChart, type UsageInfo, type TrafficDay, type ChartSeries } from '../../../api/user'

const props = defineProps<{
  serviceId: number
  hasInstance: boolean
  usage: UsageInfo | null
  trafficDays: TrafficDay[]
}>()

const trafficPercent = computed(() =>
  props.usage && props.usage.traffic_limit > 0
    ? Math.min(100, Math.round((props.usage.traffic_used / props.usage.traffic_limit) * 100))
    : null,
)
const trafficBarClass = computed(() => {
  if (trafficPercent.value === null) return ''
  if (trafficPercent.value >= 90) return 'is-danger'
  if (trafficPercent.value >= 70) return 'is-warn'
  return 'is-ok'
})

const metrics = [
  { key: 'cpu', label: 'CPU', icon: '⚡' },
  { key: 'memory', label: '内存', icon: '📊' },
  { key: 'flow', label: '网卡', icon: '🌐' },
  { key: 'disk', label: '硬盘 IO', icon: '💾' },
]
const metricWindow = ref<[Date, Date]>([new Date(Date.now() - 7 * 24 * 60 * 60 * 1000), new Date()])
const metricSeries = ref<Record<string, ChartSeries | null>>({})
const metricLoading = ref(false)
let metricRequestId = 0

async function loadMetrics() {
  if (!props.hasInstance) return
  const [rangeStartDate, rangeEndDate] = metricWindow.value
  const rangeStartMs = rangeStartDate instanceof Date ? rangeStartDate.getTime() : rangeStartDate
  const rangeEndMs = rangeEndDate instanceof Date ? rangeEndDate.getTime() : rangeEndDate
  const durationHours = Math.max(1, (rangeEndMs - rangeStartMs) / (60 * 60 * 1000))
  const upstreamRange = durationHours <= 1 ? '1h' : durationHours <= 24 ? '24h' : '7d'
  const requestId = ++metricRequestId
  metricLoading.value = true
  const results = await Promise.allSettled(
    metrics.map((m) => fetchChart(props.serviceId, m.key, upstreamRange)),
  )
  if (requestId !== metricRequestId) return
  const rangeStart = rangeStartMs
  const rangeEnd = rangeEndMs
  metricSeries.value = Object.fromEntries(
    metrics.map((m, i) => [
      m.key,
      results[i].status === 'fulfilled' && results[i].value.lines?.length
        ? {
            ...results[i].value,
            lines: results[i].value.lines.map((line) => ({
              ...line,
              points: line.points.filter((point) => {
                const time = Date.parse(point.time)
                return Number.isNaN(time) || (time >= rangeStart && time <= rangeEnd)
              }),
            })),
          }
        : null,
    ]),
  )
  metricLoading.value = false
}

function changeMetricWindow(value: [Date, Date] | null) {
  if (!value?.[0] || !value?.[1]) return
  metricWindow.value = value
  void loadMetrics()
}

// 四宫格图表数据：无数据时传空数组，由 ArtLineChart 自行显示「暂无数据」
const metricCharts = computed(() =>
  Object.fromEntries(
    metrics.map((m) => {
      const s = metricSeries.value[m.key]
      return [
        m.key,
        {
          labels: s?.lines[0]?.points.map((p) => p.time) ?? [],
          data: (s?.lines ?? []).map((l) => ({
            name: l.label || m.label,
            data: l.points.map((p) => p.value),
          })),
        },
      ]
    }),
  ),
)

const trafficSeries = computed(() => [
  { name: '入站', data: props.trafficDays.map((d) => d.in) },
  { name: '出站', data: props.trafficDays.map((d) => d.out) },
])

onMounted(loadMetrics)
watch(() => props.serviceId, loadMetrics)
watch(() => props.hasInstance, loadMetrics)
</script>

<template>
  <section class="zjmf-card zjmf-card--monitor art-card">
    <div class="zjmf-card__header">
      <div class="zjmf-card__title">
        <h2>资源监控</h2>
        <p>流量用量与四类资源状态</p>
      </div>
      <el-date-picker
        v-model="metricWindow"
        type="datetimerange"
        size="small"
        class="zjmf-range-picker"
        range-separator="至"
        start-placeholder="开始时间"
        end-placeholder="结束时间"
        format="YYYY/MM/DD HH:mm"
        :clearable="false"
        @change="changeMetricWindow"
      />
    </div>

    <!-- 流量概览 -->
    <div v-if="usage" class="zjmf-traffic-bar">
      <div class="zjmf-traffic-row">
        <span class="zjmf-traffic-label">已用流量</span>
        <span class="zjmf-traffic-value">
          <b>{{ usage.traffic_used }} GB</b>
          <template v-if="usage.traffic_limit > 0">
            <span class="zjmf-traffic-sep">/</span>
            <span>{{ usage.traffic_limit }} GB</span>
          </template>
          <template v-else>
            <span class="zjmf-traffic-unlimited">（不限量）</span>
          </template>
        </span>
      </div>
      <div v-if="trafficPercent !== null" class="zjmf-progress">
        <div class="zjmf-progress__track">
          <div class="zjmf-progress__fill" :class="trafficBarClass" :style="{ width: `${trafficPercent}%` }"></div>
        </div>
        <span class="zjmf-progress__pct">{{ trafficPercent }}%</span>
      </div>
    </div>

    <!-- 四宫格监控 -->
    <div v-loading="metricLoading" class="zjmf-metric-grid">
      <div v-for="m in metrics" :key="m.key" class="zjmf-metric-card">
        <div class="zjmf-metric-head">
          <span class="zjmf-metric-label">{{ m.label }}</span>
        </div>
        <!-- ponytail: ArtLineChart 的 tooltip 不支持单位格式化（如 MB/s），悬浮只显示数值； -->
        <!-- 若要恢复需给 Art 组件加 formatter prop，目前先接受。 -->
        <ArtLineChart
          :data="metricCharts[m.key].data"
          :xAxisData="metricCharts[m.key].labels"
          :showLegend="metricCharts[m.key].data.length > 1"
          legendPosition="top"
          :showAxisLine="false"
          height="13rem"
        />
      </div>
    </div>

    <!-- 每日流量 -->
    <div v-if="trafficDays.length" class="zjmf-traffic-daily">
      <p class="zjmf-section-label">每日流量（入站 / 出站）</p>
      <ArtLineChart
        :data="trafficSeries"
        :xAxisData="trafficDays.map((d) => d.time)"
        :showLegend="true"
        legendPosition="top"
        :showAxisLine="false"
        height="13rem"
      />
    </div>
  </section>
</template>

<style scoped>
/* 盒样式（背景/描边/圆角/阴影）交由全局 art-card 按 data-box-mode 接管 */
.zjmf-card {
  padding: 20px 22px;
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

.zjmf-range-picker { width: 340px; }

/* --- 流量概览 --- */
.zjmf-traffic-bar {
  margin-top: 16px;
  padding: 14px 16px;
  background: var(--art-gray-50);
  border: 1px solid var(--art-card-border);
  border-radius: 10px;
}
.zjmf-traffic-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12.5px;
}
.zjmf-traffic-label {
  color: var(--art-gray-500);
  font-weight: 450;
}
.zjmf-traffic-value {
  display: flex;
  align-items: baseline;
  gap: 4px;
}
.zjmf-traffic-value b {
  color: var(--art-gray-900);
  font-weight: 650;
  font-size: 14px;
}
.zjmf-traffic-sep {
  color: var(--art-gray-300);
}
.zjmf-traffic-unlimited {
  color: var(--art-gray-400);
  font-size: 11px;
}
.zjmf-progress {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 10px;
}
.zjmf-progress__track {
  flex: 1;
  height: 7px;
  overflow: hidden;
  background: var(--art-gray-200);
  border-radius: 6px;
}
.zjmf-progress__fill {
  height: 100%;
  border-radius: 6px;
  transition: width 0.35s ease;
}
.zjmf-progress__fill.is-ok {
  background: linear-gradient(90deg, var(--theme-color), color-mix(in srgb, var(--theme-color) 80%, #8baeff));
}
.zjmf-progress__fill.is-warn {
  background: linear-gradient(90deg, var(--el-color-warning), #f5a623);
}
.zjmf-progress__fill.is-danger {
  background: linear-gradient(90deg, var(--el-color-danger), #f56c6c);
}
.zjmf-progress__pct {
  flex: 0 0 auto;
  color: var(--art-gray-400);
  font-size: 11px;
  font-weight: 600;
}

/* --- 四宫格监控 --- */
.zjmf-metric-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
  margin-top: 16px;
}
.zjmf-metric-card {
  min-width: 0;
  padding: 12px 14px 6px;
  border: 1px solid var(--art-card-border);
  border-radius: 10px;
  background: var(--art-gray-50);
  transition: border-color 0.15s ease;
}
.zjmf-metric-card:hover {
  border-color: var(--theme-color-light-7, var(--el-color-primary-light-7));
}
.zjmf-metric-head {
  margin-bottom: 4px;
}
.zjmf-metric-label {
  color: var(--art-gray-600);
  font-size: 12px;
  font-weight: 600;
}

/* --- 每日流量 --- */
.zjmf-traffic-daily {
  margin-top: 18px;
  padding: 14px 16px;
  border: 1px solid var(--art-card-border);
  border-radius: 10px;
  background: var(--art-gray-50);
}
.zjmf-section-label {
  margin: 0 0 8px;
  color: var(--art-gray-500);
  font-size: 12px;
  font-weight: 500;
}

@media (max-width: 820px) {
  .zjmf-metric-grid {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 480px) { .zjmf-range-picker { width: 100%; } }
</style>
