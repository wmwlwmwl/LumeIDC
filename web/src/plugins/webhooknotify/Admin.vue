<!-- Webhook 通知插件自定义区：投递日志 + 连通性测试（配置由框架自动表单承载）。 -->
<template>
  <el-card shadow="never" v-loading="testing">
    <template #header>
      <div class="flex items-center justify-between">
        <span>投递日志</span>
        <div>
          <el-button size="small" :loading="testing" @click="sendTest">发送测试</el-button>
          <el-button size="small" @click="loadLogs">刷新</el-button>
        </div>
      </div>
    </template>
    <el-table :data="logs" size="small" empty-text="暂无投递记录">
      <el-table-column prop="createdAt" label="时间" width="170" />
      <el-table-column prop="event" label="事件" width="160" />
      <el-table-column label="结果" width="90">
        <template #default="{ row }">
          <el-tag v-if="row.error" type="danger" size="small">失败</el-tag>
          <el-tag
            v-else-if="row.httpStatus >= 200 && row.httpStatus < 300"
            type="success"
            size="small"
            >{{ row.httpStatus }}</el-tag
          >
          <el-tag v-else type="warning" size="small">{{ row.httpStatus || '-' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="durationMs" label="耗时(ms)" width="100" />
      <el-table-column prop="error" label="错误" show-overflow-tooltip />
    </el-table>
  </el-card>
</template>

<script setup lang="ts">
  import { onMounted, ref } from 'vue'
  import { ElMessage } from 'element-plus'
  import { http } from '@/http/index'

  interface DeliverLog {
    id: number
    event: string
    httpStatus: number
    error: string
    durationMs: number
    createdAt: string
  }

  const testing = ref(false)
  const logs = ref<DeliverLog[]>([])

  async function loadLogs() {
    try {
      const res = await http.get<{ ok: number; logs?: DeliverLog[] }>('/plugin/webhooknotify/logs')
      logs.value = res.logs || []
    } catch {
      // 日志刷新失败静默：表格保留旧数据
    }
  }

  async function sendTest() {
    testing.value = true
    try {
      const res = await http.post<{ ok: number; msg?: string; httpStatus?: number; durationMs?: number }>(
        '/plugin/webhooknotify/test',
        {},
      )
      if (!res.ok) throw new Error(res.msg || '投递失败')
      ElMessage.success(`投递成功（HTTP ${res.httpStatus}，${res.durationMs}ms）`)
      await loadLogs()
    } catch (err: unknown) {
      ElMessage.error((err as Error).message || '投递失败')
    } finally {
      testing.value = false
    }
  }

  onMounted(loadLogs)
</script>
