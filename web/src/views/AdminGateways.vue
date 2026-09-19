<script setup lang="ts">
import { useAdminRequest } from '../admin/useAdminTable'
import { ref, reactive, computed, onMounted, h } from 'vue'
import { ElMessage, ElMessageBox, ElTag, ElButton } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import type { ColumnOption } from '@/types'
import { copyText } from '@/utils/clipboard'
import ArtButtonTable from '../components/core/forms/art-button-table/index.vue'
import { fetchAdminGateways, saveAdminGateway, deleteAdminGateway, type AdminGateway } from '../admin/api'

const list = ref<AdminGateway[]>([])
const loading = ref(false)
const saving = ref(false)
const showSearchBar = ref(true)
const searchForm = ref<{ q: string; driver: string; enabled: string }>({ q: '', driver: '', enabled: '' })

const searchItems = [
  { label: '关键词', key: 'q', type: 'input', placeholder: '搜索名称 / 编码', clearable: true },
  {
    label: '类型',
    key: 'driver',
    type: 'select',
    props: {
      placeholder: '全部类型',
      clearable: true,
      // 直接取自驱动注册表，避免与表单下拉各维护一份而失同步
      options: gatewayDriverList(),
    },
  },
  {
    label: '状态',
    key: 'enabled',
    type: 'select',
    props: {
      placeholder: '全部状态',
      clearable: true,
      options: [
        { label: '启用', value: '1' },
        { label: '停用', value: '0' },
      ],
    },
  },
]

const filtered = computed(() => {
  const q = searchForm.value.q.trim().toLowerCase()
  const { driver, enabled } = searchForm.value
  return list.value.filter((g) => {
    if (q && !`${g.name} ${g.code}`.toLowerCase().includes(q)) return false
    if (driver && g.driver !== driver) return false
    if (enabled && String(g.enabled ? '1' : '0') !== enabled) return false
    return true
  })
})

const EPAY_CHANNELS = [
  { label: '支付宝', value: 'alipay' },
  { label: '微信支付', value: 'wxpay' },
  { label: 'QQ钱包', value: 'qqpay' },
  { label: '网银支付', value: 'bank' },
  { label: '京东支付', value: 'jdpay' },
  { label: 'PayPal', value: 'paypal' },
]
const EPAY_CUSTOM_CHANNEL = '__custom__'

const dialog = ref(false)
const editing = ref(false) // 编辑既有网关（编码不可改）
const form = reactive<Record<string, any>>({
  code: '',
  driver: 'epay',
  name: '',
  fee_percent: '0',
  sort: '0',
  enabled: '1',
  api_url: '',
  pid: '',
  key: '',
  channel: '',
  channel_choice: 'alipay',
  payment_mode: 'redirect',
  mobile_qrcode: '0',
  app_id: '',
  mch_id: '',
  api_v3_key: '',
  cert_serial: '',
  public_key_id: '',
  h5_app_name: '',
  h5_app_url: '',
  private_key: '',
  public_key: '',
})

// 各驱动实际需要的字段（与 SSR 表单的 fields 映射一致）
import { GATEWAY_DRIVERS, gatewayDriverList } from '../admin/gateway-drivers'
const visibleFields = computed(() => GATEWAY_DRIVERS[form.driver]?.fields || [])
const isCustomChannel = computed(() => form.channel_choice === EPAY_CUSTOM_CHANNEL)

// 手续费率为字符串，字典序会 9>10，需按数值比较
const byNumber =
  (key: 'fee_percent') =>
  (a: AdminGateway, b: AdminGateway): number =>
    Number(a[key] || 0) - Number(b[key] || 0)

const columns = ref<ColumnOption[]>([
  { prop: 'id', label: 'ID', width: 64, sortable: true },
  {
    prop: 'name',
    label: '名称',
    minWidth: 150,
    sortable: true,
    formatter: (row) => h('b', { style: 'color:var(--art-gray-800);font-weight:650' }, row.name),
  },
  {
    prop: 'code',
    label: '编码',
    width: 130,
    sortable: true,
    formatter: (row) =>
      h(
        'span',
        {
          style:
            'padding:2px 7px;color:var(--theme-color-deep);font-family:ui-monospace,Menlo,monospace;font-size:11px;background:var(--theme-color-soft);border-radius:5px',
        },
        row.code,
      ),
  },
  {
    prop: 'driver',
    label: '类型',
    width: 130,
    sortable: true,
    formatter: (row) =>
      h(
        'span',
        {
          style: `padding:2px 7px;font-size:11px;border-radius:5px;${
            GATEWAY_DRIVERS[row.driver]?.style || GATEWAY_DRIVERS.mock.style
          }`,
        },
        GATEWAY_DRIVERS[row.driver]?.label || row.driver,
      ),
  },
  { prop: 'fee_percent', label: '手续费率', width: 100, sortable: true, sortMethod: byNumber('fee_percent'), formatter: (row) => `${row.fee_percent}%` },
  {
    prop: 'api_url',
    label: '地址',
    minWidth: 180,
    sortable: true,
    formatter: (row) =>
      h(
        'span',
        {
          title: row.api_url,
          style: 'color:var(--art-gray-600);font-family:ui-monospace,Menlo,monospace;font-size:12px',
        },
        row.api_url || '-',
      ),
  },
  { prop: 'sort', label: '排序', width: 70, sortable: true },
  {
    prop: 'enabled',
    label: '状态',
    width: 80,
    sortable: true,
    formatter: (row) =>
      h(
        ElTag,
        { type: row.enabled ? 'success' : 'info', size: 'small', effect: 'light' },
        () => (row.enabled ? '启用' : '停用'),
      ),
  },
  {
    prop: 'operation',
    label: '操作',
    width: 130,
    fixed: 'right',
    formatter: (row) =>
      h('div', [
        h(ArtButtonTable, { type: 'edit', onClick: () => openEdit(row) }),
        h(ArtButtonTable, { type: 'delete', onClick: () => del(row) }),
      ]),
  },
])

// 编辑时提示哪些密钥已配置（留空即保持不变）
const secretConfigured = computed(() => {
  const g = list.value.find((x) => x.code === form.code)
  if (!g) return {} as Record<string, boolean>
  return {
    key: !!g.has_key,
    private_key: !!g.has_private_key,
    public_key: !!g.has_public_key,
    api_v3_key: !!g.has_api_v3_key,
  }
})

const startRequest = useAdminRequest()
async function load() {
  const isCurrent = startRequest()
  if (!isCurrent) return
  loading.value = true
  try {
    const data = await fetchAdminGateways()
    if (isCurrent()) list.value = data
  } catch (err: unknown) {
    if (isCurrent()) ElMessage.error((err as Error).message || '查询失败')
  } finally {
    if (isCurrent()) loading.value = false
  }
}
onMounted(load)

function resetForm() {
  Object.assign(form, {
    code: '',
    driver: 'epay',
    name: '',
    fee_percent: '0',
    sort: '0',
    enabled: '1',
    api_url: '',
    pid: '',
    key: '',
    channel: '',
    channel_choice: 'alipay',
    payment_mode: 'redirect',
    mobile_qrcode: '0',
    app_id: '',
    mch_id: '',
    api_v3_key: '',
    cert_serial: '',
    public_key_id: '',
    h5_app_name: '',
    h5_app_url: '',
    private_key: '',
    public_key: '',
  })
}

// 切换驱动时把支付模式重置为该驱动的默认值：各驱动默认不同
// （易支付默认跳转、支付宝默认扫码），沿用上一个驱动的值可能选到不可用的模式。
function onDriverChange(driver: string) {
  const fallback = GATEWAY_DRIVERS[driver]?.paymentModeDefault
  if (fallback) form.payment_mode = fallback
}

function openNew() {
  resetForm()
  editing.value = false
  dialog.value = true
}

function openEdit(row: AdminGateway) {
  resetForm()
  const channel = row.channel || ''
  const isPreset = EPAY_CHANNELS.some((item) => item.value === channel)
  Object.assign(form, {
    code: row.code,
    driver: row.driver,
    name: row.name,
    fee_percent: row.fee_percent || '0',
    sort: String(row.sort ?? 0),
    enabled: row.enabled ? '1' : '0',
    api_url: row.api_url || '',
    pid: row.pid || '',
    channel,
    channel_choice: isPreset ? channel : EPAY_CUSTOM_CHANNEL,
    payment_mode: row.payment_mode || GATEWAY_DRIVERS[row.driver]?.paymentModeDefault || 'redirect',
    mobile_qrcode: row.mobile_qrcode || '0',
    app_id: row.app_id || '',
    mch_id: row.mch_id || '',
    // APIv3 密钥属密钥类字段，后台不回传，编辑时始终留空（留空即沿用旧值）
    api_v3_key: '',
    cert_serial: row.cert_serial || '',
    public_key_id: row.public_key_id || '',
    h5_app_name: row.h5_app_name || '',
    h5_app_url: row.h5_app_url || '',
  })
  editing.value = true
  dialog.value = true
}

async function save() {
  if (!form.code.trim() || !form.name.trim()) {
    ElMessage.warning('请填写实例编码与显示名称')
    return
  }
  saving.value = true
  try {
    if (form.driver === 'epay') {
      form.channel = form.channel_choice === EPAY_CUSTOM_CHANNEL ? String(form.channel).trim() : form.channel_choice
      if (!form.channel) {
        ElMessage.warning('请选择或填写支付渠道')
        saving.value = false
        return
      }
    }
    // 密钥类字段为空时后端沿用旧值（与 SSR 表单一致）
    const body: Record<string, string> = {}
    for (const [k, v] of Object.entries(form)) body[k] = String(v)
    await saveAdminGateway(body)
    ElMessage.success('已保存')
    dialog.value = false
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function del(row: AdminGateway) {
  const ok = await ElMessageBox.confirm(`确认删除支付网关「${row.name}」（${row.code}）？`, '删除网关', {
    type: 'warning',
    confirmButtonText: '删除',
    cancelButtonText: '取消',
  }).catch(() => null)
  if (!ok) return
  try {
    await deleteAdminGateway(row.code)
    ElMessage.success('已删除')
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '删除失败')
  }
}

// 上游网关侧配置的异步通知地址（旧 SSR 页同款提示，点击复制）
const CALLBACK_HINT = '/pay/notify?code=网关编码'
async function copyCallback() {
  if (await copyText(CALLBACK_HINT)) ElMessage.success('回调地址已复制')
  else ElMessage.error('复制失败，请手动复制')
}
</script>

<template>
  <div class="art-full-height">
    <ArtSearchBar
      v-show="showSearchBar"
      v-model="searchForm"
      :items="searchItems"
    />

    <div class="gw-hint" role="note" :style="{ marginTop: showSearchBar ? '12px' : '0' }">
      支持多个网关实例，排序数字越小越优先。回调地址（在上游网关侧配置）：
      <code title="点击复制" @click="copyCallback">{{ CALLBACK_HINT }}</code>
    </div>

    <ElCard class="art-table-card" style="margin-top: 12px">
      <ArtTableHeader
        v-model:columns="columns"
        v-model:showSearchBar="showSearchBar"
        :loading="loading"
        @refresh="load"
      >
        <template #left>
          <ElButton type="primary" @click="openNew">
            <el-icon><Plus /></el-icon>新增网关
          </ElButton>
        </template>
      </ArtTableHeader>

      <ArtTable :loading="loading" :data="filtered" :columns="columns" empty-text="暂无支付网关" />
    </ElCard>

    <el-dialog v-model="dialog" :title="editing ? '编辑网关' : '新增网关'" width="600px">
      <el-form label-position="top" autocomplete="off">
        <div class="admin-form-grid">
          <el-form-item label="实例编码" required><el-input v-model="form.code" :disabled="editing" placeholder="如 epay_main" /></el-form-item>
          <el-form-item label="显示名称" required><el-input v-model="form.name" placeholder="如 易支付主通道" /></el-form-item>
          <el-form-item label="插件类型"><el-select v-model="form.driver" class="w-full" @change="onDriverChange"><el-option v-for="d in gatewayDriverList()" :key="d.value" :label="d.label" :value="d.value" /></el-select></el-form-item>
          <el-form-item label="在线支付手续费率（%）"><el-input-number v-model="form.fee_percent" :min="0" :max="100" :precision="2" class="w-full" /></el-form-item>
        </div>

        <template v-if="visibleFields.length">
          <el-divider content-position="left">{{ GATEWAY_DRIVERS[form.driver]?.label }} 配置</el-divider>
          <el-form-item v-if="visibleFields.includes('api_url')" label="API 地址"><el-input v-model="form.api_url" placeholder="上游网关接口地址" /></el-form-item>
          <div class="admin-form-grid">
            <el-form-item v-if="visibleFields.includes('pid')" label="商户 PID"><el-input v-model="form.pid" placeholder="商户 PID" /></el-form-item>
            <el-form-item v-if="visibleFields.includes('channel')" label="支付渠道">
              <el-select v-model="form.channel_choice" class="w-full">
                <el-option v-for="item in EPAY_CHANNELS" :key="item.value" :label="item.label" :value="item.value" />
                <el-option label="自定义" :value="EPAY_CUSTOM_CHANNEL" />
              </el-select>
              <el-input v-if="isCustomChannel" v-model="form.channel" placeholder="请输入渠道代码，如 bank、jdpay" />
            </el-form-item>
            <el-form-item v-if="visibleFields.includes('app_id')" label="应用 ID（AppID）"><el-input v-model="form.app_id" placeholder="支付宝应用 ID / 微信 AppID" /></el-form-item>
            <el-form-item v-if="visibleFields.includes('mch_id')" label="商户号"><el-input v-model="form.mch_id" placeholder="微信支付商户号" /></el-form-item>
            <el-form-item v-if="visibleFields.includes('cert_serial')" label="商户证书序列号"><el-input v-model="form.cert_serial" placeholder="商户 API 证书序列号" /></el-form-item>
          </div>
          <el-form-item v-if="visibleFields.includes('payment_mode')" label="支付模式">
            <el-select v-model="form.payment_mode" class="w-full">
              <el-option label="跳转模式（托管收银台）" value="redirect" />
              <el-option label="扫码模式（本地二维码页）" value="qrcode" />
            </el-select>
            <p v-if="GATEWAY_DRIVERS[form.driver]?.paymentModeHint" class="form-tip">
              {{ GATEWAY_DRIVERS[form.driver]?.paymentModeHint }}
            </p>
          </el-form-item>
          <el-form-item v-if="visibleFields.includes('mobile_qrcode')" label="手机端也扫码">
            <el-switch v-model="form.mobile_qrcode" active-value="1" inactive-value="0" active-text="启用" />
            <p class="form-tip">手机端不跳转，直接显示二维码。仅当未签约「手机网站支付」时开启，否则手机用户无法付款。</p>
          </el-form-item>
          <!-- 密钥类输入统一用 new-password：否则浏览器会把相邻文本框当成"用户名"、把这里
               当成"登录密码"配对保存并自动填充，把支付密钥写进浏览器的密码库。 -->
          <el-form-item v-if="visibleFields.includes('key')" label="商户密钥"><el-input v-model="form.key" type="password" show-password autocomplete="new-password" :placeholder="secretConfigured.key ? '已配置，留空保持不变' : '商户密钥'" /></el-form-item>
          <el-form-item v-if="visibleFields.includes('private_key')" label="私钥（应用 / 商户 API）"><el-input v-model="form.private_key" type="textarea" :rows="3" :placeholder="secretConfigured.private_key ? '已配置，留空保持不变' : '-----BEGIN PRIVATE KEY-----'" /></el-form-item>
          <el-form-item v-if="visibleFields.includes('public_key')" label="平台公钥（用于回调验签）"><el-input v-model="form.public_key" type="textarea" :rows="3" :placeholder="secretConfigured.public_key ? '已配置，留空保持不变' : '-----BEGIN PUBLIC KEY-----'" /></el-form-item>
          <el-form-item v-if="visibleFields.includes('api_v3_key')" label="APIv3 密钥"><el-input v-model="form.api_v3_key" type="password" show-password autocomplete="new-password" :placeholder="secretConfigured.api_v3_key ? '已配置，留空保持不变' : '32 位 APIv3 密钥，用于回调解密'" /></el-form-item>
          <el-form-item v-if="visibleFields.includes('public_key_id')" label="微信支付公钥 ID"><el-input v-model="form.public_key_id" placeholder="如 PUB_KEY_ID_xxxxxxxxxx" /></el-form-item>
          <div v-if="visibleFields.includes('h5_app_name') || visibleFields.includes('h5_app_url')" class="admin-form-grid">
            <el-form-item v-if="visibleFields.includes('h5_app_name')" label="H5 应用名称（选填）"><el-input v-model="form.h5_app_name" placeholder="支付页展示的应用名" /></el-form-item>
            <el-form-item v-if="visibleFields.includes('h5_app_url')" label="H5 应用域名（选填）"><el-input v-model="form.h5_app_url" placeholder="如 https://example.com" /></el-form-item>
          </div>
        </template>

        <div class="admin-form-grid">
          <el-form-item label="排序（小在前）"><el-input-number v-model="form.sort" :min="0" class="w-full" /></el-form-item>
          <el-form-item label="状态"><el-switch v-model="form.enabled" active-value="1" inactive-value="0" active-text="启用" /></el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="dialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存</el-button></template>
    </el-dialog>
  </div>
</template>

<style scoped>
.gw-hint {
  padding: 10px 14px;
  color: var(--art-gray-600);
  font-size: 12.5px;
  background: var(--art-gray-50);
  border: 1px solid var(--art-card-border);
  border-radius: var(--custom-radius);
}

.gw-hint code {
  padding: 2px 7px;
  color: var(--theme-color-deep);
  font-family: ui-monospace, Menlo, monospace;
  font-size: 11.5px;
  cursor: pointer;
  background: var(--theme-color-soft);
  border-radius: 5px;
}

.admin-form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}
@media (max-width: 640px) {
  .admin-form-grid {
    grid-template-columns: 1fr;
  }
}

/* 表单字段下方的补充说明（如支付模式的前置条件） */
.form-tip {
  width: 100%;
  margin: 6px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
  line-height: 1.5;
}
</style>
