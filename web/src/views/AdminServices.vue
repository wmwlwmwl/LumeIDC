<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, h } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, ElTag, ElButton } from 'element-plus'
import type { ColumnOption } from '@/types'
import ArtButtonTable from '../components/core/forms/art-button-table/index.vue'
import {
  fetchAdminServices,
  fetchUsers,
  updateAdminService,
  fetchServiceRecovery,
  confirmServiceRecovery,
  type AdminService,
  type AdminProductOption,
  type RecoverySummary,
} from '../admin/api'
import { http } from '../http/index'
import { useAdminRequest } from '../admin/useAdminTable'

const list = ref<AdminService[]>([])
const router = useRouter()
const products = ref<AdminProductOption[]>([])
const loading = ref(false)
const showSearchBar = ref(true)
const searchForm = ref<{ q: string; status: string; product_id: number | '' }>({
  q: '',
  status: '',
  product_id: '',
})
let timer: ReturnType<typeof setTimeout> | undefined
let retryTimer: ReturnType<typeof setTimeout> | undefined
let disposed = false
let inFlight = 0

const searchItems = computed(() => [
  { label: '关键词', key: 'q', type: 'input', placeholder: '搜索用户 / 服务名 / 产品', clearable: true },
  {
    label: '产品',
    key: 'product_id',
    type: 'select',
    props: {
      placeholder: '全部产品',
      clearable: true,
      options: products.value.map((p) => ({ label: p.name, value: p.id })),
    },
  },
  {
    label: '状态',
    key: 'status',
    type: 'select',
    props: {
      placeholder: '全部状态',
      clearable: true,
      options: [
        { label: '待开通', value: '0' },
        { label: '激活', value: '1' },
        { label: '已停机', value: '2' },
      ],
    },
  },
])

const statusType: Record<string, 'success' | 'warning' | 'info'> = {
  激活: 'success',
  已停机: 'warning',
  待开通: 'info',
}

// 排序辅助：列表为全量数据，纯前端排序（Element Plus 列 sortable）。
// 金额/编号字段是字符串，字典序会 9>10，需按数值比较；状态按业务顺序排。
const byNumber =
  (key: 'monthly' | 'profit' | 'upstream') =>
  (a: AdminService, b: AdminService): number =>
    Number(a[key] || 0) - Number(b[key] || 0)
const STATUS_RANK: Record<string, number> = { 待开通: 0, 激活: 1, 已停机: 2 }

const columns = ref<ColumnOption[]>([
  { prop: 'id', label: 'ID', width: 64, sortable: true },
  { prop: 'user', label: '用户', minWidth: 150, sortable: true },
  { prop: 'product', label: '产品', minWidth: 140, sortable: true },
  { prop: 'hostname', label: '主机名', minWidth: 120, sortable: true, formatter: (row) => row.hostname || '-' },
  { prop: 'config_desc', label: '配置', minWidth: 150, sortable: true, formatter: (row) => row.config_desc || '-' },
  {
    prop: 'status_text',
    label: '状态',
    width: 90,
    sortable: true,
    sortMethod: (a: AdminService, b: AdminService) => (STATUS_RANK[a.status_text] ?? 9) - (STATUS_RANK[b.status_text] ?? 9),
    formatter: (row) =>
      h(ElTag, { type: statusType[row.status_text] || 'info', size: 'small' }, () => row.status_text),
  },
  { prop: 'expires', label: '到期', width: 110, sortable: true },
  {
    prop: 'days_left',
    label: '剩余',
    width: 80,
    sortable: true,
    formatter: (row) =>
      row.days_left > 0 ? `${row.days_left}天` : row.days_left === 0 ? '今天' : '已到期',
  },
  { prop: 'monthly', label: '月价', width: 90, sortable: true, sortMethod: byNumber('monthly'), formatter: (row) => `￥${row.monthly || '-'}` },
  { prop: 'upstream', label: '上游', width: 80, sortable: true, sortMethod: byNumber('upstream'), formatter: (row) => (row.upstream ? `#${row.upstream}` : '-') },
  { prop: 'prov_err', label: '失败原因', minWidth: 120, sortable: true, formatter: (row) => row.prov_err || '-' },
  { prop: 'profit', label: '利润', width: 90, sortable: true, sortMethod: byNumber('profit'), formatter: (row) => (row.profit ? `￥${row.profit}` : '-') },
  {
    prop: 'operation',
    label: '操作',
    width: 280,
    fixed: 'right',
    formatter: (row) => {
      // 管理入口：进后台版服务详情页（复用用户端页面，接口走 /admin/services/{id}/...）
      const buttons = [
        h(ArtButtonTable, {
          icon: 'ri:settings-3-line',
          iconClass: 'bg-theme/12 text-theme',
          title: '管理该服务',
          onClick: () => router.push(`/services/${row.id}`),
        }),
        h(ArtButtonTable, {
          icon: 'ri:edit-line',
          iconClass: 'bg-theme/12 text-theme',
          title: '编辑（配置 / 归属用户 / 到期 / 续费价）',
          onClick: () => openEdit(row),
        }),
      ]
      if (statusValueOf(row) === '待开通' && row.prov_err) {
        buttons.push(
          h(ArtButtonTable, {
            icon: 'ri:refresh-line',
            iconClass: 'bg-theme/12 text-theme',
            title: '重试开通（账单已失效会自动重新结算并支付）',
            onClick: () => act(row, 'retry'),
          }),
          h(ArtButtonTable, {
            icon: 'ri:refund-2-line',
            iconClass: 'bg-error/12 text-error',
            title: '退款给用户（全额退回订单实付，订单作废、服务终止）',
            onClick: () => act(row, 'refund_pending'),
          }),
        )
      }
      // 升级卡在上游改价/账单被删/余额不足：等管理员选按上游新价开通，还是退款给用户
      if (row.transition === 'upgrading' && row.prov_err) {
        buttons.push(
          h(ArtButtonTable, {
            icon: 'ri:refresh-line',
            iconClass: 'bg-theme/12 text-theme',
            title: '重试升级（复用上游已结算账单，按上游当前价继续开通）',
            onClick: () => act(row, 'retry_upgrade'),
          }),
          h(ArtButtonTable, {
            icon: 'ri:refund-2-line',
            iconClass: 'bg-error/12 text-error',
            title: '退款给用户（退回升级差价，服务保持原配置）',
            onClick: () => act(row, 'refund_upgrade'),
          }),
        )
      }
      // 续费卡在上游改价/账单被删/欠费：等管理员选按上游新价续费，还是退款给用户
      if (row.transition === 'renew_pending' && row.prov_err) {
        buttons.push(
          h(ArtButtonTable, {
            icon: 'ri:refresh-line',
            iconClass: 'bg-theme/12 text-theme',
            title: '重试续费（按上游当前价继续续费至上游）',
            onClick: () => act(row, 'retry_renew'),
          }),
          h(ArtButtonTable, {
            icon: 'ri:refund-2-line',
            iconClass: 'bg-error/12 text-error',
            title: '退款给用户（退回续费金额，并撤回本地已延长的周期）',
            onClick: () => act(row, 'refund_renew'),
          }),
        )
      }
      // "上游结果未知"的隔离任务：需管理员在上游核对后走对账恢复对话框（不调上游、不动资金）
      if (row.recovery && row.prov_err) {
        buttons.push(
          h(ArtButtonTable, {
            icon: 'ri:shield-check-line',
            iconClass: 'bg-theme/12 text-theme',
            title: '对账恢复（上游结果未知，请先在上游核对账单与实例）',
            onClick: () => openRecover(row),
          }),
        )
      }
      if (statusValueOf(row) === '激活') {
        buttons.push(
          h(ArtButtonTable, {
            icon: 'ri:pause-circle-line',
            iconClass: 'bg-warning/12 text-warning',
            onClick: () => act(row, 'suspend'),
          }),
        )
      }
      if (statusValueOf(row) === '已停机') {
        buttons.push(
          h(ArtButtonTable, {
            icon: 'ri:play-circle-line',
            iconClass: 'bg-success/12 text-success',
            onClick: () => act(row, 'unsuspend'),
          }),
        )
      }
      buttons.push(
        h(ArtButtonTable, { type: 'delete', onClick: () => act(row, 'terminate') }),
      )
      return h('div', buttons)
    },
  },
])

const startRequest = useAdminRequest()
async function load(silent = false) {
  const isCurrent = startRequest()
  if (!isCurrent) return
  clearTimeout(timer)
  inFlight++
  if (!silent) loading.value = true
  try {
    const res = await fetchAdminServices({
      q: searchForm.value.q,
      status: searchForm.value.status,
      product_id: searchForm.value.product_id === '' ? undefined : Number(searchForm.value.product_id),
    })
    if (!isCurrent()) return
    list.value = res.list
    products.value = res.products
  } catch (err: unknown) {
    if (isCurrent() && !silent) ElMessage.error((err as Error).message || '查询失败')
  } finally {
    if (isCurrent()) loading.value = false
    inFlight--
    // 所有在途查询结束后再等 10 秒；用户搜索不受轮询锁限制。
    if (!disposed && inFlight === 0) timer = setTimeout(() => load(true), 10000)
  }
}
onMounted(() => load())
onBeforeUnmount(() => {
  disposed = true
  clearTimeout(timer)
  clearTimeout(retryTimer)
})

function handleSearch() {
  load()
}

function handleReset() {
  searchForm.value = { q: '', status: '', product_id: '' }
  load()
}

const ACTIONS: Record<string, { label: string; confirm?: (row: AdminService) => string; done?: string }> = {
  // 重试类动作只是把任务入队（异步执行），回执只代表"已提交"，不能提示成功。
  // 二次确认只在「上游改价」时弹：按新价继续会少赚，点错钱就付了；
  // 余额不足这类重试价格没变，不打扰。
  retry: {
    label: '重试开通',
    done: '重试已提交，开通结果稍后自动刷新',
    confirm: (row) =>
      row.price_changed
        ? `上游价格已变动：本次重试将按上游新价继续开通服务 #${row.id}（${row.product}），` +
          `复用上游已结算的账单直接付款，利润会变薄甚至持平。` +
          `如需改为退款给用户，请取消后点「退款给用户」。`
        : '',
  },
  refund_pending: {
    label: '退款给用户',
    confirm: (row) =>
      `确认关闭服务 #${row.id}（${row.product}）并全额退款？` +
      `将把该订单实付金额全额退回用户余额，订单作废、服务终止，且不可恢复。` +
      `仅在确认不再为用户开通（如上游已涨价）时使用。`,
  },
  retry_upgrade: {
    label: '重试升级',
    done: '重试已提交，处理结果稍后自动刷新',
    confirm: (row) =>
      row.price_changed
        ? `上游价格已变动：本次重试将按上游新价继续升级服务 #${row.id}（${row.product}），` +
          `复用上游已结算的账单直接付款，利润会变薄甚至持平。` +
          `如需改为退款给用户，请取消后点「退款给用户」。`
        : '',
  },
  refund_upgrade: {
    label: '退款给用户',
    confirm: (row) =>
      `确认取消服务 #${row.id}（${row.product}）的升级并退款？` +
      `将把升级订单实付差额退回用户（余额支付的退回余额，第三方支付需线下退渠道），` +
      `订单作废，服务保持原产品继续可用。`,
  },
  retry_renew: {
    label: '重试续费',
    done: '重试已提交，处理结果稍后自动刷新',
    confirm: (row) =>
      row.price_changed
        ? `上游价格已变动：本次重试将按上游新价继续为服务 #${row.id}（${row.product}）续费，` +
          `跳过续费前比价向上游续期，利润会变薄甚至持平。` +
          `如需改为退款给用户，请取消后点「退款给用户」。`
        : '',
  },
  refund_renew: {
    label: '退款给用户',
    confirm: (row) =>
      `确认取消服务 #${row.id}（${row.product}）的本次续费并退款？` +
      `将退回续费订单实付（余额支付的退回余额，第三方支付需线下退渠道），订单作废，` +
      `并把本地已延长的到期时间撤回一个周期。`,
  },
  suspend: { label: '停机', confirm: (row) => `确认停机服务 #${row.id}（${row.product}）？停机后用户将无法使用该实例。` },
  unsuspend: { label: '解除', confirm: (row) => `确认解除服务 #${row.id}（${row.product}）的停机状态？` },
  terminate: { label: '删除', confirm: (row) => `确认删除服务 #${row.id}？已绑定上游时会同步销毁，操作不可恢复。` },
}

// ---------- 对账恢复（重引擎）：展示后端白名单证据 + 人工举证表单，解除任务隔离 ----------
// 全程不调上游、不动资金；提交体字段与后端 RecoveryConfirmation 一一对应。
const recoverVisible = ref(false)
const recoverLoading = ref(false)
const recoverSubmitting = ref(false)
const recoverRow = ref<AdminService | null>(null)
const recoverSummary = ref<RecoverySummary | null>(null)
const recoverForm = ref({
  decision: '' as '' | 'confirmed_completed' | 'confirmed_not_executed',
  evidence: '',
  remote_stable: false,
  billing_verified: false,
  delivery_verified: false,
  verified_host_id: '' as number | '',
})
const RECOVERY_KIND_LABELS: Record<string, string> = { provision: '开通', renew: '续费', upgrade: '升级' }

// 需要填写"核实的主机 ID"的口径与后端校验一致：确认已生效，或开通以外的类型（续费/升级）。
const needHostId = computed(
  () =>
    recoverForm.value.decision === 'confirmed_completed' ||
    (recoverSummary.value?.kind ?? '') !== 'provision',
)

async function openRecover(row: AdminService) {
  recoverRow.value = row
  recoverSummary.value = null
  recoverForm.value = {
    decision: '',
    evidence: '',
    remote_stable: false,
    billing_verified: false,
    delivery_verified: false,
    verified_host_id: '',
  }
  recoverVisible.value = true
  recoverLoading.value = true
  try {
    recoverSummary.value = await fetchServiceRecovery(row.id)
    // 后端已记录的绑定主机直接预填，减少手抄出错。
    if (recoverSummary.value.host_id > 0) recoverForm.value.verified_host_id = recoverSummary.value.host_id
  } catch (err: unknown) {
    recoverVisible.value = false
    ElMessage.error((err as Error).message || '未找到可核对的隔离任务')
    await load(true)
  } finally {
    recoverLoading.value = false
  }
}

async function submitRecover() {
  const row = recoverRow.value
  const sum = recoverSummary.value
  if (!row || !sum || sum.block_reason) return
  const f = recoverForm.value
  if (f.decision !== 'confirmed_completed' && f.decision !== 'confirmed_not_executed') {
    ElMessage.error('请选择对账决策（上游已生效 / 上游未执行）')
    return
  }
  const evidence = f.evidence.trim()
  if (evidence.length < 10) {
    ElMessage.error('请填写至少十字的核对证据（勿含密码）')
    return
  }
  if (!f.remote_stable || !f.billing_verified || !f.delivery_verified) {
    ElMessage.error('请逐项勾选确认：远端已稳定、账单已核对、交付已核对')
    return
  }
  let hostId = 0
  if (needHostId.value) {
    hostId = Number(f.verified_host_id)
    if (!Number.isInteger(hostId) || hostId <= 0) {
      ElMessage.error('请填写正整数的核实主机 ID（在上游核实到的实例标识）')
      return
    }
  }
  recoverSubmitting.value = true
  try {
    await confirmServiceRecovery(row.id, {
      job_id: sum.job_id,
      expected_version: sum.version,
      decision: f.decision,
      evidence,
      verified_host_id: hostId,
      remote_stable: f.remote_stable,
      billing_verified: f.billing_verified,
      delivery_verified: f.delivery_verified,
    })
    ElMessage.success('对账恢复已提交')
    recoverVisible.value = false
    await load(true)
    if (f.decision === 'confirmed_not_executed' && !disposed) {
      // 未执行路径会立刻重新入队：3 秒后补刷一次，不用等满 10 秒轮询。
      clearTimeout(retryTimer)
      retryTimer = setTimeout(() => load(true), 3000)
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '对账恢复失败')
  } finally {
    recoverSubmitting.value = false
  }
}

async function act(row: AdminService, do_: keyof typeof ACTIONS) {
  const def = ACTIONS[do_]
  // confirm 返回空串表示这次不需要确认（如普通的重试，价格没变就直接提交）
  const ask = def.confirm ? def.confirm(row) : ''
  if (ask) {
    const ok = await ElMessageBox.confirm(ask, def.label, {
      type: do_ === 'terminate' ? 'warning' : 'info',
      confirmButtonText: '执行',
      cancelButtonText: '取消',
    }).catch(() => null)
    if (!ok) return
  }
  try {
    const res = (await http.post(`/services/${row.id}/action`, { do: do_ })) as { ok: number; msg?: string }
    if (String(res.ok) === '1') ElMessage.success(def.done || `${def.label}成功`)
    else ElMessage.error(res.msg || `${def.label}失败`)
    await load(true)
    // 带 done 文案的都是"入队即返回"的异步动作：服务端入队后会立刻催一次队列，
    // 3 秒后补刷一次，省得等满 10 秒的轮询才看到结果。
    if (def.done && !disposed) {
      clearTimeout(retryTimer)
      retryTimer = setTimeout(() => load(true), 3000)
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || `${def.label}失败`)
  }
}

function statusValueOf(row: AdminService): '待开通' | '激活' | '已停机' {
  return row.status_text as '待开通' | '激活' | '已停机'
}

// ---------- 编辑服务（换归属用户 / 到期时间 / 固定续费价） ----------
const editVisible = ref(false)
const savingEdit = ref(false)
const editing = ref<AdminService | null>(null)
const editForm = ref<{ user_id: number | ''; expires_at: string; config_desc: string; renew_monthly: string; renew_quarterly: string; renew_yearly: string }>({
  user_id: '',
  expires_at: '',
  config_desc: '',
  renew_monthly: '',
  renew_quarterly: '',
  renew_yearly: '',
})
const userOptions = ref<{ id: number; email: string }[]>([])
const userSearching = ref(false)

// 远程搜索用户（按邮箱/名称），用于换归属
async function searchUsers(q: string) {
  if (!q.trim()) return
  userSearching.value = true
  try {
    const res = await fetchUsers(q.trim(), 1)
    userOptions.value = res.list.map((u) => ({ id: u.id, email: u.email }))
  } catch {
    /* 搜索失败保持原选项 */
  } finally {
    userSearching.value = false
  }
}

function openEdit(row: AdminService) {
  editing.value = row
  userOptions.value = [{ id: row.user_id, email: row.user }]
  editForm.value = {
    user_id: row.user_id,
    expires_at: row.expires,
    config_desc: row.config_desc || '',
    renew_monthly: row.renew_monthly || '',
    renew_quarterly: row.renew_quarterly || '',
    renew_yearly: row.renew_yearly || '',
  }
  editVisible.value = true
}

async function saveEdit() {
  const row = editing.value
  if (!row) return
  const f = editForm.value
  const payload: Record<string, string> = {
    renew_monthly: f.renew_monthly.trim(),
    renew_quarterly: f.renew_quarterly.trim(),
    renew_yearly: f.renew_yearly.trim(),
  }
  for (const k of ['renew_monthly', 'renew_quarterly', 'renew_yearly'] as const) {
    const v = payload[k]
    if (v !== '' && (Number.isNaN(Number(v)) || Number(v) < 0)) {
      ElMessage.error('续费价必须是非负数字')
      return
    }
  }
  if (f.user_id !== '' && f.user_id !== row.user_id) payload.user_id = String(f.user_id)
  if (f.expires_at && f.expires_at !== row.expires) payload.expires_at = f.expires_at
  // 配置：与列表当前显示值不同才提交（空串=清除手工配置，恢复自动生成）
  if (f.config_desc.trim() !== (row.config_desc || '')) payload.config_desc = f.config_desc.trim()
  if (Object.keys(payload).length === 3) {
    editVisible.value = false
    return
  }
  savingEdit.value = true
  try {
    await updateAdminService(row.id, payload)
    ElMessage.success('已保存')
    editVisible.value = false
    await load(true)
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    savingEdit.value = false
  }
}
</script>

<template>
  <div class="art-full-height">
    <ArtSearchBar
      v-show="showSearchBar"
      v-model="searchForm"
      :items="searchItems"
      @search="handleSearch"
      @reset="handleReset"
    />

    <ElCard class="art-table-card" :style="{ marginTop: showSearchBar ? '12px' : '0' }">
      <ArtTableHeader
        v-model:columns="columns"
        v-model:showSearchBar="showSearchBar"
        :loading="loading"
        @refresh="load()"
      >
        <template #left>
          <span class="admin-count">共 <b>{{ list.length }}</b> 个实例（每 10 秒自动刷新）</span>
        </template>
      </ArtTableHeader>

      <ArtTable :loading="loading" :data="list" :columns="columns" />
    </ElCard>

    <!-- 编辑服务：换归属用户 / 到期时间 / 固定续费价（对齐魔方财务服务编辑） -->
    <ElDialog v-model="editVisible" title="编辑服务" width="520px" append-to-body>
      <ElForm label-width="110px" @submit.prevent>
        <ElFormItem label="归属用户">
          <ElSelect
            v-model="editForm.user_id"
            filterable
            remote
            :remote-method="searchUsers"
            :loading="userSearching"
            placeholder="输入邮箱搜索用户"
            style="width: 100%"
          >
            <ElOption v-for="u in userOptions" :key="u.id" :label="`${u.email}（#${u.id}）`" :value="u.id" />
          </ElSelect>
        </ElFormItem>
        <ElFormItem label="到期时间">
          <ElDatePicker
            v-model="editForm.expires_at"
            type="date"
            value-format="YYYY-MM-DD"
            placeholder="留空则不修改"
            style="width: 100%"
          />
        </ElFormItem>
        <ElFormItem label="配置">
          <ElInput
            v-model="editForm.config_desc"
            type="textarea"
            :autosize="{ minRows: 2, maxRows: 4 }"
            placeholder="留空 = 按产品配置项自动生成；填写后列表/用户端直接展示该内容"
          />
        </ElFormItem>
        <ElFormItem label="月续费价">
          <ElInput v-model="editForm.renew_monthly" placeholder="留空 = 跟随产品价" clearable />
        </ElFormItem>
        <ElFormItem label="季续费价">
          <ElInput v-model="editForm.renew_quarterly" placeholder="留空 = 跟随产品价" clearable />
        </ElFormItem>
        <ElFormItem label="年续费价">
          <ElInput v-model="editForm.renew_yearly" placeholder="留空 = 跟随产品价" clearable />
        </ElFormItem>
      </ElForm>
      <template #footer>
        <ElButton @click="editVisible = false">取消</ElButton>
        <ElButton type="primary" :loading="savingEdit" @click="saveEdit">保存</ElButton>
      </template>
    </ElDialog>

    <!-- 对账恢复：展示后端白名单证据摘要 + 人工举证表单（决策 / 证据 / 三项确认 / 核实主机 ID） -->
    <ElDialog v-model="recoverVisible" title="对账恢复" width="620px" append-to-body @closed="recoverSummary = null">
      <div v-if="recoverLoading" class="recover-tip">正在读取隔离任务证据…</div>
      <ElAlert
        v-else-if="recoverSummary?.block_reason"
        type="error"
        :closable="false"
        show-icon
        :title="recoverSummary.block_reason"
        description="证据存在冲突或不一致，禁止解除隔离；请先处理对应问题，再回来重新核对。"
      />
      <ElForm v-else-if="recoverSummary" label-width="130px" @submit.prevent>
        <ElAlert
          v-if="!recoverSummary.can_resume"
          type="warning"
          :closable="false"
          show-icon
          :title="recoverSummary.resume_reason"
          class="recover-alert"
        />
        <ElFormItem label="任务类型">
          {{ RECOVERY_KIND_LABELS[recoverSummary.kind] || recoverSummary.kind }}
          <span class="recover-muted">（任务 #{{ recoverSummary.job_id }} / 版本 {{ recoverSummary.version }}）</span>
        </ElFormItem>
        <ElFormItem label="订单号">
          #{{ recoverSummary.order_id }}（{{ recoverSummary.cycle }}）
        </ElFormItem>
        <ElFormItem label="账单状态">
          <template v-if="recoverSummary.invoice_status === 1">已支付 ￥{{ recoverSummary.amount }}</template>
          <template v-else>未支付（账单状态 {{ recoverSummary.invoice_status }}）</template>
          <span v-if="recoverSummary.upstream_invoice" class="recover-muted">（上游账单：{{ recoverSummary.upstream_invoice }}）</span>
        </ElFormItem>
        <ElFormItem label="上游主机">
          {{ recoverSummary.host_id > 0 ? `#${recoverSummary.host_id}` : '未绑定' }}
          <span v-if="recoverSummary.checkpoint_host" class="recover-muted">（检查点：{{ recoverSummary.checkpoint_host }}）</span>
        </ElFormItem>
        <ElFormItem label="上游账户">{{ recoverSummary.account || '-' }}</ElFormItem>
        <ElFormItem label="未决任务数">{{ recoverSummary.pending_jobs }}</ElFormItem>
        <ElFormItem label="对账决策">
          <ElRadioGroup v-model="recoverForm.decision">
            <ElRadio value="confirmed_completed">上游已生效（补记本地终态）</ElRadio>
            <ElRadio value="confirmed_not_executed" :disabled="!recoverSummary.can_resume">上游未执行（任务重新排队）</ElRadio>
          </ElRadioGroup>
        </ElFormItem>
        <ElFormItem v-if="needHostId" label="核实的主机 ID">
          <ElInput
            v-model="recoverForm.verified_host_id"
            placeholder="在上游核实到的主机 / 站点 ID（正整数）"
            style="width: 260px"
          />
        </ElFormItem>
        <ElFormItem label="核对证据">
          <ElInput
            v-model="recoverForm.evidence"
            type="textarea"
            :autosize="{ minRows: 3, maxRows: 6 }"
            :maxlength="2000"
            show-word-limit
            placeholder="填写在上游核对到的证据（至少 10 字，如账单号、实例 ID、站点状态）。请勿包含密码等敏感信息。"
          />
        </ElFormItem>
        <ElFormItem label="人工确认">
          <div class="recover-checks">
            <ElCheckbox v-model="recoverForm.remote_stable">远端已稳定（实例 / 续费状态不再变动）</ElCheckbox>
            <ElCheckbox v-model="recoverForm.billing_verified">账单已核对（无待付账单与退款冲突）</ElCheckbox>
            <ElCheckbox v-model="recoverForm.delivery_verified">交付已核对（用户可正常使用）</ElCheckbox>
          </div>
        </ElFormItem>
      </ElForm>
      <template #footer>
        <ElButton @click="recoverVisible = false">取消</ElButton>
        <ElButton
          type="primary"
          :loading="recoverSubmitting"
          :disabled="recoverLoading || !recoverSummary || !!recoverSummary.block_reason"
          @click="submitRecover"
        >
          提交对账恢复
        </ElButton>
      </template>
    </ElDialog>
  </div>
</template>

<style scoped>
.admin-count {
  color: var(--art-gray-600);
  font-size: 13px;
}
.admin-count b {
  color: var(--theme-color-deep);
}
.recover-tip {
  color: var(--art-gray-600);
  padding: 8px 0;
}
.recover-alert {
  margin-bottom: 12px;
}
.recover-muted {
  color: var(--art-gray-600);
  font-size: 12px;
}
.recover-checks {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
</style>
