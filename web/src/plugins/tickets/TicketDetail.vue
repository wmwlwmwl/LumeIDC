<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowLeft, Paperclip, Promotion, Close } from '@element-plus/icons-vue'
import {
  closeTicket,
  fetchTicketDetail,
  reopenTicket,
  replyTicket,
  type TicketAttachment,
  type TicketItem,
  type TicketMessage,
} from '@/api/user'
import { formatDate } from '@/utils/format'

const route = useRoute()
const ticket = ref<TicketItem | null>(null)
const messages = ref<TicketMessage[]>([])
const attachments = ref<TicketAttachment[]>([])
const reply = ref('')
const replyFile = ref<File | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)
const loading = ref(false)
const loadError = ref(false)
const sending = ref(false)
const statusBusy = ref(false)

function statusText(status: string) {
  return status === 'closed' ? '已关闭' : status === 'pending' ? '待处理' : '处理中'
}

function priorityText(priority: string) {
  return priority === 'urgent' ? '非常紧急' : priority === 'high' ? '紧急' : '普通'
}

async function load() {
  loading.value = true
  loadError.value = false
  try {
    const data = await fetchTicketDetail(Number(route.params.id))
    ticket.value = data.ticket
    messages.value = data.messages || []
    attachments.value = data.attachments || []
  } catch (err: unknown) {
    ticket.value = null
    loadError.value = true
    ElMessage.error((err as Error).message || '读取工单失败')
  } finally {
    loading.value = false
  }
}

onMounted(load)

async function send() {
  if (!ticket.value || !reply.value.trim() || sending.value) return
  sending.value = true
  try {
    await replyTicket(ticket.value.id, reply.value.trim(), replyFile.value || undefined)
    reply.value = ''
    replyFile.value = null
    await load()
    ElMessage.success('回复已提交')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '回复失败')
  } finally {
    sending.value = false
  }
}

function pickFile() {
  fileInput.value?.click()
}

function upload(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (file) replyFile.value = file
  input.value = ''
}

function removeFile() {
  replyFile.value = null
}

async function changeStatus() {
  if (!ticket.value) return
  const closed = ticket.value.status === 'closed'
  let reason = ''
  if (closed) { const confirmed = await ElMessageBox.confirm('重新打开后可以继续回复该工单。', '重新打开工单', { type:'info',confirmButtonText:'重新打开',cancelButtonText:'取消' }).catch(() => null); if (!confirmed) return } else { const result = await ElMessageBox.prompt('请填写关闭原因，客户和客服都可以查看。', '关闭工单', { inputPlaceholder:'例如：问题已解决', inputValidator:(value) => value.trim() ? true : '请填写关闭原因', confirmButtonText:'确认关闭',cancelButtonText:'取消' }).catch(() => null); if (!result) return; reason = result.value }
  statusBusy.value = true
  try {
    if (closed) await reopenTicket(ticket.value.id)
    else await closeTicket(ticket.value.id, reason)
    await load()
    ElMessage.success(closed ? '工单已重新打开' : '工单已关闭')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '操作失败')
  } finally {
    statusBusy.value = false
  }
}
</script>

<template>
  <div v-loading="loading" class="ticket-detail-page">
    <div class="ticket-detail-head art-card">
      <RouterLink to="/tickets" class="ticket-back" aria-label="返回工单列表" title="返回工单列表">
        <el-icon><ArrowLeft /></el-icon>
      </RouterLink>
      <div v-if="ticket" class="ticket-detail-title">
        <div class="ticket-name-row">
          <h1>{{ ticket.subject }}</h1>
          <span class="ticket-number">#{{ ticket.id }}</span>
        </div>
        <p>{{ formatDate(ticket.created_at) }} · {{ ticket.service_name || '未关联服务' }}</p>
      </div>
      <el-button
        v-if="ticket"
        class="status-button"
        :type="ticket.status === 'closed' ? 'primary' : 'warning'"
        :loading="statusBusy"
        @click="changeStatus"
      >
        {{ ticket.status === 'closed' ? '重新打开' : '关闭工单' }}
      </el-button>
    </div>

    <template v-if="ticket">
      <section class="ticket-overview art-card">
        <div class="overview-main">
          <div class="tag-row">
            <el-tag :type="ticket.status === 'closed' ? 'info' : ticket.status === 'pending' ? 'warning' : 'success'">{{ statusText(ticket.status) }}</el-tag>
            <el-tag type="info">{{ priorityText(ticket.priority) }}</el-tag>
          </div>
          <p class="ticket-original">{{ ticket.body }}</p>
        </div>
        <dl class="overview-meta">
          <div><dt>问题分类</dt><dd>{{ ticket.category === 'billing' ? '财务账单' : ticket.category === 'account' ? '账户安全' : ticket.category === 'pre-sale' ? '售前咨询' : '技术支持' }}</dd></div>
          <div><dt>关联服务</dt><dd>{{ ticket.service_name || ticket.service_hostname || '未关联' }}</dd></div>
          <div><dt>最后更新</dt><dd>{{ formatDate(ticket.updated_at) }}</dd></div>
        </dl>
        <div v-if="attachments.length" class="attachment-list">
          <span class="section-label">附件</span>
          <a v-for="file in attachments" :key="file.id" :href="file.url" target="_blank" rel="noreferrer"><el-icon><Paperclip /></el-icon>{{ file.name }}</a>
        </div>
      </section>

      <section class="conversation-section art-card">
        <div class="section-heading"><h2>沟通记录</h2><span>{{ messages.length }} 条消息</span></div>
        <div v-if="messages.length" class="conversation">
          <article v-for="message in messages" :key="message.id" class="message" :class="{ 'is-self': !message.admin_id }">
            <div class="message-avatar">{{ message.admin_id ? '客' : '我' }}</div>
            <div class="message-content">
              <div class="message-meta">
                <strong>{{ message.admin_id ? '客服支持' : '我' }}</strong>
                <time>{{ formatDate(message.created_at) }}</time>
              </div>
              <p>{{ message.content }}</p>
            </div>
          </article>
        </div>
        <div v-else class="empty-conversation">暂无回复，客服会尽快处理你的工单。</div>
      </section>

      <section v-if="ticket.status !== 'closed'" class="reply-panel art-card">
        <div class="reply-panel-head"><div><h2>继续沟通</h2><p>补充信息或回复客服，帮助问题更快解决。</p></div><span class="reply-state">工单开放中</span></div>
        <el-input
          v-model="reply"
          type="textarea"
          :rows="5"
          maxlength="10000"
          show-word-limit
          placeholder="输入你的回复内容..."
        />
        <div class="reply-actions">
          <div class="reply-attach">
            <button type="button" class="upload-button" :disabled="sending" @click="pickFile">
              <el-icon><Paperclip /></el-icon>添加附件
            </button>
            <input
              ref="fileInput"
              type="file"
              class="upload-input"
              accept="image/jpeg,image/png,image/gif,application/pdf,text/plain,application/zip"
              @change="upload"
            />
            <span v-if="replyFile" class="reply-file">
              {{ replyFile.name }}
              <button type="button" aria-label="移除附件" @click="removeFile"><el-icon><Close /></el-icon></button>
            </span>
          </div>
          <el-button
            type="primary"
            :icon="Promotion"
            :loading="sending"
            :disabled="!reply.trim()"
            @click="send"
          >
            发送回复
          </el-button>
        </div>
      </section>
      <div v-else class="closed-tip">工单已关闭。需要继续处理时，可以点击右上角重新打开。</div>
    </template>

    <div v-else-if="loadError" class="ticket-state" role="alert">
      <p>工单加载失败，请稍后重试。</p>
      <el-button size="small" @click="load">重新加载</el-button>
    </div>
  </div>
</template>

<style scoped>
.ticket-detail-page {
  padding-bottom: 28px;
}

.ticket-back {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  color: var(--art-gray-500);
  background: var(--art-gray-50);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-md);
  transition: color 0.16s ease, background 0.16s ease, border-color 0.16s ease;
}

.ticket-back:hover {
  color: var(--theme-color);
  background: var(--theme-color-soft);
  border-color: color-mix(in srgb, var(--theme-color) 40%, var(--art-card-border));
}

.ticket-detail-head {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
  padding: 18px 20px;
}

.ticket-detail-title {
  min-width: 0;
  flex: 1;
}

.ticket-name-row {
  display: flex;
  align-items: center;
  gap: 9px;
  min-width: 0;
}

.ticket-name-row h1 {
  margin: 0;
  overflow: hidden;
  color: var(--art-gray-900);
  font-size: 18px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ticket-number {
  flex-shrink: 0;
  color: var(--theme-color);
  font-size: 12px;
  font-weight: 600;
}

.ticket-detail-title p {
  margin: 5px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
}

.status-button {
  flex-shrink: 0;
}

.ticket-overview {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 18px;
  padding: 22px;
}

.tag-row {
  display: flex;
  gap: 8px;
}

.ticket-original {
  margin: 18px 0 0;
  color: var(--art-gray-700);
  line-height: 1.9;
  white-space: pre-wrap;
}

.overview-meta {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 13px;
  margin: 0;
  padding-top: 16px;
  border-top: 1px solid var(--art-card-border);
}

.overview-meta div {
  display: grid;
  gap: 4px;
}

.overview-meta dt {
  color: var(--art-gray-500);
  font-size: 11px;
}

.overview-meta dd {
  margin: 0;
  color: var(--art-gray-700);
  font-size: 13px;
}

.attachment-list {
  grid-column: 1 / -1;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding-top: 15px;
  border-top: 1px solid var(--art-card-border);
}

.section-label {
  margin-right: 4px;
  color: var(--art-gray-500);
  font-size: 11px;
}

.attachment-list a,
.upload-button {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 7px 10px;
  color: var(--theme-color);
  font-size: 12px;
  background: var(--theme-color-soft);
  border: 0;
  border-radius: var(--radius-sm);
  text-decoration: none;
  cursor: pointer;
  transition: background 0.16s ease;
}

.attachment-list a:hover,
.upload-button:hover {
  background: color-mix(in srgb, var(--theme-color) 16%, transparent);
}

.upload-button:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.conversation-section {
  margin-top: 24px;
  padding: 18px 20px 20px;
}

.section-heading {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  margin-bottom: 13px;
}

.section-heading h2 {
  margin: 0;
  color: var(--art-gray-800);
  font-size: 17px;
}

.section-heading > span {
  color: var(--art-gray-500);
  font-size: 11px;
}

.conversation {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.message {
  display: flex;
  align-items: flex-start;
  gap: 11px;
  max-width: 82%;
}

/* 自己的消息靠右，客服消息靠左（与常见 IM 一致） */
.message.is-self {
  align-self: flex-end;
  flex-direction: row-reverse;
}

.message-avatar {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  flex-shrink: 0;
  color: var(--theme-color-contrast);
  font-size: 12px;
  background: var(--el-color-success);
  border-radius: 50%;
}

.message.is-self .message-avatar {
  background: var(--theme-color);
}

.message-content {
  min-width: 0;
  padding: 12px 15px;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: 4px var(--custom-radius) var(--custom-radius) var(--custom-radius);
}

.message.is-self .message-content {
  background: var(--theme-color-soft);
  border: 0;
  border-radius: var(--custom-radius) 4px var(--custom-radius) var(--custom-radius);
}

.message-meta {
  display: flex;
  align-items: center;
  gap: 10px;
}

.message-meta strong {
  color: var(--art-gray-700);
  font-size: 12px;
}

.message-meta time {
  color: var(--art-gray-500);
  font-size: 10px;
}

.message-content p {
  margin: 7px 0 0;
  color: var(--art-gray-600);
  line-height: 1.7;
  white-space: pre-wrap;
}

.empty-conversation,
.closed-tip {
  padding: 24px;
  color: var(--art-gray-500);
  font-size: 13px;
  text-align: center;
  background: var(--art-gray-50);
  border: 1px dashed var(--art-card-border);
  border-radius: var(--radius-md);
}

.reply-panel {
  margin-top: 24px;
  padding: 18px;
}

.reply-panel-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 13px;
}

.reply-panel-head h2 {
  margin: 0;
  color: var(--art-gray-800);
  font-size: 15px;
}

.reply-panel-head p {
  margin: 5px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
}

.reply-state {
  padding: 4px 8px;
  color: var(--el-color-success);
  font-size: 11px;
  background: var(--el-color-success-light-9);
  border-radius: var(--radius-sm);
}

.reply-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 11px;
}

.reply-attach {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  min-width: 0;
}

.upload-input {
  display: none;
}

.reply-file {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 240px;
  padding: 6px 8px 6px 10px;
  overflow: hidden;
  color: var(--art-gray-700);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
  background: var(--art-gray-100);
  border-radius: var(--radius-sm);
}

.reply-file button {
  display: inline-flex;
  color: var(--art-gray-500);
  background: transparent;
  border: 0;
  cursor: pointer;
}

.reply-file button:hover {
  color: var(--el-color-danger);
}

.closed-tip {
  margin-top: 24px;
}

.ticket-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 56px 20px;
  text-align: center;
}

.ticket-state p {
  margin: 0;
  color: var(--art-gray-600);
  font-size: 14px;
}

@media (max-width: 680px) {
  .ticket-detail-head {
    align-items: flex-start;
    flex-wrap: wrap;
    gap: 8px;
    min-height: 0;
    padding: 15px;
  }

  .ticket-detail-title {
    flex-basis: 100%;
  }

  .status-button {
    margin-left: auto;
  }

  .ticket-overview {
    display: block;
    padding: 16px;
  }

  .overview-meta {
    grid-template-columns: 1fr 1fr;
    gap: 10px;
    margin-top: 18px;
    padding: 15px 0 0;
    border-top: 1px solid var(--art-card-border);
  }

  .conversation-section {
    padding: 15px;
  }

  .attachment-list {
    margin-top: 16px;
  }

  .message {
    max-width: 94%;
  }

  .reply-actions {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
