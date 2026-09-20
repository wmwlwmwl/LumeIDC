<!-- 前台插件页动态壳：按路由参数 name 从 clientRegistry 取插件组件渲染。 -->
<template>
  <component :is="comp" v-if="comp" />
  <div v-else class="art-card p-5 text-center" style="opacity: 0.7">页面不存在或插件未启用</div>
</template>

<script setup lang="ts">
  import { computed, defineAsyncComponent } from 'vue'
  import { useRoute } from 'vue-router'
  import { clientPluginComponent } from './registry'

  const route = useRoute()
  const comp = computed(() => {
    const loader = clientPluginComponent(String(route.params.name || ''))
    return loader ? defineAsyncComponent(loader) : null
  })
</script>
