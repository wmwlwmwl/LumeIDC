<!-- 前台模板：卡片网格 + 一键切换。模板来自内置 themes 目录，切换即时生效。 -->
<template>
  <div class="art-full-height p-4" v-loading="loading">
    <el-card shadow="never">
      <template #header>
        <div class="flex items-center justify-between">
          <span>前台模板</span>
          <el-button size="small" @click="load">刷新</el-button>
        </div>
      </template>
      <el-empty v-if="!themes.length" description="暂无可用模板" />
      <div v-else class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <div
          v-for="t in themes"
          :key="t.key"
          class="theme-card overflow-hidden rounded-lg transition-shadow hover:shadow-md"
          :class="{ active: t.active }"
        >
          <div class="preview-box relative aspect-[4/3]">
            <img
              :src="previewUrl(t.key)"
              :alt="t.name"
              class="h-full w-full object-cover"
              loading="lazy"
              @error="onPreviewError"
            />
            <el-tag v-if="t.active" type="success" size="small" class="absolute right-2 top-2">
              使用中
            </el-tag>
          </div>
          <div class="p-3">
            <div class="flex items-center gap-2">
              <span class="font-medium">{{ t.name }}</span>
              <span v-if="t.version" class="text-xs opacity-60">v{{ t.version }}</span>
            </div>
            <div class="mt-1 line-clamp-2 min-h-5 text-xs opacity-60">{{ t.description || t.key }}</div>
            <div class="mt-1 text-xs opacity-40">{{ t.author || '未知作者' }}</div>
            <div class="mt-3 flex justify-end">
              <el-button v-if="t.active" size="small" disabled>当前模板</el-button>
              <el-button
                v-else
                size="small"
                type="primary"
                :loading="switching === t.key"
                @click="apply(t)"
                >启用</el-button
              >
            </div>
          </div>
        </div>
      </div>
      <div class="mt-3 text-xs opacity-60">切换即时生效，用户刷新前台页面后使用新模板。</div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
  import { onMounted, ref } from 'vue'
  import { ElMessage, ElMessageBox } from 'element-plus'
  import { fetchThemes, switchTheme, type ThemeInfo } from '@/admin/api'
  import { useSession } from '@/http/session'

  const session = useSession()
  const loading = ref(false)
  const switching = ref('')
  const themes = ref<ThemeInfo[]>([])

  // 预览图经后台接口下发，需拼当前后台基址（自定义路径场景）
  function previewUrl(key: string): string {
    const base = (session.adminPath || '/admin').replace(/\/$/, '')
    return `${base}/themes/${key}/preview`
  }

  // 无预览图的模板：用首屏渐变占位代替裂图
  function onPreviewError(e: Event) {
    const img = e.target as HTMLImageElement
    img.style.display = 'none'
  }

  async function load() {
    loading.value = true
    try {
      themes.value = await fetchThemes()
    } catch (err: unknown) {
      ElMessage.error((err as Error).message || '加载模板列表失败')
    } finally {
      loading.value = false
    }
  }

  async function apply(t: ThemeInfo) {
    try {
      await ElMessageBox.confirm(`确定将前台模板切换为「${t.name}」吗？`, '切换模板', {
        type: 'warning',
        confirmButtonText: '切换',
        cancelButtonText: '取消',
      })
    } catch {
      return
    }
    switching.value = t.key
    try {
      await switchTheme(t.key)
      themes.value.forEach((x) => (x.active = x.key === t.key))
      ElMessage.success(`已启用「${t.name}」`)
    } catch (err: unknown) {
      ElMessage.error((err as Error).message || '切换失败')
    } finally {
      switching.value = ''
    }
  }

  onMounted(load)
</script>

<style scoped>
  .theme-card { border: 1px solid var(--el-border-color); }
  .theme-card.active { border-color: var(--el-color-primary); }
  .preview-box { background: var(--el-fill-color-light); }
</style>
