<!-- 后台根组件：提供 Element Plus 全局配置与路由出口 -->
<template>
  <ElConfigProvider
    size="default"
    :locale="zhCn"
    :z-index="3000"
    :card="{ shadow: 'never' }"
  >
    <RouterView />
  </ElConfigProvider>
</template>

<script setup lang="ts">
  import { onBeforeMount, onMounted } from 'vue'
  import zhCn from 'element-plus/es/locale/lang/zh-cn'
  import { initializeTheme } from '@/hooks/core/useTheme'
  import { toggleTransition } from '@/utils/ui/animation'
  import { useSettingStore } from '@/store/modules/setting'

  defineOptions({ name: 'AdminRoot' })

  // 与 Art App.vue 对齐：挂载前应用主题并临时关闭过渡，避免首屏闪烁。
  onBeforeMount(() => {
    toggleTransition(true)
    const setting = useSettingStore()
    initializeTheme()
    document.documentElement.setAttribute(
      'data-box-mode',
      setting.boxBorderMode ? 'border-mode' : 'shadow-mode',
    )
  })

  onMounted(() => {
    toggleTransition(false)
  })
</script>
