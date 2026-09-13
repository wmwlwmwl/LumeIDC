<!-- 统计卡片（移植自 Art Design Pro） -->
<template>
  <div
    class="art-card h-32 flex-c px-5 transition-transform duration-200 hover:-translate-y-0.5"
    :class="boxStyle"
  >
    <div v-if="icon" class="mr-4 size-11 flex-cc rounded-lg text-xl text-white" :class="iconStyle">
      <ArtSvgIcon :icon="icon" />
    </div>
    <div class="flex-1">
      <p v-if="title" class="m-0 text-lg font-medium" :style="{ color: textColor }">
        {{ title }}
      </p>
      <ArtCountTo
        v-if="count !== undefined"
        class="m-0 text-2xl font-medium"
        :target="count"
        :duration="2000"
        :decimals="decimals"
        :separator="separator"
      />
      <p
        v-if="description"
        class="mt-1 text-sm text-g-500 opacity-90"
        :style="{ color: textColor }"
      >
        {{ description }}
      </p>
    </div>
    <div v-if="showArrow">
      <ArtSvgIcon icon="ri:arrow-right-s-line" class="text-xl text-g-500" />
    </div>
  </div>
</template>

<script setup lang="ts">
  defineOptions({ name: 'ArtStatsCard' })

  interface StatsCardProps {
    /** 盒子附加样式 */
    boxStyle?: string
    /** 图标（Iconify 名称） */
    icon?: string
    /** 图标容器样式，如 bg-primary / bg-success */
    iconStyle?: string
    /** 标题 */
    title?: string
    /** 数值 */
    count?: number
    /** 小数位 */
    decimals?: number
    /** 千分位分隔符 */
    separator?: string
    /** 描述 */
    description: string
    /** 文本颜色 */
    textColor?: string
    /** 是否显示箭头 */
    showArrow?: boolean
  }

  withDefaults(defineProps<StatsCardProps>(), {
    decimals: 0,
    separator: ','
  })
</script>
