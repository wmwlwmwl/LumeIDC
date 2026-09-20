<!-- 插件页动态壳：按路由参数 name 渲染插件后台页。
     布局：配置 schema 自动表单（若有）在上，插件自定义组件（若注册）在下。 -->
<template>
  <div class="plugin-shell p-4">
    <PluginAutoConfig v-if="showConfig" :plugin-name="name" :title="pluginTitle" class="mb-4" />
    <component :is="customComp" v-if="customComp" />
    <div v-if="ready && !showConfig && !customComp" class="plugin-missing">
      <el-result icon="warning" title="插件不存在" sub-title="该插件未注册后台页面，或已被移除" />
    </div>
  </div>
</template>

<script setup lang="ts">
  import { computed, defineAsyncComponent, onMounted, ref } from 'vue'
  import { useRoute } from 'vue-router'
  import { http } from '@/http/index'
  import PluginAutoConfig from './PluginAutoConfig.vue'
  import { pluginComponent } from './registry'

  const route = useRoute()
  const name = computed(() => String(route.params.name || ''))
  const ready = ref(false)
  const showConfig = ref(false)
  const pluginTitle = ref('')

  const customComp = computed(() => {
    const loader = pluginComponent(name.value)
    return loader ? defineAsyncComponent(loader) : null
  })

  onMounted(async () => {
    try {
      const res = await http.get<{ ok: number; plugins?: { name: string; title: string; hasConfig: boolean }[] }>(
        '/plugins',
      )
      const item = (res.plugins || []).find((p) => p.name === name.value)
      if (item) {
        showConfig.value = item.hasConfig
        pluginTitle.value = item.title
      }
    } catch {
      // 清单不可用：仅按本地 registry 渲染自定义组件
    } finally {
      ready.value = true
    }
  })
</script>

<style scoped>
  .plugin-missing {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 50vh;
  }
</style>
