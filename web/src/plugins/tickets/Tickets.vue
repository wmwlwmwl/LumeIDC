<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { ChatDotRound, Plus } from '@element-plus/icons-vue'
import { createTicket, fetchServices, fetchTickets, type ServiceLite, type TicketItem } from '@/api/user'
import { formatDate } from '@/utils/format'
import PublicPageHead from '@/components/public/PublicPageHead.vue'

const list = ref<TicketItem[]>([])
const services = ref<ServiceLite[]>([])
const dialog = ref(false)
const loading = ref(false)
const loadError = ref(false)
const submitting = ref(false)
const q = ref('')
const status = ref('')
const category = ref('')
const page = ref(1)
const total = ref(0)
const form = ref({ subject: '', body: '', priority: 'normal', category: 'technical', service_id: 0 })

const STATUS_META: Record<string, { label: string; type: 'info' | 'success' | 'warning' | 'danger' }> = {
  pending: { label: '待处理', type: 'warning' },
  processing: { label: '处理中', type: 'success' },
  closed: { label: '已关闭', type: 'info' },
}
const PRIORITY_LABEL: Record<string, string> = { normal: '普通', high: '紧急', urgent: '非常紧急' }

function statusMeta(s: string) {
  return STATUS_META[s] || { label: s || '未知', type: 'info' as const }
}

async function load() {
  loading.value = true
  loadError.value = false
  try {
    const data = await fetchTickets({
      q: q.value.trim(),
      status: status.value,
      category: category.value,
      page: page.value,
      limit: 10,
    })
    list.value = data.list
    total.value = data.total
  } catch (err: unknown) {
    loadError.value = true
    ElMessage.error((err as Error).message || '读取工单失败')
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  await Promise.all([load(), loadServices()])
})

async function loadServices() {
  try {
    services.value = await fetchServices()
  } catch {
    services.value = []
  }
}

function resetFilters() {
  page.value = 1
  q.value = ''
  status.value = ''
  category.value = ''
  load()
}

async function submit() {
  if (!form.value.subject.trim() || !form.value.body.trim()) {
    ElMessage.warning('请填写工单主题和问题描述')
    return
  }
  submitting.value = true
  try {
    await createTicket({ ...form.value, subject: form.value.subject.trim(), body: form.value.body.trim() })
    ElMessage.success('工单已提交')
    form.value = { subject: '', body: '', priority: 'normal', category: 'technical', service_id: 0 }
    dialog.value = false
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '提交失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div>
    <PublicPageHead title="工单支持" subtitle="遇到问题？提交工单，客服会尽快跟进。">
      <template #extra>
        <el-button type="primary" :icon="Plus" @click="dialog = true">提交工单</el-button>
      </template>
    </PublicPageHead>

    <div class="art-card ticket-filters">
      <el-input
        v-model="q"
        aria-label="搜索工单"
        clearable
        placeholder="搜索工单主题或内容"
        @keyup.enter="page = 1; load()"
      />
      <el-select v-model="status" aria-label="工单状态" clearable placeholder="全部状态" @change="page = 1; load()">
        <el-option label="待处理" value="pending" />
        <el-option label="处理中" value="processing" />
        <el-option label="已关闭" value="closed" />
      </el-select>
      <el-select v-model="category" aria-label="工单分类" clearable placeholder="全部分类" @change="page = 1; load()">
        <el-option label="技术支持" value="technical" />
        <el-option label="财务账单" value="billing" />
        <el-option label="账户安全" value="account" />
        <el-option label="售前咨询" value="pre-sale" />
      </el-select>
      <el-button @click="resetFilters">重置</el-button>
    </div>

    <div v-loading="loading" class="ticket-list">
      <RouterLink v-for="ticket in list" :key="ticket.id" :to="`/tickets/${ticket.id}`" class="art-card ticket-item">
        <div class="ticket-icon"><el-icon><ChatDotRound /></el-icon></div>
        <div class="ticket-copy">
          <div class="ticket-copy__head">
            <strong>{{ ticket.subject }}</strong>
            <span
              v-if="ticket.priority && ticket.priority !== 'normal'"
              class="ticket-priority"
              :class="`is-${ticket.priority}`"
            >
              {{ PRIORITY_LABEL[ticket.priority] || ticket.priority }}
            </span>
          </div>
          <p>{{ ticket.body }}</p>
          <small>
            #{{ ticket.id }} · {{ formatDate(ticket.created_at) }}
            <template v-if="ticket.service_id">
              · 关联服务：{{ ticket.service_name || ticket.service_hostname || `服务 #${ticket.service_id}` }}
            </template>
          </small>
        </div>
        <el-tag :type="statusMeta(ticket.status).type">{{ statusMeta(ticket.status).label }}</el-tag>
      </RouterLink>

      <div v-if="loadError" class="ticket-state" role="alert">
        <p>工单列表加载失败，请稍后重试。</p>
        <el-button size="small" @click="load">重新加载</el-button>
      </div>
      <el-empty v-else-if="!list.length && !loading" description="还没有工单" />
    </div>

    <div v-if="total > 10" class="ticket-pager">
      <el-pagination
        background
        layout="total, prev, pager, next"
        :total="total"
        :page-size="10"
        :current-page="page"
        @current-change="(value: number) => { page = value; load() }"
      />
    </div>

    <el-dialog v-model="dialog" title="提交工单" width="520px">
      <el-form label-position="top">
        <el-form-item label="主题">
          <el-input v-model="form.subject" maxlength="160" placeholder="例如：服务器无法连接" />
        </el-form-item>
        <el-form-item label="优先级">
          <el-select v-model="form.priority" style="width: 100%">
            <el-option label="普通" value="normal" />
            <el-option label="紧急" value="high" />
            <el-option label="非常紧急" value="urgent" />
          </el-select>
        </el-form-item>
        <el-form-item label="问题分类">
          <el-select v-model="form.category" style="width: 100%">
            <el-option label="技术支持" value="technical" />
            <el-option label="财务账单" value="billing" />
            <el-option label="账户安全" value="account" />
            <el-option label="售前咨询" value="pre-sale" />
          </el-select>
        </el-form-item>
        <el-form-item label="关联服务">
          <el-select v-model="form.service_id" clearable style="width: 100%" placeholder="可选，不关联服务">
            <el-option label="不关联服务" :value="0" />
            <el-option
              v-for="service in services"
              :key="service.id"
              :label="service.name || service.hostname || `服务 #${service.id}`"
              :value="service.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="问题描述">
          <el-input
            v-model="form.body"
            type="textarea"
            :rows="6"
            maxlength="10000"
            show-word-limit
            placeholder="请尽量描述问题现象、发生时间和相关服务。"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">提交工单</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.ticket-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ticket-filters {
  display: flex;
  gap: 9px;
  margin-bottom: 14px;
  padding: 12px 14px;
}

.ticket-filters .el-input {
  width: 280px;
}

.ticket-filters .el-select {
  width: 145px;
}

.ticket-pager {
  display: flex;
  justify-content: center;
  margin-top: 18px;
}

.ticket-item {
  display: flex;
  align-items: flex-start;
  gap: 13px;
  padding: 16px;
  cursor: pointer;
  border-radius: var(--radius-lg);
  transition: border-color 0.16s ease, box-shadow 0.16s ease, transform 0.16s ease;
}

.ticket-item:hover {
  transform: translateY(-2px);
  border-color: color-mix(in srgb, var(--theme-color) 32%, var(--art-card-border));
  box-shadow: 0 12px 28px color-mix(in srgb, var(--art-gray-900) 8%, transparent);
}

.ticket-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  flex-shrink: 0;
  color: var(--theme-color);
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
}

.ticket-copy {
  min-width: 0;
  flex: 1;
}

.ticket-copy__head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.ticket-copy strong {
  overflow: hidden;
  color: var(--art-gray-800);
  font-size: 14px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ticket-priority {
  flex-shrink: 0;
  padding: 1px 7px;
  font-size: 11px;
  border-radius: var(--radius-sm);
}

.ticket-priority.is-high {
  color: var(--el-color-warning);
  background: var(--el-color-warning-light-9);
}

.ticket-priority.is-urgent {
  color: var(--el-color-danger);
  background: var(--el-color-danger-light-9);
}

.ticket-copy p {
  margin: 6px 0;
  overflow: hidden;
  color: var(--art-gray-500);
  font-size: 12px;
  line-height: 1.6;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
}

.ticket-copy small {
  color: var(--art-gray-500);
  font-size: 11px;
}

.ticket-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 44px 20px;
  text-align: center;
}

.ticket-state p {
  margin: 0;
  color: var(--art-gray-600);
  font-size: 14px;
}

@media (max-width: 700px) {
  .ticket-filters {
    flex-wrap: wrap;
  }

  .ticket-filters .el-input,
  .ticket-filters .el-select {
    width: 100%;
  }
}
</style>
