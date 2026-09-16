<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onBeforeUnmount } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { fetchSMSProviders, type SMSProviderDescriptor, type SMSRange } from '../admin/smsTemplates'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useAdminSettings } from '../admin/useSettings'
import { testAdminEmail } from '../admin/api'

const { loading, saving, cfg, load, save } = useAdminSettings()

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

// ---- 测试邮件 ----
const testTo = ref('')
const testAccount = ref(-1)
const testing = ref(false)
// 可测试账号 = 已填写主机的账号（顺序与保存后的服务端列表一致），-1 表示自动轮换
const testAccountOptions = computed(() =>
  accounts.value
    .filter((a) => a.host.trim())
    .map((a, i) => ({ label: a.name || a.host, value: i })),
)
async function sendTest() {
  if (!testTo.value) {
    ElMessage.warning('请输入收件邮箱')
    return
  }
  testing.value = true
  try {
    const res = await testAdminEmail(testTo.value, testAccount.value)
    if (res.ok) ElMessage.success(res.msg || '测试邮件已发送')
    else ElMessage.error(res.msg || '发送失败')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '发送失败')
  } finally {
    testing.value = false
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
const mailSnapshot = () => JSON.stringify({ accounts: accounts.value, cooldown: cooldownSeconds.value })
const routesBody = () => Object.fromEntries(smsRanges.filter(({ key }) => smsRoutes.value[key].provider).map(({ key }) => [key, { ...smsRoutes.value[key] }]))
const smsBody = () => ({ sms_routes: JSON.stringify(routesBody()) })
const dirty = computed(() => ready.value && (JSON.stringify(smsBody()) !== smsBaseline.value || mailSnapshot() !== mailBaseline.value))
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
    await nextTick()
    smsBaseline.value = JSON.stringify(smsBody())
    mailBaseline.value = mailSnapshot()
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
  <div class="art-full-height" v-loading="loading">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>通知设置</h4>
            <p>邮件与短信发送通道配置。邮件内容、启停与总开关在「邮件模板」页维护。</p>
          </div>
        </div>
      </template>

      <el-form :disabled="!ready || !!saving" label-position="top">
      <el-divider content-position="left">邮件通知</el-divider>
      <p class="mb-3 text-xs text-g-500">可配置多个 SMTP 账号（轮换发送）；密码留空表示沿用旧值。</p>

      <div v-for="(a, i) in accounts" :key="a._key" class="mb-3 rounded-lg border border-[var(--art-card-border)] p-4">
        <div class="mb-2 flex items-center justify-between">
          <strong class="text-sm text-g-800">账号 {{ i + 1 }}</strong>
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
        <el-checkbox v-model="a.enabled" class="mt-2">启用该账号</el-checkbox>
      </div>
      <el-button size="small" @click="addAccount">+ 添加账号</el-button>

      <el-divider content-position="left">发送选项</el-divider>
      <div class="admin-form-grid">
        <el-form-item label="发送失败冷却（秒，1-86400）">
          <el-input-number v-model="cooldownSeconds" :min="1" :max="86400" class="w-full" />
        </el-form-item>
      </div>
      <p class="text-xs text-g-500">
        业务邮件的内容、启停和总开关在
        <router-link :to="{ name: 'admin-email-templates' }">邮件模板</router-link>
        页维护；此处仅配置发送通道。
      </p>

      <div class="notify-save-row">
        <el-button type="primary" :loading="saving === 'mail'" @click="saveMail">保存邮件设置</el-button>
        <span class="notify-test">
          <el-input v-model="testTo" placeholder="测试收件邮箱" style="width: 220px" />
          <el-select v-model="testAccount" style="width: 150px" placeholder="发送账号">
            <el-option label="自动（轮换全部）" :value="-1" />
            <el-option v-for="o in testAccountOptions" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
          <el-button :loading="testing" @click="sendTest">发送测试邮件</el-button>
        </span>
      </div>

      </el-form>
      <el-divider content-position="left">短信</el-divider>
      <el-alert title="验证码仅走国内路由。未绑定验证码模板时，仍使用旧的国内单通道配置回退；业务通知按模板范围选择对应路由。" type="warning" :closable="false" />
      <p class="my-3"><router-link :to="{ name: 'admin-sms-templates' }">管理短信模板、业务场景绑定与投递状态</router-link></p>
      <el-alert v-if="loadError" :title="loadError" type="error" :closable="false" />
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
        <el-tag v-if="dirty" type="warning">有未保存修改</el-tag>
      </div>
      </el-form>
    </ElCard>
  </div>
</template>

<style scoped>
.notify-save-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}
.notify-test {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
</style>
