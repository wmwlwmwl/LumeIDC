<!-- 插件管理：清单 + 运行时启停 + 配置入口。 -->
<template>
  <div class="art-full-height p-4" v-loading="loading">
    <el-card shadow="never">
      <template #header>
        <div class="flex items-center justify-between">
          <span>插件管理</span>
          <el-button size="small" @click="load">刷新</el-button>
        </div>
      </template>
      <el-table :data="plugins" empty-text="暂无插件">
        <el-table-column label="插件" min-width="200">
          <template #default="{ row }">
            <div class="font-medium">{{ row.title }}</div>
            <div class="text-xs opacity-60">{{ row.name }} · v{{ row.version }}</div>
          </template>
        </el-table-column>
        <el-table-column prop="description" label="描述" min-width="280" show-overflow-tooltip />
        <el-table-column label="启用" width="90">
          <template #default="{ row }">
            <el-switch
              :model-value="row.enabled"
              :loading="row._toggling"
              @change="(v: string | number | boolean) => toggle(row, v === true)"
            />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="110">
          <template #default="{ row }">
            <el-button
              v-if="row.hasAdminPage || row.hasConfig"
              size="small"
              :disabled="!row.enabled"
              @click="router.push(`/plugin/${row.name}`)"
              >打开</el-button
            >
          </template>
        </el-table-column>
      </el-table>
      <div class="mt-3 text-xs opacity-60">
        禁用即时生效：事件停止投递、接口不可用、菜单在下次进入后台时隐藏；插件数据保留。
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
  import { onMounted, ref } from 'vue'
  import { useRouter } from 'vue-router'
  import { ElMessage } from 'element-plus'
  import { http } from '@/http/index'

  interface PluginItem {
    name: string
    title: string
    version: string
    description: string
    enabled: boolean
    hasAdminPage: boolean
    hasConfig: boolean
    _toggling?: boolean
  }

  const router = useRouter()
  const loading = ref(false)
  const plugins = ref<PluginItem[]>([])

  async function load() {
    loading.value = true
    try {
      const res = await http.get<{ ok: number; plugins?: PluginItem[] }>('/plugins')
      plugins.value = res.plugins || []
    } catch (err: unknown) {
      ElMessage.error((err as Error).message || '加载插件清单失败')
    } finally {
      loading.value = false
    }
  }

  async function toggle(row: PluginItem, enabled: boolean) {
    row._toggling = true
    try {
      const res = await http.post<{ ok: number; msg?: string }>(`/plugins/${row.name}/toggle`, { enabled })
      if (!res.ok) throw new Error(res.msg || '操作失败')
      row.enabled = enabled
      ElMessage.success(enabled ? `已启用「${row.title}」` : `已禁用「${row.title}」`)
    } catch (err: unknown) {
      ElMessage.error((err as Error).message || '操作失败')
    } finally {
      row._toggling = false
    }
  }

  onMounted(load)
</script>
