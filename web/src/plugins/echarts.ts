/**
 * ECharts 插件配置
 *
 * 按需导入 ECharts 图表和组件，减小打包体积。
 * 项目实际只用折线/柱状两类图表（art-line-chart / art-bar-chart），
 * 新增其他图表时在这里补注册即可。
 */

// ECharts 按需导入配置
import * as echarts from 'echarts/core'

// 导入图表类型
import { BarChart, LineChart } from 'echarts/charts'

// 导入组件
import { TooltipComponent, GridComponent, LegendComponent } from 'echarts/components'

// 导入渲染器
import { CanvasRenderer } from 'echarts/renderers'

// 注册必要的组件
echarts.use([
  // 图表类型
  BarChart,
  LineChart,

  // 组件
  TooltipComponent,
  GridComponent,
  LegendComponent,

  // 渲染器
  CanvasRenderer
])

// 导出 echarts 实例和类型
export { echarts }
export type { EChartsOption, BarSeriesOption } from 'echarts'

// 导出常用的图形工具
export const graphic = echarts.graphic
