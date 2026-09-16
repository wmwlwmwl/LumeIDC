<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ApiError } from '../http/index'
import {
  fetchEmailTemplates, saveEmailTemplate, resetEmailTemplate, previewEmailTemplate,
  testEmailTemplate, setEmailMasterEnabled, emailPreviewDocument,
  type EmailTemplate, type EmailTemplateDraft,
} from '../admin/emailTemplates'

const list = ref<EmailTemplate[]>([])
const loading = ref(false)
const loadError = ref('')
const emailEnabled = ref(false)
const masterSaving = ref(false)
const category = ref('')
const selected = ref<EmailTemplate | null>(null)
const draft = ref<EmailTemplateDraft>({ code: '', subject: '', body: '', enabled: true })
const baseline = ref('')
const dirty = computed(() => !!selected.value && JSON.stringify(draft.value) !== baseline.value)
const busy = ref('')
const previewSubject = ref('')
const previewDocument = ref('')
const testTo = ref('')
const subjectInput = ref<{ input?: HTMLInputElement }>()
const bodyInput = ref<{ textarea?: HTMLTextAreaElement }>()
const insertTarget = ref<'subject' | 'body'>('body')
const categories = computed(() => [...new Set(list.value.map(item => item.category))])
const filtered = computed(() => list.value.filter(item => !category.value || item.category === category.value))

function showError(error: unknown, fallback: string) {
  // 代理或邮件底层异常不向界面透传原始错误。
  ElMessage.error(error instanceof ApiError && /[\u4e00-\u9fff]/.test(error.message) ? error.message : fallback)
}
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const data = await fetchEmailTemplates()
    list.value = data.list
    emailEnabled.value = data.emailEnabled
  }
  catch { loadError.value = '读取邮件模板失败，请重试' }
  finally { loading.value = false }
}
// 总开关独立于草稿：切换不校验未保存状态，失败时回滚开关显示。
async function toggleMaster(next: boolean) {
  if (masterSaving.value) return
  masterSaving.value = true
  try {
    await setEmailMasterEnabled(next)
    ElMessage.success(next ? '邮件通知总开关已开启' : '邮件通知总开关已关闭，业务邮件不再发送')
  } catch (error) {
    emailEnabled.value = !next
    showError(error, '保存邮件通知总开关失败，已恢复原状态')
  } finally { masterSaving.value = false }
}
function edit(item: EmailTemplate) {
  selected.value = item
  draft.value = { code: item.code, subject: item.subject, body: item.body, enabled: item.enabled }
  baseline.value = JSON.stringify(draft.value)
  previewDocument.value = ''
}
async function mayLeave(): Promise<boolean> {
  if (busy.value) {
    ElMessage.warning('操作进行中，请稍候')
    return false
  }
  if (!dirty.value) return true
  try {
    await ElMessageBox.confirm('当前模板有未保存修改。离开将丢弃这些修改，是否继续？', '未保存的草稿', {
      type: 'warning', confirmButtonText: '丢弃并继续', cancelButtonText: '继续编辑',
    })
    return true
  } catch { return false }
}
async function select(item: EmailTemplate) {
  if (item.code === selected.value?.code || !(await mayLeave())) return
  edit(item)
}
async function closeEditor() {
  if (await mayLeave()) selected.value = null
}
onBeforeRouteLeave(mayLeave)
function beforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value || busy.value) { event.preventDefault(); event.returnValue = '' }
}
onMounted(() => { window.addEventListener('beforeunload', beforeUnload); void load() })
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
watch(draft, () => { previewDocument.value = '' }, { deep: true })

async function insertVariable(key: string) {
  const target = insertTarget.value
  const input = target === 'subject' ? subjectInput.value?.input : bodyInput.value?.textarea
  const value = draft.value[target]
  const start = input?.selectionStart ?? value.length
  const end = input?.selectionEnd ?? start
  const token = '{{' + key + '}}'
  draft.value[target] = value.slice(0, start) + token + value.slice(end)
  await nextTick()
  input?.focus()
  input?.setSelectionRange(start + token.length, start + token.length)
}
async function save() {
  if (busy.value) return
  busy.value = 'save'
  try {
    await saveEmailTemplate(draft.value)
    Object.assign(selected.value!, draft.value, { custom: true })
    baseline.value = JSON.stringify(draft.value)
    ElMessage.success('邮件模板已保存')
  } catch (error) { showError(error, '保存失败，当前草稿已保留，请稍后重试') }
  finally { busy.value = '' }
}
async function preview() {
  if (busy.value) return
  busy.value = 'preview'
  previewDocument.value = ''
  try {
    const result = await previewEmailTemplate(draft.value)
    previewSubject.value = result.subject
    previewDocument.value = emailPreviewDocument(result.body)
  } catch (error) { showError(error, '预览失败，请检查模板格式及变量') }
  finally { busy.value = '' }
}
async function sendTest() {
  if (busy.value) return
  if (!testTo.value.trim()) { ElMessage.warning('请输入一个测试收件邮箱'); return }
  busy.value = 'test'
  try {
    await testEmailTemplate(testTo.value.trim(), draft.value)
    ElMessage.success('当前草稿测试邮件已发送，模板尚未保存')
  } catch (error) { showError(error, '测试发送失败，请检查邮箱、模板和邮件通道配置') }
  finally { busy.value = '' }
}
async function reset() {
  if (busy.value || !selected.value) return
  try {
    await ElMessageBox.confirm('恢复默认会立即覆盖服务器上的自定义标题、正文及启用状态，并丢弃当前未保存草稿。此操作不能撤销，是否继续？', '恢复默认模板', {
      type: 'warning', confirmButtonText: '确认恢复默认', cancelButtonText: '取消',
    })
  } catch { return }
  busy.value = 'reset'
  let resetDone = false
  try {
    const code = draft.value.code
    await resetEmailTemplate(code)
    resetDone = true
    const updated = await fetchEmailTemplates()
    const item = updated.list.find(entry => entry.code === code)
    if (!item) throw new Error('未找到模板')
    list.value = updated.list
    emailEnabled.value = updated.emailEnabled
    edit(item)
    ElMessage.success('邮件模板已恢复默认')
  } catch (error) {
    if (resetDone) ElMessage.warning('服务器已恢复默认，但读取失败；当前本地草稿已保留，请关闭编辑后刷新列表查看')
    else showError(error, '恢复默认失败，当前草稿已保留')
  } finally { busy.value = '' }
}
</script>

<template>
  <div class="art-full-height email-templates">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title"><h4>邮件模板</h4><p>编辑固定业务邮件。预览和测试使用当前草稿及示例变量，不会自动保存。</p></div>
        </div>
      </template>
      <div class="template-toolbar">
        <el-select v-model="category" clearable placeholder="全部分类" aria-label="按邮件模板分类筛选">
          <el-option v-for="item in categories" :key="item" :label="item" :value="item" />
        </el-select>
        <el-button :loading="loading" :disabled="!!selected || !!busy" @click="load">刷新列表</el-button>
        <router-link :to="{ name: 'admin-notify-settings' }">配置邮件发送通道</router-link>
      </div>
      <el-alert v-if="loadError" :title="loadError" type="error" :closable="false" role="alert" />
      <div class="template-master">
        <div>
          <strong>邮件通知总开关</strong>
          <p class="template-hint">关闭后所有业务邮件都不再发送，站内信不受影响；验证码和短信也不受此开关影响。只想停用某一类邮件，请用左侧对应模板的启用开关。</p>
        </div>
        <el-switch v-model="emailEnabled" :loading="masterSaving" active-text="开启" inactive-text="关闭"
          aria-label="邮件通知总开关" @change="toggleMaster(emailEnabled)" />
      </div>
      <div class="template-layout" v-loading="loading">
        <nav class="template-list" aria-label="业务邮件模板">
          <button v-for="item in filtered" :key="item.code" type="button" class="template-choice"
            :class="{ active: selected?.code === item.code }" :aria-pressed="selected?.code === item.code"
            :disabled="!!busy" @click="select(item)">
            <strong>{{ item.name }}</strong>
            <span>{{ item.category }} · {{ item.enabled ? '已启用' : '已停用' }} · {{ item.custom ? '自定义' : '默认' }}</span>
            <span v-if="item.required">必要邮件，不可停用</span>
          </button>
          <p v-if="!loading && !filtered.length">暂无符合条件的模板</p>
        </nav>
        <section v-if="selected" class="template-editor" aria-label="邮件模板编辑器" :aria-busy="!!busy">
          <div class="template-toolbar">
            <h3>{{ selected.name }}</h3>
            <el-tag v-if="dirty" type="warning" role="status">有未保存修改</el-tag>
            <el-button :disabled="!!busy" @click="closeEditor">关闭编辑</el-button>
          </div>
          <el-form label-position="top" :disabled="!!busy" @submit.prevent="save">
            <el-form-item label="启用邮件通知">
              <el-switch v-model="draft.enabled" :disabled="selected.required" aria-label="启用该邮件模板" active-text="启用" inactive-text="停用" />
              <p v-if="selected.required" class="template-hint">验证码等必要邮件不能停用。</p>
            </el-form-item>
            <el-form-item label="邮件标题" required>
              <el-input ref="subjectInput" v-model="draft.subject" aria-label="邮件标题" @focus="insertTarget = 'subject'" />
            </el-form-item>
            <el-form-item label="邮件正文（HTML 源码）" required>
              <el-input ref="bodyInput" v-model="draft.body" type="textarea" :rows="16" aria-label="邮件正文HTML源码" spellcheck="false" @focus="insertTarget = 'body'" />
            </el-form-item>
            <div class="template-variables" aria-label="可用模板变量">
              <p>可用变量（点击插入到最近编辑的标题或正文光标处）：</p>
              <el-button v-for="variable in selected.variables" :key="variable.key" class="variable-button"
                :aria-label="'插入' + variable.label + '，示例：' + variable.example" @click="insertVariable(variable.key)">
                <span>{{ variable.label }}<code v-text="'{{' + variable.key + '}}'" /><small>示例：{{ variable.example }}</small></span>
              </el-button>
            </div>
            <div class="template-toolbar">
              <el-button type="primary" :loading="busy === 'save'" @click="save">保存模板</el-button>
              <el-button :loading="busy === 'preview'" @click="preview">预览当前草稿</el-button>
              <el-button type="danger" plain :loading="busy === 'reset'" @click="reset">恢复默认</el-button>
            </div>
            <el-form-item label="测试收件邮箱（单个地址）">
              <div class="template-test">
                <el-input v-model="testTo" type="email" autocomplete="email" aria-label="测试收件邮箱" placeholder="请输入一个收件邮箱" />
                <el-button :loading="busy === 'test'" @click="sendTest">发送当前草稿测试</el-button>
              </div>
            </el-form-item>
            <p class="template-hint">测试会真实发送示例邮件，不修改保存内容。前5次后每15分钟允许1次，与通道测试共享额度。</p>
          </el-form>
          <section v-if="previewDocument" class="template-preview" aria-label="安全邮件预览">
            <h4>标题：{{ previewSubject }}</h4>
            <p class="template-hint">安全预览已禁用脚本、外部资源、链接和表单；仅供排版参考，实际邮箱显示可能不同。</p>
            <iframe title="邮件正文安全预览" sandbox="" referrerpolicy="no-referrer" :srcdoc="previewDocument" />
          </section>
        </section>
        <el-empty v-else description="请选择一个业务模板进行编辑" />
      </div>
    </ElCard>
  </div>
</template>

<style scoped>
.template-toolbar { display: flex; flex-wrap: wrap; gap: 12px; align-items: center; margin-bottom: 16px; }
.template-toolbar .el-select { width: 220px; max-width: 100%; }
.template-layout { display: grid; grid-template-columns: minmax(180px, 240px) minmax(0, 1fr); gap: 24px; }
.template-list { display: flex; flex-direction: column; gap: 8px; }
.template-choice { text-align: left; padding: 12px; border: 1px solid var(--el-border-color); border-radius: 8px; background: var(--el-bg-color); color: var(--el-text-color-primary); cursor: pointer; overflow-wrap: anywhere; }
.template-choice span { display: block; font-size: 12px; margin-top: 6px; }
.template-choice.active { border-color: var(--el-color-primary); background: var(--el-color-primary-light-9); }
.template-choice:focus-visible { outline: 2px solid var(--el-color-primary); outline-offset: 2px; }
.template-choice:disabled { cursor: wait; opacity: .65; }
.template-editor { min-width: 0; }
.template-master { display: flex; flex-wrap: wrap; gap: 12px; align-items: center; justify-content: space-between; margin-bottom: 16px; padding: 12px 16px; border: 1px solid var(--el-border-color); border-radius: 8px; }
.template-master > div { flex: 1 1 320px; min-width: 0; }
.template-master .template-hint { margin: 4px 0 0; }
.template-hint { font-size: 12px; color: var(--el-text-color-secondary); width: 100%; }
.template-variables { display: flex; flex-wrap: wrap; align-items: stretch; gap: 8px; margin-bottom: 16px; }
.template-variables > p { width: 100%; margin: 0; }
/* 全局默认按钮使用固定高度，此处多行变量卡片需按内容撑开。 */
.template-variables .variable-button { height: auto !important; min-width: 0; max-width: 100%; margin: 0; padding: 10px; white-space: normal; text-align: left; line-height: 1.5; }
.variable-button :deep(> span) { min-width: 0; max-width: 100%; overflow-wrap: anywhere; }
.variable-button code, .variable-button small { display: block; margin-top: 4px; overflow-wrap: anywhere; }
.template-test { display: flex; flex-wrap: wrap; gap: 8px; width: 100%; }
.template-test .el-input { flex: 1 1 200px; }
.template-preview { margin-top: 20px; overflow-wrap: anywhere; }
.template-preview iframe { display: block; width: 100%; height: 450px; border: 1px solid var(--el-border-color); background: white; }
.template-editor :deep(textarea) { font-family: monospace; }
@media (max-width: 768px) {
  .template-layout { grid-template-columns: minmax(0, 1fr); }
  .template-list { max-height: 260px; overflow-y: auto; }
  .template-toolbar .el-button + .el-button { margin-left: 0; }
}
</style>
