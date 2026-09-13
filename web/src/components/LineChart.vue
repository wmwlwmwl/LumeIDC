<script setup lang="ts">
/**
 * 通用折线图（ECharts 按需注册，异步加载，不进首屏）。
 * 监控类数据统一走这里：流量（入/出）、资源（CPU/内存/磁盘/网卡）等。
 */
import { ref, watch, onMounted, onBeforeUnmount, shallowRef } from 'vue'

interface Series {
  name: string
  data: number[]
  color?: string
}

const props = defineProps<{
  labels: string[]
  series: Series[]
  unit?: string
}>()

const el = ref<HTMLElement | null>(null)
// ECharts 实例/模块类型复杂，用 any 承载（配置为纯数据驱动）
const chart = shallowRef<any>(null)
const echarts = shallowRef<any>(null)

// ECharts 画布无法解析 CSS 变量，渲染时从 :root 读取当前主题值（含暗色模式）。
function cssVar(name: string, fallback: string): string {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return v || fallback
}
function palette(): string[] {
  return [
    cssVar('--theme-color', ''),
    cssVar('--el-color-success', ''),
    cssVar('--el-color-warning', ''),
    cssVar('--el-color-danger', ''),
  ]
}

async function render() {
  if (!el.value || !props.labels.length || !props.series.length) return
  if (!echarts.value) {
    const core = await import('echarts/core')
    const { LineChart } = await import('echarts/charts')
    const { GridComponent, TooltipComponent, LegendComponent } = await import('echarts/components')
    const { CanvasRenderer } = await import('echarts/renderers')
    core.use([LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])
    echarts.value = core
  }
  const ec = echarts.value
  // 动态 import 期间组件可能已卸载：el 被置空后不能继续 init
  if (!el.value) return
  if (!chart.value) {
    chart.value = ec.init(el.value)
  }
  const colors = palette()
  chart.value.setOption(
    {
      grid: { left: 8, right: 12, top: 28, bottom: 4, containLabel: true },
      tooltip: {
        trigger: 'axis',
        valueFormatter: (v: number) => (props.unit ? `${v} ${props.unit}` : String(v)),
      },
      legend: {
        data: props.series.map((s) => s.name),
        right: 0,
        top: 0,
        itemWidth: 12,
        itemHeight: 8,
        textStyle: { fontSize: 11 },
        show: props.series.length > 1,
      },
      xAxis: { type: 'category', data: props.labels, axisLabel: { fontSize: 10 }, boundaryGap: false },
      yAxis: {
        type: 'value',
        axisLabel: { fontSize: 10 },
        splitLine: { lineStyle: { color: cssVar('--art-gray-200', '') } },
      },
      series: props.series.map((s, i) => ({
        name: s.name,
        type: 'line',
        smooth: true,
        showSymbol: false,
        data: s.data,
        areaStyle: { opacity: 0.12 },
        lineStyle: { width: 2 },
        color: s.color || colors[i % colors.length],
      })),
    },
    true, // 数据类型切换时整体替换，避免残留旧系列
  )
}

function resize() {
  chart.value?.resize()
}

onMounted(() => {
  render()
  window.addEventListener('resize', resize)
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', resize)
  chart.value?.dispose()
  chart.value = null
})
watch(() => [props.labels, props.series], render, { deep: true })
</script>

<template>
  <div ref="el" class="h-56 w-full"></div>
</template>