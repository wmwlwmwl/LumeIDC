<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onBeforeUnmount } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { fetchSMSProviders, type SMSProviderDescriptor, type SMSRange } from '../admin/smsTemplates'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useAdminSettings } from '../admin/useSettings'
import { testAdminEmail } from '../admin/api'
import { http } from '../http/index'

// 邮件出站渠道清单（内置 smtp + 插件注册渠道）
async function fetchMailSenders(): Promise<{ name: string; label: string }[]> {
  const res = await http.get<{ ok: number; senders?: { name: string; label: string }[] }>('/mail-senders')
  return res.senders || []
}

const { loading, saving, cfg, load, save } = useAdminSettings()

// ---- 邮件出站渠道（smtp 内置；插件渠道经 MailSender 注册，密钥在各插件配置页）----
const mailChannel = ref('smtp')
const mailSenders = ref<{ name: string; label: string }[]>([])
watch(() => cfg['mail_channel'], (v) => { mailChannel.value = v || 'smtp' }, { immediate: true })

// ---- 邮件账号（多账号，密码留空保持旧值）----
interface MailAccount {
  name: string
  host: string
  port: number
  user: string
  pass: string
  from: string
  enabled: boolean
  _key: number // 稳定 key：删除行后避免卡片内部状态错位
}
let accountKeySeq = 0
const accounts = ref<MailAccount[]>([])

function parseAccounts() {
  try {
    const raw = cfg['smtp_accounts']
    accounts.value = raw
      ? (JSON.parse(raw) as MailAccount[]).map((a) => ({ ...a, _key: ++accountKeySeq }))
      : []
  } catch {
    accounts.value = []
  }
  if (!accounts.value.length) addAccount()
}
// cfg 初次加载后据此回填表单（密码已被服务端掩码为空）；保存成功后不重拉，保留本地输入
watch(() => cfg['smtp_accounts'], parseAccounts, { immediate: true })

function addAccount() {
  accounts.value.push({ name: '', host: '', port: 587, user: '', pass: '', from: '', enabled: true, _key: ++accountKeySeq })
}
async function removeAccount(i: number) {
  const ok = await ElMessageBox.confirm('确认移除该邮件账号？', '移除账号', { type: 'warning' }).catch(() => null)
  if (!ok) return
  accounts.value.splice(i, 1)
}
// 账号顺序即轮换发送顺序，与旧 SSR 页一致：上移/下移交换相邻两项
function moveAccount(i: number, dir: -1 | 1) {
  const j = i + dir
  if (j < 0 || j >= accounts.value.length) return
  ;[accounts.value[i], accounts.value[j]] = [accounts.value[j], accounts.value[i]]
}

async function saveMail() {
  if (!ready.value || saving.value) return
  const clean = accounts.value
    .filter((a) => a.host.trim())
    .map(({ name, host, port, user, pass, from, enabled }) => ({ name, host, port, user, pass, from, enabled }))
  if (await save(
    '',
    {
      smtp_accounts: JSON.stringify(clean),
      smtp_cooldown_seconds: String(cooldownSeconds.value || 60),
      mail_channel: mailChannel.value || 'smtp',
    },
    'mail',
  )) {
    accounts.value.forEach(account => { account.pass = '' })
    mailBaseline.value = mailSnapshot()
  }
}

// 冷却秒数：el-input-number 需要 number 类型，而 cfg 值为服务端返回的字符串
const cooldownSeconds = ref(60)
watch(
  () => cfg['smtp_cooldown_seconds'],
  (v) => {
    const n = Number(v)
    if (Number.isFinite(n) && n > 0) cooldownSeconds.value = n
  },
  { immediate: true },
)

// ---- 管理员告警收件邮箱 ----
const adminNotify = ref('')
watch(() => cfg['admin_notify_email'], (v) => { adminNotify.value = v || '' }, { immediate: true })
const adminBaseline = ref('')
const adminSnapshot = () => adminNotify.value.trim()
async function saveAdminNotify() {
  if (!ready.value || saving.value) return
  if (await save('admin_notify', { admin_notify_email: adminSnapshot() })) {
    adminBaseline.value = adminSnapshot()
  }
}

// ---- 测试邮件 ----
const testTo = ref('')
const testingAccount = ref<number | null>(null)
// 测试接口按已保存账号的下标发送；修改或排序后必须先保存，避免测试到旧账号。
async function sendTest(account: MailAccount) {
  if (!ready.value || saving.value || testingAccount.value !== null) return
  if (mailSnapshot() !== mailBaseline.value) {
    ElMessage.warning('请先保存邮件设置，再测试该账号')
    return
  }
  const index = accounts.value.filter(a => a.host.trim()).findIndex(a => a._key === account._key)
  if (index < 0) {
    ElMessage.warning('请填写 SMTP 主机并保存邮件设置')
    return
  }
  if (!testTo.value.trim()) {
    ElMessage.warning('请输入收件邮箱')
    return
  }
  testingAccount.value = account._key
  try {
    const res = await testAdminEmail(testTo.value.trim(), index)
    if (res.ok) ElMessage.success(res.msg || '测试邮件已发送')
    else ElMessage.error(res.msg || '发送失败')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '发送失败')
  } finally {
    testingAccount.value = null
  }
}

// ---- 短信 ----
const providers = ref<SMSProviderDescriptor[]>([])
const ready = ref(false)
const loadError = ref('')
type SMSRouteDraft = Record<string, string>
const smsRanges: { key: SMSRange; label: string }[] = [{ key: 'cn', label: '国内' }, { key: 'global', label: '国际' }, { key: 'marketing', label: '营销' }]
const smsRoutes = ref<Record<SMSRange, SMSRouteDraft>>({ cn: {}, global: {}, marketing: {} })
const smsBaseline = ref('')
const mailBaseline = ref('')
const mailSnapshot = () => JSON.stringify({ accounts: accounts.value, cooldown: cooldownSeconds.value, channel: mailChannel.value })
const routesBody = () => Object.fromEntries(smsRanges.filter(({ key }) => smsRoutes.value[key].provider).map(({ key }) => [key, { ...smsRoutes.value[key] }]))
const smsBody = () => ({ sms_routes: JSON.stringify(routesBody()) })
const dirty = computed(() => ready.value && (JSON.stringify(smsBody()) !== smsBaseline.value || mailSnapshot() !== mailBaseline.value || adminSnapshot() !== adminBaseline.value))
const fieldLabels: Record<string, string> = {
  sms_access_key: '应用标识 / 访问密钥标识', sms_secret_key: '密钥或密码（留空保持不变）',
  sms_username: '账号 / 腾讯云短信应用编号', sms_sign_name: '签名', sms_endpoint: '供应商固定接口地址（选填）',
  sms_region: '腾讯云地域（默认 ap-guangzhou）', sms_global_access_key: '国际应用标识',
  sms_global_secret_key: '国际密钥（留空保持不变）', sms_global_sign_name: '国际签名',
}
function routeFields(range: SMSRange) { return providers.value.find(item => item.key === smsRoutes.value[range].provider)?.config_fields || [] }
function changeSmsProvider(range: SMSRange, provider: string) {
  if (saving.value || provider === smsRoutes.value[range].provider) return
  smsRoutes.value[range] = provider ? { provider } : {}
}
function parseRoutes() {
  try {
    const raw = cfg.sms_routes || '{}'
    const saved = JSON.parse(raw) as Partial<Record<SMSRange, SMSRouteDraft>>
    smsRoutes.value = { cn: { ...(saved.cn || {}) }, global: { ...(saved.global || {}) }, marketing: { ...(saved.marketing || {}) } }
  } catch { smsRoutes.value = { cn: {}, global: {}, marketing: {} } }
}
async function saveSms() {
  if (!ready.value || saving.value) return
  if (await save('sms', smsBody())) {
    for (const route of Object.values(smsRoutes.value)) {
      route.sms_secret_key = ''
      route.sms_global_secret_key = ''
    }
    smsBaseline.value = JSON.stringify(smsBody())
  }
}
async function initialize() {
  loadError.value = ''
  try {
    const [ok, descriptors] = await Promise.all([load(), fetchSMSProviders()])
    if (!ok) throw new Error('读取设置失败')
    providers.value = descriptors
    parseRoutes()
    // 邮件渠道清单失败不阻塞页面（无插件渠道时仅显示内置 SMTP）
    fetchMailSenders().then((list) => { mailSenders.value = list }).catch(() => {})
    await nextTick()
    smsBaseline.value = JSON.stringify(smsBody())
    mailBaseline.value = mailSnapshot()
    adminBaseline.value = adminSnapshot()
    ready.value = true
  } catch { loadError.value = '读取通知设置或短信供应商失败，请重新加载页面；保存已禁用，避免覆盖旧配置' }
}
onBeforeRouteLeave(async () => {
  if (saving.value) return false
  if (!dirty.value) return true
  return !!await ElMessageBox.confirm('有未保存的通知设置，离开将丢弃修改。是否继续？', '未保存的设置', { confirmButtonText: '丢弃并离开', cancelButtonText: '取消', type: 'warning' }).catch(() => false)
})
function beforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value || saving.value) { event.preventDefault(); event.returnValue = '' }
}
onMounted(() => window.addEventListener('beforeunload', beforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
void initialize()
</script>

<template>
  <div class="notify-page art-full-height" v-loading="loading">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>通知设置</h4>
            <p>配置发送通道与告警接收人，让每一条通知送达正确的位置。</p>
          </div>
        </div>
      </template>

      <el-alert v-if="loadError" :title="loadError" type="error" :closable="false" class="load-alert" />
      <div class="notify-overview">
        <span>通道配置与内容模板分别维护，各区域独立保存。</span>
        <el-tag v-if="dirty" type="warning" effect="plain">有未保存修改</el-tag>
      </div>
      <section class="notify-section">
      <header class="section-heading">
        <div><span class="section-kicker">邮件通道</span><h3>SMTP 发送账号</h3><p>支持多账号轮换发送，按排列顺序依次使用。</p></div>
        <router-link class="template-link" :to="{ name: 'admin-email-templates' }">管理邮件模板</router-link>
      </header>
      <el-form :disabled="!ready || !!saving" label-position="top">

      <div class="mail-channel">
        <el-form-item label="出站渠道">
          <el-select v-model="mailChannel" style="max-width: 420px; width: 100%">
            <el-option label="内置 SMTP（多账号轮换）" value="smtp" />
            <el-option v-for="s in mailSenders.filter((x) => x.name !== 'smtp')" :key="s.name" :label="s.label" :value="s.name" />
          </el-select>
        </el-form-item>
        <p v-if="mailChannel !== 'smtp'" class="field-help">
          当前使用插件渠道「{{ mailSenders.find((x) => x.name === mailChannel)?.label || mailChannel }}」，其密钥在对应插件的配置页维护；SMTP 账号仅作渠道移除时的回退。
        </p>
      </div>

      <div v-for="(a, i) in accounts" :key="a._key" class="account-card">
        <div class="account-heading">
          <div class="account-title"><span class="account-number">{{ String(i + 1).padStart(2, '0') }}</span><strong>{{ a.name || '邮件账号 ' + (i + 1) }}</strong><el-tag size="small" :type="a.enabled ? 'success' : 'info'" effect="plain">{{ a.enabled ? '已启用' : '已停用' }}</el-tag></div>
          <span>
            <el-button size="small" text :disabled="i === 0" @click="moveAccount(i, -1)">上移</el-button>
            <el-button size="small" text :disabled="i === accounts.length - 1" @click="moveAccount(i, 1)">下移</el-button>
            <el-button size="small" text type="danger" @click="removeAccount(i)">移除</el-button>
          </span>
        </div>
        <div class="admin-form-grid">
          <el-form-item label="名称"><el-input v-model="a.name" placeholder="选填，如 主账号" /></el-form-item>
          <el-form-item label="SMTP 主机"><el-input v-model="a.host" placeholder="如 smtp.qq.com" /></el-form-item>
          <el-form-item label="端口"><el-input-number v-model="a.port" :min="1" :max="65535" class="w-full" /></el-form-item>
          <el-form-item label="用户名"><el-input v-model="a.user" /></el-form-item>
          <el-form-item label="密码"><el-input v-model="a.pass" type="password" show-password placeholder="留空保持不变" /></el-form-item>
          <el-form-item label="发件人"><el-input v-model="a.from" placeholder="如 noreply@qq.com" /></el-form-item>
        </div>
        <div class="account-footer">
        <el-checkbox v-model="a.enabled">启用该账号</el-checkbox>
        <div class="notify-test">
          <el-input v-model="testTo" placeholder="测试收件邮箱" :aria-label="`账号 ${i + 1} 测试收件邮箱`" style="width: 220px; max-width: 100%" />
          <el-button :loading="testingAccount === a._key" :disabled="testingAccount !== null && testingAccount !== a._key" @click="sendTest(a)">测试该账号</el-button>
        </div>
        </div>
        <p class="field-help">密码留空沿用原值。测试使用已保存的配置，修改后请先保存。</p>
      </div>
      <el-button class="add-account" plain @click="addAccount">+ 添加邮件账号</el-button>

      <div class="delivery-options">
      <div><h4>失败冷却</h4><p class="field-help">账号发送失败后，等待指定秒数再尝试使用。</p></div>
      <div class="cooldown-field">
        <el-form-item label="发送失败冷却（秒，1-86400）">
          <el-input-number v-model="cooldownSeconds" :min="1" :max="86400" class="w-full" />
        </el-form-item>
      </div>
      </div>
      <p class="field-help">
        业务邮件的内容、启停和总开关在
        <router-link :to="{ name: 'admin-email-templates' }">邮件模板</router-link>
        页维护；此处仅配置发送通道。
      </p>

      <div class="notify-save-row mt-4">
        <el-button type="primary" :loading="saving === 'mail'" @click="saveMail">保存邮件设置</el-button>
      </div>

      </el-form>
      </section>

      <section class="notify-section">
      <header class="section-heading"><div><span class="section-kicker">管理员告警</span><h3>需要处理的通知，及时送达</h3><p>接收人工实名申请，以及开通、续费、升降配失败告警（含上游余额不足）。</p></div></header>
      <el-form :disabled="!ready || !!saving" label-position="top">
      <div class="alert-recipients">
      <el-form-item label="收件邮箱">
        <el-input v-model="adminNotify" type="textarea" :rows="2" placeholder="ops@example.com, admin@example.com" aria-label="管理员告警收件邮箱" />
      </el-form-item>
      <p class="field-help">最多 10 个邮箱，用逗号、分号或换行分隔；留空则不发送管理员告警。</p>
      </div>
      <div class="notify-save-row">
        <el-button type="primary" :loading="saving === 'admin_notify'" @click="saveAdminNotify">保存告警收件邮箱</el-button>
      </div>

      </el-form>
      </section>

      <section class="notify-section">
      <header class="section-heading"><div><span class="section-kicker">短信通道</span><h3>按发送范围配置服务商</h3><p>国内、国际与营销通道分别配置，独立使用。</p></div><router-link class="template-link" :to="{ name: 'admin-sms-templates' }">管理短信模板</router-link></header>
      <el-alert title="验证码仅走国内路由。未绑定验证码模板时，仍使用旧的国内单通道配置回退；业务通知按模板范围选择对应路由。" type="warning" :closable="false" />
      <el-form :disabled="!ready || !!saving" label-position="top">
        <el-tabs>
          <el-tab-pane v-for="range in smsRanges" :key="range.key" :label="range.label">
            <p class="text-xs text-g-500">{{ range.key === 'cn' ? '国内路由用于验证码和国内通知。' : range.key === 'global' ? '国际路由仅用于国际业务通知。' : '营销路由仅用于营销业务通知，不可用于验证码。' }}</p>
            <el-form-item :label="range.label + '服务商'">
              <el-select :model-value="smsRoutes[range.key].provider" clearable placeholder="选择短信服务商" style="max-width: 320px" :aria-label="range.label + '短信服务商'" @update:model-value="changeSmsProvider(range.key, $event)">
                <el-option v-for="item in providers.filter(item => item.capabilities.ranges.includes(range.key) && item.capabilities.notification)" :key="item.key" :label="item.name" :value="item.key" />
              </el-select>
            </el-form-item>
            <div v-if="smsRoutes[range.key].provider" class="admin-form-grid">
              <el-form-item v-for="key in routeFields(range.key)" :key="key" :label="fieldLabels[key] || key">
                <el-input v-model="smsRoutes[range.key][key]" :aria-label="range.label + (fieldLabels[key] || key)" :type="key.includes('secret') ? 'password' : 'text'" :show-password="key.includes('secret')" autocomplete="off" />
              </el-form-item>
            </div>
          </el-tab-pane>
        </el-tabs>
      <div class="notify-save-row">
        <el-button type="primary" :loading="saving === 'sms'" @click="saveSms">保存短信设置</el-button>
      </div>
      </el-form>
      </section>
    </ElCard>
  </div>
</template>

<style scoped>
.notify-page { color: var(--el-text-color-primary); }
.notify-overview { display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; color: var(--el-text-color-secondary); font-size: 13px; margin-bottom: 24px; }
.load-alert { margin-bottom: 20px; }
.notify-section { border: 1px solid var(--el-border-color-lighter); border-radius: 12px; padding: 24px; margin-bottom: 24px; }
.notify-section:last-child { margin-bottom: 0; }
.section-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; flex-wrap: wrap; margin-bottom: 24px; }
.section-kicker { color: var(--el-color-primary); font-size: 12px; font-weight: 500; }
.section-heading h3 { font-size: 18px; font-weight: 600; margin: 6px 0; }
.section-heading p, .field-help { font-size: 13px; line-height: 1.7; color: var(--el-text-color-secondary); margin: 6px 0 0; }
.template-link { display: inline-flex; align-items: center; min-height: 32px; padding: 0 12px; border: 1px solid var(--el-color-primary-light-5); border-radius: 6px; color: var(--el-color-primary); background: var(--el-color-primary-light-9); font-size: 13px; white-space: nowrap; text-decoration: none; }
.template-link:hover { border-color: var(--el-color-primary); background: var(--el-color-primary-light-8); }
.account-card { padding: 20px; border: 1px solid var(--el-border-color-lighter); border-radius: 10px; margin-bottom: 16px; background: var(--el-fill-color-blank); }
.account-heading, .account-footer { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px; }
.account-heading { margin-bottom: 20px; }
.account-title { display: flex; align-items: center; gap: 10px; min-width: 0; flex-wrap: wrap; }
.account-title strong { overflow-wrap: anywhere; font-size: 14px; }
.account-number { display: inline-flex; align-items: center; justify-content: center; width: 32px; height: 32px; border-radius: 8px; background: var(--el-color-primary-light-9); color: var(--el-color-primary); font-size: 13px; }
.account-footer { border-top: 1px solid var(--el-border-color-lighter); padding-top: 16px; margin-top: 4px; }
.add-account { width: 100%; border-style: dashed; height: 40px; }
.delivery-options { display: flex; align-items: center; justify-content: space-between; gap: 24px; flex-wrap: wrap; background: var(--el-fill-color-light); border-radius: 10px; padding: 20px; margin: 24px 0 14px; }
.delivery-options h4 { margin: 0; font-size: 14px; font-weight: 500; }
.cooldown-field { width: 240px; max-width: 100%; }
.cooldown-field :deep(.el-form-item) { margin-bottom: 0; }
.alert-recipients { max-width: 760px; }
.notify-section :deep(.admin-form-grid) { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0 20px; }
.notify-section :deep(.el-tabs) { margin-top: 16px; }
.notify-section :deep(.el-tab-pane > p) { margin: 0 0 20px; line-height: 1.7; }
.notify-save-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  border-top: 1px solid var(--el-border-color-lighter);
  padding-top: 20px;
  margin-top: 24px;
}
.notify-test {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
@media (max-width: 1100px) {
  .notify-section :deep(.admin-form-grid) { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 640px) {
  .notify-section { padding: 16px; }
  .account-card { padding: 14px; }
  .notify-section :deep(.admin-form-grid) { grid-template-columns: minmax(0, 1fr); }
  .delivery-options { padding: 16px; gap: 16px; }
  .notify-test { width: 100%; }
  .notify-test :deep(.el-input) { flex: 1; min-width: 140px; }
}
</style>
