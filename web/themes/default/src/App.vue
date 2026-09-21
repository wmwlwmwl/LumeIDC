<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeMount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElConfigProvider } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import PublicLayout from './components/layout/PublicLayout.vue'
import { installPublicTheme } from '@/components/public/usePublicTheme'
import { http } from '@/http/index'

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

// ---- 插件前台内容注入（GET /plugins/injections；统计代码/客服悬浮窗等）----
// 安全约定：内容为站点主自装插件代码（编译期信任），不过滤。
const bodyInjections = ref<string[]>([])

function injectToHead(html: string) {
  // 解析片段并克隆可执行节点：innerHTML 产生的 <script> 不会执行，需重建。
  const tpl = document.createElement('template')
  tpl.innerHTML = html
  tpl.content.childNodes.forEach((node) => {
    if (node.nodeType !== Node.ELEMENT_NODE) return
    const el = node as Element
    if (el.tagName === 'SCRIPT') {
      const s = document.createElement('script')
      for (const attr of Array.from(el.attributes)) s.setAttribute(attr.name, attr.value)
      s.textContent = el.textContent || ''
      document.head.appendChild(s)
    } else if (['STYLE', 'LINK', 'META', 'TITLE'].includes(el.tagName)) {
      document.head.appendChild(el.cloneNode(true))
    }
  })
}

onMounted(async () => {
  try {
    const res = await http.get<{ ok: number; injections?: { position: string; html: string }[] }>(
      '/plugins/injections',
    )
    for (const inj of res.injections || []) {
      if (inj.position === 'head') injectToHead(inj.html)
      else if (inj.position === 'body_bottom') bodyInjections.value.push(inj.html)
    }
  } catch {
    // 注入是增强，失败静默
  }
})
</script>

<template>
  <ElConfigProvider size="default" :locale="zhCn" :z-index="3000" :card="{ shadow: 'never' }">
    <RouterView v-if="isBare" />
    <component :is="isAuthLayout ? PublicAuthLayout : PublicLayout" v-else />
    <!-- 插件 body_bottom 注入（v-html 内容来自站点主自装插件，受信任） -->
    <div v-for="(html, i) in bodyInjections" :key="i" v-html="html" style="display: contents" />
  </ElConfigProvider>
</template>
