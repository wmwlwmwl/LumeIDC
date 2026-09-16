<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { fetchAdminSettings } from '../admin/api'
import {
  fetchSMSTemplates, fetchSMSScenes, fetchSMSDeliveries, saveSMSTemplate, deleteSMSTemplate,
  saveSMSBinding, previewSMSTemplate, canBindSMS, smsParameterMap, smsProviders, smsStatuses,
  fetchSMSProviders, remoteSMSTemplate, smsTemplateDTO, smsSupports, smsRemoteSupported, smsRanges, smsAuditStatuses, smsRemoteActions, unlockSMSTemplate,
  type SMSProviderDescriptor, type SMSRemoteAction,
  type SMSTemplate, type SMSScene, type SMSBinding, type SMSPreview, type SMSDelivery,
} from '../admin/smsTemplates'

const providers = ref<SMSProviderDescriptor[]>([])
const descriptor = computed(() => providers.value.find(item => item.key === draft.value?.provider))
const textOnly = computed(() => ['stay33', 'smsbao'].includes(draft.value?.provider || ''))
const canInsert = computed(() => !!draft.value && !['aliyun', 'aliyun_sms', 'qcloudsms'].includes(draft.value.provider))
const remoteStale = ref(false)
// 后端持久锁：进程中断或写回失败后保留，需管理员核对控制台再手动解除。
const locked = computed(() => !!draft.value?.remote_operation)
const templates = ref<SMSTemplate[]>([])
const scenes = ref<SMSScene[]>([])
const bindings = ref<SMSBinding[]>([])
const deliveries = ref<SMSDelivery[]>([])
const routes = ref<Partial<Record<string, { provider: string }>>>({})
const ready = ref(false)
const loading = ref(false)
const loadError = ref('')
const deliveryError = ref('')
const busy = ref('')
const tab = ref('templates')
const draft = ref<SMSTemplate | null>(null)
const rows = ref<{ name: string; variable: string }[]>([])
const baseline = ref('')
const sceneCode = ref('')
const previewResult = ref<SMSPreview | null>(null)
const bodyInput = ref<{ textarea?: HTMLTextAreaElement }>()
const editorSnapshot = () => JSON.stringify({ draft: draft.value, rows: rows.value })
const editorDirty = computed(() => !!draft.value && editorSnapshot() !== baseline.value)
const bindingDirty = (binding: SMSBinding) => {
  const original = scenes.value.find(scene => scene.code === binding.code)
  return !!original && (original.template_id !== binding.template_id || original.enabled !== binding.enabled)
}
const dirty = computed(() => editorDirty.value || bindings.value.some(bindingDirty))
const previewScenes = computed(() => scenes.value.filter(scene => scene.kind === draft.value?.kind))
const currentScene = computed(() => previewScenes.value.find(scene => scene.code === sceneCode.value))
const variables = computed(() => currentScene.value?.variables || [])

async function confirm(message: string, title: string, action = '确认继续'): Promise<boolean> {
  try {
    await ElMessageBox.confirm(message, title, { type: 'warning', confirmButtonText: action, cancelButtonText: '取消' })
    return true
  } catch { return false }
}
async function mayLeave(): Promise<boolean> {
  if (busy.value || loading.value) { ElMessage.warning('操作进行中，请稍候'); return false }
  return !dirty.value || await confirm('有未保存的模板或场景绑定，离开将丢弃这些修改。是否继续？', '未保存的草稿', '丢弃并继续')
}
function resetBindings() {
  bindings.value = scenes.value.map(({ code, template_id, enabled }) => ({ code, template_id, enabled }))
}
async function changeTab(value: string) {
  if (value === tab.value || !await mayLeave()) return
  draft.value = null
  resetBindings()
  tab.value = value
  if (value === 'deliveries') await loadDeliveries()
}
async function load() {
  if (!await mayLeave()) return
  loading.value = true
  loadError.value = ''
  try {
    const [list, sceneList, settings, descriptors] = await Promise.all([fetchSMSTemplates(), fetchSMSScenes(), fetchAdminSettings(), fetchSMSProviders()])
    providers.value = descriptors
    remoteStale.value = false
    templates.value = list
    scenes.value = sceneList
    try { routes.value = JSON.parse(settings.sms_routes || '{}') as Partial<Record<string, { provider: string }>> } catch { routes.value = {} }
    draft.value = null
    resetBindings()
    ready.value = true
  } catch { loadError.value = '读取短信模板、场景或通道失败，当前草稿已保留，请重试' }
  finally { loading.value = false }
}
async function loadDeliveries() {
  if (busy.value) return
  busy.value = 'deliveries'
  deliveryError.value = ''
  try { deliveries.value = await fetchSMSDeliveries() }
  catch { deliveryError.value = '读取投递记录失败，请重试；下方可能是上次读取的数据' }
  finally { busy.value = '' }
}
function edit(item: SMSTemplate) {
  draft.value = { ...item, range_type: item.range_type || 'cn', remark: item.remark || '', sign_name: item.sign_name || '', parameters: { ...item.parameters } }
  rows.value = Object.entries(item.parameters || {}).map(([name, variable]) => ({ name, variable }))
  sceneCode.value = scenes.value.find(scene => scene.kind === item.kind)?.code || ''
  previewResult.value = null
  baseline.value = item.id ? editorSnapshot() : ''
}
async function select(item: SMSTemplate) {
  if (!await mayLeave()) return
  edit(item)
}
async function create() {
  if (!await mayLeave()) return
  const selected = providers.value.find(item => item.key === routes.value.cn?.provider) || providers.value[0]
  if (!selected) { ElMessage.warning('未读取到短信服务商能力，请刷新'); return }
  edit({ id: 0, name: '', provider: selected.key, kind: selected.capabilities.otp ? 'otp' : 'notification', range_type: selected.capabilities.ranges[0], template_code: '', content: '', parameters: {}, enabled: true })
}
async function closeEditor() {
  if (await mayLeave()) draft.value = null
}
async function changeProvider(value: string) {
  if (!draft.value || value === draft.value.provider || busy.value) return
  if (!await confirm('切换模板服务商会清空当前模板编码、正文及映射，以免沿用不兼容内容。其他服务商模板可存档，但只有当前通道可绑定生效。', '切换模板服务商')) return
  draft.value.provider = value
  draft.value.template_code = ''
  draft.value.content = ''
  draft.value.parameters = {}
  rows.value = []
  const selected = providers.value.find(item => item.key === value)
  draft.value.range_type = selected?.capabilities.ranges[0]
  draft.value.kind = selected?.capabilities.otp ? 'otp' : 'notification'
}
watch(() => draft.value?.kind, () => { sceneCode.value = previewScenes.value[0]?.code || '' })
watch([draft, rows, sceneCode], () => { previewResult.value = null }, { deep: true })
function templateInput(): SMSTemplate | null {
  if (!draft.value) return null
  if (!draft.value.name.trim()) { ElMessage.warning('请填写模板名称'); return null }
  if (!smsSupports(draft.value, descriptor.value)) { ElMessage.warning('服务商不支持当前类型或范围，营销不能用于验证码'); return null }
  try { return { ...smsTemplateDTO(draft.value), parameters: smsParameterMap(rows.value, draft.value.provider) } }
  catch (error) { ElMessage.warning((error as Error).message); return null }
}
async function save() {
  if (busy.value) return
  if (locked.value) { ElMessage.warning('该模板有未确认的远程操作，请先核对供应商控制台并解除锁定后再修改'); return }
  const input = templateInput()
  if (!input) return
  busy.value = 'save'
  try {
    const id = await saveSMSTemplate(input)
    const item = { ...draft.value!, ...input, id }
    const index = templates.value.findIndex(template => template.id === id)
    if (index < 0) templates.value.push(item)
    else templates.value[index] = item
    edit(item)
    ElMessage.success('模板已本地保存，不代表供应商审核通过')
  } catch { ElMessage.error('保存失败，请检查模板、参数及已绑定场景的兼容性；草稿已保留') }
  finally { busy.value = '' }
}
async function preview() {
  if (busy.value) return
  const input = templateInput()
  if (!input) return
  if (!currentScene.value) { ElMessage.warning('请选择同类型预览场景'); return }
  busy.value = 'preview'
  previewResult.value = null
  try { previewResult.value = await previewSMSTemplate(input, sceneCode.value) }
  catch { ElMessage.error('本地预览失败，请检查场景、模板格式和变量映射；草稿已保留') }
  finally { busy.value = '' }
}
async function remove() {
  if (!draft.value?.id || busy.value) return
  if (locked.value) { ElMessage.warning('该模板有未确认的远程操作，请先核对供应商控制台并解除锁定后再删除'); return }
  if (scenes.value.some(scene => scene.template_id === draft.value!.id)) {
    ElMessage.warning('正在绑定的模板不可删除，请先到业务场景解绑（包括已关闭场景）')
    return
  }
  if (!await confirm('删除后不可恢复，当前未保存草稿也将丢弃。是否删除此模板？', '删除短信模板', '确认删除')) return
  busy.value = 'delete'
  try {
    await deleteSMSTemplate(draft.value.id)
    templates.value = templates.value.filter(item => item.id !== draft.value!.id)
    draft.value = null
    ElMessage.success('模板已删除')
  } catch { ElMessage.error('删除失败，模板可能正在绑定，请刷新场景后重试；草稿已保留') }
  finally { busy.value = '' }
}
function remoteProblem(action: SMSRemoteAction): string {
  const item = draft.value
  if (!item?.id) return '请先本地保存模板'
  if (busy.value || loading.value) return '操作进行中'
  if (locked.value) return '该模板有未确认的远程操作，请先在供应商控制台核对，再解除锁定'
  if (remoteStale.value) return '远程结果需核对，请先刷新配置；不要重复提交'
  if (dirty.value) return '请先保存模板和场景绑定的修改'
  if (item.provider !== routes.value[item.range_type || 'cn']?.provider) return '非该范围当前通道，仅可存档'
  if (!smsRemoteSupported(item, descriptor.value)) return '该服务商或范围不支持远程模板管理，请在供应商控制台维护'
  if (action === 'query' && !descriptor.value?.capabilities.audit_sync) return '供应商不支持审核同步'
  if (action === 'create' ? !!item.template_code : !item.template_code) return action === 'create' ? '已有模板编码，禁止重复创建' : '请先填写或创建远程模板编码'
  if ((action === 'create' || action === 'update') && !item.content.trim()) return '请填写正文并本地保存'
  if (action === 'delete' && scenes.value.some(scene => scene.template_id === item.id)) return '请先解除全部场景绑定'
  return ''
}
async function remote(action: SMSRemoteAction) {
  const problem = remoteProblem(action)
  if (problem) { ElMessage.warning(problem); return }
  const item = draft.value!
  if (!await confirm(`即将对「${item.name}」执行${smsRemoteActions[action]}，会真实访问供应商，使用已本地保存的内容。${action === 'delete' ? '远程删除不可恢复，可能影响其他系统使用此编码；本地模板仍保留。' : '本地保存不代表审核通过，审核结论以供应商返回为准。'} 是否确认？`, smsRemoteActions[action], '确认' + smsRemoteActions[action])) return
  if (remoteProblem(action)) return
  busy.value = 'remote:' + action
  remoteStale.value = true
  try {
    await remoteSMSTemplate(item.id, action)
    try {
      const list = await fetchSMSTemplates()
      templates.value = list
      const fresh = list.find(template => template.id === item.id)
      if (fresh) edit(fresh)
      else draft.value = null
      remoteStale.value = false
      ElMessage.success('远程操作已完成，审核状态已从后端刷新')
    } catch { ElMessage.warning('远程操作已完成，但读取最新状态失败。请刷新配置，勿重复提交') }
  } catch { ElMessage.error('远程操作失败或结果未确认，草稿已保留。请核对供应商控制台并刷新配置，勿重复提交') }
  finally { busy.value = '' }
}
async function unlock() {
  const item = draft.value
  if (!item?.id || !locked.value || busy.value) return
  if (!await confirm(`仅在已登录供应商控制台确认「${item.name}」上次远程操作的真实结果后才能解除锁定。解除后不会自动重放任何远程操作，本地审核状态将置为待同步。是否继续？`, '解除远程操作锁定', '确认已核对并解锁')) return
  busy.value = 'unlock'
  try {
    await unlockSMSTemplate(item.id)
    const list = await fetchSMSTemplates()
    templates.value = list
    const fresh = list.find(template => template.id === item.id)
    if (fresh) edit(fresh)
    remoteStale.value = false
    ElMessage.success('已解除锁定，请重新执行查询审核同步状态')
  } catch { ElMessage.error('解锁失败，请刷新配置后重试；若仍被锁定，请先核对供应商控制台') }
  finally { busy.value = '' }
}
async function insertVariable(key: string) {
  if (!draft.value || !canInsert.value) return
  const input = bodyInput.value?.textarea
  const value = draft.value.content
  const start = input?.selectionStart ?? value.length
  const end = input?.selectionEnd ?? start
  const token = '{{' + key + '}}'
  draft.value.content = value.slice(0, start) + token + value.slice(end)
  await nextTick()
  input?.focus()
  input?.setSelectionRange(start + token.length, start + token.length)
}
function sceneFor(binding: SMSBinding): SMSScene { return scenes.value.find(scene => scene.code === binding.code)! }
function availableTemplates(binding: SMSBinding) { return templates.value.filter(template => {
  const capability = providers.value.find(item => item.key === template.provider)
  const routeProvider = routes.value[template.range_type || 'cn']?.provider || ''
  return !!capability && canBindSMS(template, sceneFor(binding), routeProvider, capability)
}) }
function bindingProblem(binding: SMSBinding): boolean {
  return binding.template_id !== 0 && !availableTemplates(binding).some(template => template.id === binding.template_id)
}
async function saveBinding(binding: SMSBinding, unbind = false) {
  if (busy.value) return
  const scene = sceneFor(binding)
  const input = unbind ? { code: binding.code, template_id: 0, enabled: scene.kind === 'otp' } : { ...binding }
  if (!input.template_id) {
    if (!await confirm(scene.kind === 'otp'
      ? '解绑验证码模板后将立即回退通知设置中的旧通道验证码配置，不会关闭验证码。请先确认旧配置仍有效。是否继续？'
      : '解绑后将关闭该场景的短信通知。是否继续？', '确认解绑', '确认解绑')) return
    input.enabled = scene.kind === 'otp'
  }
  busy.value = 'binding:' + binding.code
  try {
    await saveSMSBinding(input)
    Object.assign(binding, input)
    Object.assign(scene, input)
    ElMessage.success('场景绑定已保存')
  } catch { ElMessage.error('绑定失败，请检查当前通道、模板启用状态、类型及场景变量；草稿已保留') }
  finally { busy.value = '' }
}
onBeforeRouteLeave(mayLeave)
function beforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value || busy.value) { event.preventDefault(); event.returnValue = '' }
}
onMounted(() => { window.addEventListener('beforeunload', beforeUnload); void load() })
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
</script>

<template>
  <div class="art-full-height sms-templates">
    <ElCard class="art-card">
      <template #header><div class="art-card-header"><div class="title"><h4>短信模板</h4><p>独立模板库、固定业务场景绑定及投递状态。本地保存不是供应商审核，不自动提交审核。</p></div></div></template>
      <el-alert title="模板绑定与远程操作均按模板范围匹配当前路由；验证码模板仅可使用国内路由。切换任一范围不会影响其他范围。" type="warning" :closable="false" />
      <div class="sms-toolbar">
        <span>当前路由：国内 {{ smsProviders[routes.cn?.provider || ''] || '未配置' }}，国际 {{ smsProviders[routes.global?.provider || ''] || '未配置' }}，营销 {{ smsProviders[routes.marketing?.provider || ''] || '未配置' }}</span>
        <router-link :to="{ name: 'admin-notify-settings' }">配置短信通道与验证码回退</router-link>
        <el-button :loading="loading" :disabled="!!busy" @click="load">刷新配置</el-button>
        <el-tag v-if="dirty" type="warning" role="status">有未保存修改</el-tag>
      </div>
      <nav class="sms-toolbar" aria-label="短信管理分栏">
        <el-button :type="tab === 'templates' ? 'primary' : 'default'" :aria-pressed="tab === 'templates'" @click="changeTab('templates')">模板库</el-button>
        <el-button :type="tab === 'scenes' ? 'primary' : 'default'" :aria-pressed="tab === 'scenes'" @click="changeTab('scenes')">业务场景绑定</el-button>
        <el-button :type="tab === 'deliveries' ? 'primary' : 'default'" :aria-pressed="tab === 'deliveries'" @click="changeTab('deliveries')">最近投递状态</el-button>
      </nav>
      <el-alert v-if="loadError" :title="loadError" type="error" :closable="false" role="alert" />
      <section v-if="tab === 'templates'" v-loading="loading">
        <div class="sms-toolbar"><el-button type="primary" :disabled="!ready || !!busy" @click="create">新增模板</el-button></div>
        <div class="sms-layout">
          <nav class="sms-list" aria-label="短信模板列表">
            <button v-for="item in templates" :key="item.id" type="button" class="sms-choice" :class="{ active: draft?.id === item.id }" :aria-pressed="draft?.id === item.id" :disabled="!!busy" @click="select(item)">
              <strong>{{ item.name }}</strong><span>{{ smsProviders[item.provider] || '未知服务商' }} · {{ item.kind === 'otp' ? '验证码' : '业务通知' }} · {{ item.enabled ? '已启用' : '已停用' }}</span>
              <span v-if="item.provider !== routes[item.range_type || 'cn']?.provider">仅存档，非该范围当前通道</span>
              <span v-if="item.remote_operation" class="sms-warning">远程操作未确认，已锁定</span>
            </button>
            <p v-if="ready && !templates.length">暂无模板，可新增模板或使用验证码旧配置。</p>
          </nav>
          <section v-if="draft" class="sms-editor" aria-label="短信模板编辑器" :aria-busy="!!busy">
            <div class="sms-toolbar"><h3>{{ draft.id ? '编辑模板' : '新增模板' }}</h3><el-button :disabled="!!busy" @click="closeEditor">关闭编辑</el-button></div>
            <el-form label-position="top" :disabled="!!busy" @submit.prevent="save">
              <el-form-item label="模板名称" required><el-input v-model="draft.name" aria-label="模板名称" :maxlength="100" /></el-form-item>
              <div class="sms-grid">
                <el-form-item label="模板服务商" required>
                  <el-select :model-value="draft.provider" :disabled="!!draft.id" aria-label="模板服务商" @update:model-value="changeProvider"><el-option v-for="item in providers" :key="item.key" :label="item.name" :value="item.key" /></el-select>
                </el-form-item>
                <el-form-item label="模板类型" required>
                  <el-select v-model="draft.kind" :disabled="!!draft.id" aria-label="模板类型"><el-option label="验证码" value="otp" :disabled="!descriptor?.capabilities.otp || draft.range_type === 'marketing'" /><el-option label="业务通知" value="notification" :disabled="!descriptor?.capabilities.notification" /></el-select>
                </el-form-item>
              </div>
              <el-form-item label="发送范围" required><el-select v-model="draft.range_type" :disabled="!!draft.id" aria-label="发送范围"><el-option v-for="range in descriptor?.capabilities.ranges || []" :key="range" :value="range" :label="smsRanges[range]" :disabled="range === 'marketing' && draft.kind === 'otp'" /></el-select></el-form-item>
              <p class="sms-hint">营销范围不能用于验证码。国际模板仅适用于非中国大陆号码；已保存模板的服务商、类型和范围不可修改，如需切换请新增。</p>
              <el-form-item label="允许被场景绑定"><el-switch v-model="draft.enabled" aria-label="启用模板" active-text="启用" inactive-text="停用" /><p class="sms-hint">已绑定验证码场景的模板不能停用；模板修改须兼容所有已绑定场景。停用模板不会解除绑定，删除前仍须解绑。</p></el-form-item>
              <el-form-item label="预览业务场景（决定可用变量）" required>
                <el-select v-model="sceneCode" aria-label="预览业务场景"><el-option v-for="scene in previewScenes" :key="scene.code" :label="scene.name" :value="scene.code" /></el-select>
              </el-form-item>
              <p v-if="draft.provider === 'aliyun_sms' || draft.provider === 'qcloudsms'" class="sms-hint">可填写已有模板编码，或先保存正文再明确确认远程创建。阿里云正文使用供应商参数名变量；腾讯云参数须从1连续编号（如1、2或param1、param2），正文使用对应编号占位符，如 {1}。</p>
              <p v-else-if="draft.provider === 'aliyun'" class="sms-hint">PNVS 仅支持验证码，不支持业务通知。请填写控制台模板编码，并添加且仅添加一行「code → 验证码（code）」映射。</p>
              <el-form-item v-if="!textOnly" label="控制台模板编码（待远程创建时留空）"><el-input v-model="draft.template_code" :disabled="!!draft.remote_template_id" aria-label="控制台模板编码" :maxlength="64" /></el-form-item>
              <el-form-item v-if="draft.provider !== 'aliyun'" label="短信正文（纯文本，最多500字，不含换行）"><el-input ref="bodyInput" v-model="draft.content" type="textarea" :rows="6" :maxlength="500" aria-label="短信纯文本正文" placeholder="远程创建或修改需填写正文；纯文本供应商必须填写正文" /></el-form-item>
              <el-form-item label="模板签名（留空使用通道对应范围签名）"><el-input v-model="draft.sign_name" aria-label="模板签名" :maxlength="100" /></el-form-item>
              <el-form-item label="申请说明 / 备注"><el-input v-model="draft.remark" aria-label="申请说明" :maxlength="500" /></el-form-item>
              <div class="sms-variables" aria-label="场景可用业务变量">
                <p>可用变量与示例{{ canInsert ? '（点击插入正文光标处）' : '（在下方映射表中选择）' }}：</p>
                <el-button v-for="variable in variables" :key="variable.key" class="variable-button" :disabled="!canInsert" :aria-label="'插入' + variable.label + '，示例：' + variable.example" @click="insertVariable(variable.key)"><span>{{ variable.label }}<code v-text="'{{' + variable.key + '}}'" /><small>示例：{{ variable.example }}</small></span></el-button>
              </div>
              <section v-if="!textOnly" aria-label="供应商参数映射">
                <h4>供应商参数名 → 业务变量</h4>
                <div v-for="(row, index) in rows" :key="index" class="sms-mapping">
                  <el-input v-model="row.name" :aria-label="'第' + (index + 1) + '行供应商参数名'" placeholder="供应商参数名，如 code" />
                  <el-select v-model="row.variable" :aria-label="'第' + (index + 1) + '行业务变量'" placeholder="选择业务变量">
                    <el-option v-for="variable in variables" :key="variable.key" :label="variable.label + '（' + variable.key + '）'" :value="variable.key" />
                    <el-option v-if="row.variable && !variables.some(variable => variable.key === row.variable)" :label="'当前场景不支持：' + row.variable" :value="row.variable" disabled />
                  </el-select>
                  <el-button type="danger" plain :aria-label="'删除第' + (index + 1) + '行映射'" @click="rows.splice(index, 1)">删除行</el-button>
                </div>
                <el-button @click="rows.push({ name: '', variable: '' })">添加参数映射</el-button>
              </section>
              <div class="sms-toolbar">
                <el-button type="primary" :loading="busy === 'save'" @click="save">保存模板</el-button>
                <el-button :loading="busy === 'preview'" @click="preview">本地预览草稿</el-button>
                <el-button v-if="draft.id" type="danger" plain :loading="busy === 'delete'" @click="remove">删除模板</el-button>
              </div>
              <p class="sms-hint">预览仅在本地服务中使用示例变量，不调用服务商、不产生费用，也不保存草稿。此处不提供真实试发。</p>
              <section class="sms-preview" aria-label="供应商审核与远程操作">
                <h4>供应商审核与远程操作</h4>
                <p role="status">最近同步状态：{{ smsAuditStatuses[draft.audit_status || 'unknown'] || '审核状态未知' }}</p>
                <p v-if="draft.audit_message">状态说明：{{ draft.audit_message }}</p>
                <p v-if="draft.audit_updated_at">同步时间：{{ draft.audit_updated_at }}</p>
                <p v-if="draft.remote_template_id">已关联远程编号：{{ draft.remote_template_id }}</p>
                <p class="sms-hint">以上为后端最近同步结果，不代表当前本地草稿已审核；本地修改不会自动提交供应商，也不会伪造审核通过。</p>
                <p v-if="locked" class="sms-warning" role="alert">该模板存在未确认的远程操作（{{ smsRemoteActions[draft.remote_operation as SMSRemoteAction] || draft.remote_operation }}），结果未知，已禁止修改、绑定、发送和重复提交。请先登录供应商控制台核对，再解除锁定。</p>
                <div v-if="locked" class="sms-toolbar">
                  <el-button type="warning" :loading="busy === 'unlock'" @click="unlock">解除远程操作锁定</el-button>
                  <span class="sms-hint">仅清除本地锁定标记并置为待同步，不会自动重放创建、修改、删除或发送。</span>
                </div>
                <p v-if="draft.provider === 'submail' && draft.range_type === 'global'" class="sms-warning">赛邮国际模板远程管理暂不受后端支持，以下操作已禁用，请在供应商控制台维护。</p>
                <p v-else-if="!descriptor?.capabilities.template_crud" class="sms-hint">此供应商不支持远程模板管理。</p>
                <p v-if="!descriptor?.capabilities.audit_sync" class="sms-hint">此供应商不支持审核状态同步，请自行核对供应商控制台。</p>
                <p v-if="remoteStale" class="sms-warning" role="alert">远程结果需核对，请刷新配置，勿重复提交。</p>
                <div v-for="(label, action) in smsRemoteActions" :key="action" class="sms-toolbar">
                  <el-button :type="action === 'delete' ? 'danger' : 'default'" :disabled="!!remoteProblem(action)" :loading="busy === 'remote:' + action" :aria-describedby="'sms-remote-' + action" @click="remote(action)">{{ label }}</el-button>
                  <span :id="'sms-remote-' + action" class="sms-hint">{{ remoteProblem(action) || '需明确确认，将真实访问供应商' }}</span>
                </div>
              </section>
            </el-form>
            <section v-if="previewResult" class="sms-preview" aria-label="短信纯文本预览">
              <h4>本地预览（非手机送达效果）</h4>
              <p>服务商：{{ smsProviders[previewResult.provider] || '未知服务商' }}</p>
              <p v-if="previewResult.template_code">模板编码：{{ previewResult.template_code }}</p>
              <pre v-if="previewResult.content">{{ previewResult.content }}</pre>
              <p v-for="(value, name) in previewResult.parameters" :key="name">{{ name }}：{{ value }}</p>
              <p class="sms-hint">此处为本地草稿的渲染结果，实际发送内容及审核结论以供应商为准。</p>
            </section>
          </section>
          <el-empty v-else description="请选择模板或新增模板" />
        </div>
      </section>
      <section v-else-if="tab === 'scenes'" aria-label="固定业务场景绑定">
        <el-alert title="验证码不可关闭；未绑定时回退旧通道验证码配置。业务通知默认关闭，需选择兼容模板并启用。停用通知不等于解绑。" type="info" :closable="false" />
        <article v-for="binding in bindings" :key="binding.code" class="sms-scene">
          <h3>{{ sceneFor(binding).name }} <el-tag v-if="bindingDirty(binding)" type="warning">未保存</el-tag></h3>
          <p>{{ sceneFor(binding).kind === 'otp' ? '验证码 · 必要场景，不可关闭' : '业务通知 · 默认关闭' }}</p>
          <el-form label-position="top" :disabled="!!busy">
            <el-form-item label="绑定模板">
              <el-select v-model="binding.template_id" :aria-label="sceneFor(binding).name + '绑定模板'">
                <el-option :label="sceneFor(binding).kind === 'otp' ? '不绑定（使用旧验证码配置）' : '不绑定（关闭通知）'" :value="0" />
                <el-option v-for="item in availableTemplates(binding)" :key="item.id" :label="item.name" :value="item.id" />
                <el-option v-if="bindingProblem(binding)" :label="'原绑定不可用，请重新配置或解绑'" :value="binding.template_id" disabled />
              </el-select>
            </el-form-item>
            <el-form-item label="启用场景"><el-switch v-model="binding.enabled" :disabled="sceneFor(binding).kind === 'otp' || binding.template_id === 0" :aria-label="'启用' + sceneFor(binding).name" active-text="启用" inactive-text="关闭" /></el-form-item>
            <p v-if="bindingProblem(binding)" class="sms-warning" role="alert">原模板与当前通道、类型或启用状态不匹配，不会自动切换服务商，请重新配置。</p>
            <div class="sms-toolbar">
              <el-button type="primary" :disabled="bindingProblem(binding)" :loading="busy === 'binding:' + binding.code" @click="saveBinding(binding)">保存绑定</el-button>
              <el-button v-if="sceneFor(binding).template_id || binding.template_id" type="danger" plain @click="saveBinding(binding, true)">解绑{{ sceneFor(binding).kind === 'otp' ? '并回退旧配置' : '并关闭通知' }}</el-button>
            </div>
          </el-form>
        </article>
      </section>
      <section v-else aria-label="最近一百条短信投递状态">
        <p>最近100条业务短信投递记录；每条业务短信最多自动尝试一次，不提供人工重发。</p>
        <p class="sms-hint">服务商已受理不保证手机送达；待核查表示结果不确定，不自动重发。失败、跳过、待处理和发送中按记录显示。</p>
        <el-button :loading="busy === 'deliveries'" :disabled="!!busy && busy !== 'deliveries'" @click="loadDeliveries">刷新投递记录</el-button>
        <el-alert v-if="deliveryError" :title="deliveryError" type="error" :closable="false" role="alert" />
        <div class="sms-table"><el-table :data="deliveries" empty-text="暂无投递记录">
          <el-table-column label="业务场景" min-width="150"><template #default="{ row }">{{ scenes.find(scene => scene.code === row.scene)?.name || '未知场景' }}</template></el-table-column>
          <el-table-column prop="recipient" label="接收号码（已脱敏）" min-width="160" />
          <el-table-column label="投递状态" min-width="190"><template #default="{ row }">{{ smsStatuses[row.status] || '未知状态' }}</template></el-table-column>
          <el-table-column label="服务商" min-width="140"><template #default="{ row }">{{ smsProviders[row.provider] || row.provider || '—' }}</template></el-table-column>
          <el-table-column label="服务商流水" min-width="180"><template #default="{ row }">{{ row.provider_message_id || '—' }}</template></el-table-column>
          <el-table-column label="请求编号" min-width="180"><template #default="{ row }">{{ row.request_id || '—' }}</template></el-table-column>
          <el-table-column label="错误码" min-width="150"><template #default="{ row }">{{ row.error_code || '—' }}</template></el-table-column>
          <el-table-column prop="message" label="状态说明" min-width="220" />
          <el-table-column prop="created_at" label="创建时间" min-width="180" />
        </el-table></div>
      </section>
    </ElCard>
  </div>
</template>

<style scoped>
.sms-toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; margin: 16px 0; }
.sms-toolbar .el-button + .el-button { margin-left: 0; }
.sms-layout { display: grid; grid-template-columns: minmax(180px, 240px) minmax(0, 1fr); gap: 24px; }
.sms-list { display: flex; flex-direction: column; gap: 8px; }
.sms-choice { text-align: left; padding: 12px; border: 1px solid var(--el-border-color); border-radius: 8px; background: var(--el-bg-color); color: var(--el-text-color-primary); cursor: pointer; overflow-wrap: anywhere; }
.sms-choice span { display: block; font-size: 12px; margin-top: 6px; }
.sms-choice.active { border-color: var(--el-color-primary); background: var(--el-color-primary-light-9); }
.sms-choice:focus-visible { outline: 2px solid var(--el-color-primary); outline-offset: 2px; }
.sms-editor, .sms-grid > *, .sms-mapping > * { min-width: 0; }
.sms-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.sms-hint { font-size: 12px; color: var(--el-text-color-secondary); width: 100%; margin: 10px 0; overflow-wrap: anywhere; }
.sms-warning { color: var(--el-color-danger); }
.sms-mapping { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto; gap: 8px; margin: 12px 0; }
.sms-variables { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 16px; }
.sms-variables > p { width: 100%; }
.sms-variables .variable-button { height: auto !important; min-width: 0; max-width: 100%; margin: 0; padding: 10px; white-space: normal; text-align: left; line-height: 1.5; }
.variable-button :deep(> span) { min-width: 0; max-width: 100%; overflow-wrap: anywhere; }
.variable-button code, .variable-button small { display: block; margin-top: 4px; overflow-wrap: anywhere; }
.sms-preview, .sms-scene { padding: 16px; margin-top: 16px; border: 1px solid var(--el-border-color); border-radius: 8px; overflow-wrap: anywhere; }
.sms-preview pre { white-space: pre-wrap; overflow-wrap: anywhere; }
.sms-table { max-width: 100%; overflow-x: auto; margin-top: 16px; }
@media (max-width: 768px) {
  .sms-layout, .sms-grid, .sms-mapping { grid-template-columns: minmax(0, 1fr); }
  .sms-list { max-height: 260px; overflow-y: auto; }
  .sms-mapping { border-bottom: 1px solid var(--el-border-color); padding-bottom: 12px; }
}
</style>
