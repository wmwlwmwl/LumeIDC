<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useAdminSettings } from '../admin/useSettings'
import { testAdminEmail } from '../admin/api'

const { loading, saving, cfg, load, save, flag } = useAdminSettings()

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

function saveMail() {
  const clean = accounts.value
    .filter((a) => a.host.trim())
    .map(({ name, host, port, user, pass, from, enabled }) => ({ name, host, port, user, pass, from, enabled }))
  save(
    '',
    {
      smtp_accounts: JSON.stringify(clean),
      smtp_cooldown_seconds: String(cooldownSeconds.value || 60),
      notify_email_forward_enabled: flag('notify_email_forward_enabled'),
    },
    'mail',
  )
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
function saveSms() {
  save('sms', {
    sms_provider: cfg['sms_provider'] || '',
    sms_endpoint: cfg['sms_endpoint'] || '',
    sms_access_key: cfg['sms_access_key'] || '',
    sms_username: cfg['sms_username'] || '',
    sms_secret_key: cfg['sms_secret_key'] || '',
    sms_sign_name: cfg['sms_sign_name'] || '',
    sms_template_code: cfg['sms_template_code'] || '',
    sms_template_content: cfg['sms_template_content'] || '',
  })
}

load()
</script>

<template>
  <div class="art-full-height" v-loading="loading">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>通知设置</h4>
            <p>邮件与短信通道配置，用于站内信转发、验证码与业务通知。</p>
          </div>
        </div>
      </template>

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
        <el-form-item label="站内信邮件转发">
          <el-switch v-model="cfg['notify_email_forward_enabled']" active-value="1" inactive-value="0" active-text="启用" />
        </el-form-item>
      </div>

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

      <el-divider content-position="left">短信</el-divider>
      <el-form-item label="服务商">
        <el-select v-model="cfg['sms_provider']" clearable placeholder="选择短信服务商" style="max-width: 320px">
          <el-option label="阿里云 PNVS（号码认证）" value="aliyun" />
          <el-option label="阿里云短信" value="aliyun_sms" />
          <el-option label="Stay33" value="stay33" />
        </el-select>
      </el-form-item>

      <p v-if="!cfg['sms_provider']" class="text-xs text-g-500">选择服务商后填写对应参数。</p>

      <!-- 阿里云 PNVS（号码认证） -->
      <template v-else-if="cfg['sms_provider'] === 'aliyun'">
        <div class="admin-form-grid">
          <el-form-item label="AccessKey ID"><el-input v-model="cfg['sms_access_key']" /></el-form-item>
          <el-form-item label="AccessKey Secret（留空保持不变）">
            <el-input v-model="cfg['sms_secret_key']" type="password" show-password />
          </el-form-item>
          <el-form-item label="签名名称（签名/方案）"><el-input v-model="cfg['sms_sign_name']" /></el-form-item>
          <el-form-item label="模板编码（选填）">
            <el-input v-model="cfg['sms_template_code']" placeholder="留空按场景默认：登录/注册 100001、改绑 100002、绑定 100004、验证 100005" />
          </el-form-item>
        </div>
        <p class="text-xs text-g-500">接口地址固定为 dypnsapi.aliyuncs.com，无需配置。</p>
      </template>

      <!-- 阿里云短信 -->
      <template v-else-if="cfg['sms_provider'] === 'aliyun_sms'">
        <div class="admin-form-grid">
          <el-form-item label="AccessKey ID"><el-input v-model="cfg['sms_access_key']" /></el-form-item>
          <el-form-item label="AccessKey Secret（留空保持不变）">
            <el-input v-model="cfg['sms_secret_key']" type="password" show-password />
          </el-form-item>
          <el-form-item label="签名名称"><el-input v-model="cfg['sms_sign_name']" /></el-form-item>
          <el-form-item label="模板编码"><el-input v-model="cfg['sms_template_code']" placeholder="控制台申请的短信模板 CODE" /></el-form-item>
          <el-form-item label="接口地址（选填）">
            <el-input v-model="cfg['sms_endpoint']" placeholder="仅域名，默认 dysmsapi.aliyuncs.com" />
          </el-form-item>
        </div>
      </template>

      <!-- Stay33 -->
      <template v-else-if="cfg['sms_provider'] === 'stay33'">
        <div class="admin-form-grid">
          <el-form-item label="用户名"><el-input v-model="cfg['sms_username']" /></el-form-item>
          <el-form-item label="密钥（留空保持不变）">
            <el-input v-model="cfg['sms_secret_key']" type="password" show-password />
          </el-form-item>
          <el-form-item label="签名名称"><el-input v-model="cfg['sms_sign_name']" /></el-form-item>
          <el-form-item label="接口地址（选填）">
            <el-input v-model="cfg['sms_endpoint']" placeholder="默认 https://idc.stay33.cn/sms/sendApi.php" />
          </el-form-item>
        </div>
        <el-form-item label="模板内容（选填）">
          <el-input
            v-model="cfg['sms_template_content']"
            type="textarea"
            :rows="2"
            placeholder="默认：【签名】您的验证码是：{code}，5分钟内有效。可用占位：{code}、{purpose}、{sign_name}"
          />
        </el-form-item>
      </template>

      <div class="notify-save-row">
        <el-button type="primary" :loading="saving === 'sms'" @click="saveSms">保存短信设置</el-button>
      </div>
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
