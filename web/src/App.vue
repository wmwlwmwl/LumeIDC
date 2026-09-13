<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeMount } from 'vue'
import { useRoute } from 'vue-router'
import { ElConfigProvider } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import PublicLayout from './components/layout/PublicLayout.vue'
import { installPublicTheme } from './components/public/usePublicTheme'

// 认证外壳（Art 两栏）按需加载，避免营销首包带上登录页资源。
const PublicAuthLayout = defineAsyncComponent(
  () => import('./components/layout/PublicAuthLayout.vue'),
)

const route = useRoute()
// 登录/注册等认证页使用 Art 两栏外壳（无营销头尾）。
const isAuthLayout = computed(() => route.meta.authLayout === true)
// 裸页（如安装向导）不套任何外壳。
const isBare = computed(() => route.meta.bare === true)

onBeforeMount(() => {
  // 前台主题：应用持久化明暗/盒模型，并锁定 public-app 根类与统一主色（#5D87FF）。
  installPublicTheme()
})
</script>

<template>
  <ElConfigProvider size="default" :locale="zhCn" :z-index="3000" :card="{ shadow: 'never' }">
    <RouterView v-if="isBare" />
    <component :is="isAuthLayout ? PublicAuthLayout : PublicLayout" v-else />
  </ElConfigProvider>
</template>
