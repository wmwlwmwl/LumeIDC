<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, ElTag, ElDropdown, ElDropdownMenu, ElDropdownItem } from 'element-plus'
import type { ColumnOption } from '@/types'
import ArtButtonTable from '../components/core/forms/art-button-table/index.vue'
import { fetchUsers, setUserStatus, rechargeUser, refundUser, type AdminUser } from '../admin/api'

const router = useRouter()
const loading = ref(false)
const list = ref<AdminUser[]>([])
const total = ref(0)
const page = ref(1)
const per = 25
const showSearchBar = ref(true)
const searchForm = ref<{ q: string; status: string }>({ q: '', status: '' })

// 服务端分页：排序交给后端 ORDER BY（字段经白名单映射）
const sortKey = ref('')
const sortOrder = ref<'asc' | 'desc'>('desc')
function handleSortChange({ prop, order }: { prop: string; order: 'ascending' | 'descending' | null }) {
  sortKey.value = order ? prop : ''
  sortOrder.value = order === 'ascending' ? 'asc' : 'desc'
  page.value = 1
  load()
}

const searchItems = [
  { label: '关键词', key: 'q', type: 'input', placeholder: '搜索邮箱 / 姓名 / 手机', clearable: true },
  {
    label: '状态',
    key: 'status',
    type: 'select',
    props: {
      placeholder: '全部状态',
      clearable: true,
      options: [
        { label: '正常', value: 'active' },
        { label: '禁用', value: 'disabled' },
      ],
    },
  },
]

const REALNAME_META: Record<string, { text: string; type: 'success' | 'warning' | 'info' }> = {
  approved: { text: '已实名', type: 'success' },
  pending: { text: '待审核', type: 'warning' },
  none: { text: '未实名', type: 'info' },
}

const columns = ref<ColumnOption[]>([
  { prop: 'id', label: 'ID', width: 64, sortable: 'custom' },
  { prop: 'email', label: '邮箱', minWidth: 180, sortable: 'custom' },
  { prop: 'name', label: '名称', minWidth: 120, sortable: 'custom', formatter: (row) => row.name || '-' },
  {
    prop: 'phone',
    label: '手机号',
    width: 150,
    sortable: 'custom',
    formatter: (row) => h('span', { style: 'white-space:nowrap' }, row.phone || '-'),
  },
  {
    prop: 'realname',
    label: '实名',
    width: 90,
    formatter: (row) => {
      const meta = REALNAME_META[row.realname] || REALNAME_META.none
      return h(ElTag, { type: meta.type, size: 'small', effect: 'light' }, () => meta.text)
    },
  },
  {
    prop: 'balance',
    label: '余额',
    width: 110,
    sortable: 'custom',
    formatter: (row) =>
      h('b', { style: 'color: var(--art-gray-800); font-weight: 650' }, `￥${row.balance}`),
  },
  {
    prop: 'status',
    label: '状态',
    width: 90,
    sortable: 'custom',
    formatter: (row) =>
      h(
        ElTag,
        { type: row.disabled ? 'danger' : 'success', size: 'small', effect: 'light' },
        () => row.status,
      ),
  },
  { prop: 'created_at', label: '注册时间', width: 170, sortable: 'custom' },
  {
    prop: 'operation',
    label: '操作',
    width: 140,
    fixed: 'right',
    formatter: (row) =>
      h('div', { class: 'flex-c' }, [
        h(ArtButtonTable, { type: 'edit', onClick: () => openEdit(row) }),
        h(ArtButtonTable, {
          icon: row.disabled ? 'ri:play-circle-line' : 'ri:forbid-2-line',
          iconClass: row.disabled ? 'bg-success/12 text-success' : 'bg-warning/12 text-warning',
          onClick: () => toggleStatus(row),
        }),
        h(
          ElDropdown,
          {
            trigger: 'click',
            onCommand: (cmd: string) => {
              if (cmd === 'recharge') openBalance(row, 'recharge')
              else if (cmd === 'refund') openBalance(row, 'refund')
            },
          },
          {
            default: () => h(ArtButtonTable, { type: 'more' }),
            dropdown: () =>
              h(ElDropdownMenu, {}, () => [
                h(ElDropdownItem, { command: 'recharge' }, () => '充值'),
                h(ElDropdownItem, { command: 'refund' }, () => '退款'),
              ]),
          },
        ),
      ]),
  },
])

const pagination = computed(() => ({ current: page.value, size: per, total: total.value }))

// 状态筛选在当前页数据上做（列表本身为服务端分页）
const filtered = computed(() => {
  const st = searchForm.value.status
  if (!st) return list.value
  return list.value.filter((u) => (st === 'disabled' ? u.disabled : !u.disabled))
})

async function load() {
  loading.value = true
  try {
    const res = await fetchUsers(
      searchForm.value.q.trim(),
      page.value,
      sortKey.value || undefined,
      sortKey.value ? sortOrder.value : undefined,
    )
    list.value = res.list
    total.value = res.total
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '查询失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

function handleSearch() {
  page.value = 1
  load()
}

function handleReset() {
  searchForm.value = { q: '', status: '' }
  page.value = 1
  load()
}

function handleCurrentChange(p: number) {
  page.value = p
  load()
}

function openEdit(row: AdminUser) {
  router.push(`/users/${row.id}/edit`)
}

async function toggleStatus(row: AdminUser) {
  const disable = !row.disabled
  const action = disable ? '禁用' : '启用'
  const confirmed = await ElMessageBox.confirm(
    `确认${action}用户「${row.email}」？${disable ? '禁用后其登录会话会被吊销。' : ''}`,
    `${action}用户`,
    { type: 'warning', confirmButtonText: action, cancelButtonText: '取消' },
  ).catch(() => false)
  if (!confirmed) return
  try {
    const res = await setUserStatus(row.id, disable)
    ElMessage.success(res.msg)
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '操作失败')
  }
}

// ---- 充值 / 退款 ----
const opVisible = ref(false)
const opKind = ref<'recharge' | 'refund'>('recharge')
const opUser = ref<AdminUser | null>(null)
const opAmount = ref('')
const opNote = ref('')
const opSaving = ref(false)

const opTitle = computed(() => (opKind.value === 'recharge' ? '充值' : '退款'))

function openBalance(row: AdminUser, kind: 'recharge' | 'refund') {
  opKind.value = kind
  opUser.value = row
  opAmount.value = ''
  opNote.value = ''
  opVisible.value = true
}

function fillAll() {
  opAmount.value = opUser.value?.balance || '0.00'
}

async function submitBalance() {
  if (!opUser.value) return
  const amount = opAmount.value.trim()
  if (!/^\d+(\.\d{1,2})?$/.test(amount) || Number(amount) <= 0) {
    ElMessage.warning('请输入有效金额（最多两位小数且大于 0）')
    return
  }
  opSaving.value = true
  try {
    const fn = opKind.value === 'recharge' ? rechargeUser : refundUser
    const res = await fn(opUser.value.id, amount, opNote.value.trim())
    ElMessage.success(res.msg)
    opVisible.value = false
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '操作失败')
  } finally {
    opSaving.value = false
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
        @refresh="load"
      >
        <template #left>
          <span class="admin-count">共 <b>{{ total }}</b> 位用户</span>
        </template>
      </ArtTableHeader>

      <ArtTable
        :loading="loading"
        :data="filtered"
        :columns="columns"
        :pagination="pagination"
        @sort-change="handleSortChange"
        @pagination:current-change="handleCurrentChange"
      />
    </ElCard>

    <el-dialog v-model="opVisible" :title="opTitle" width="440px" align-center>
      <div v-if="opUser" class="bal-user">
        <span class="bal-user__email">{{ opUser.email }}</span>
        <span class="bal-user__balance">当前余额: ￥{{ opUser.balance }}</span>
      </div>
      <el-form label-position="top">
        <el-form-item :label="opKind === 'recharge' ? '充值金额' : '退款金额'" required>
          <div class="bal-amount">
            <el-input v-model="opAmount" type="number" placeholder="0.00" @keyup.enter="submitBalance" />
            <el-button v-if="opKind === 'refund'" @click="fillAll">全部</el-button>
          </div>
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="opNote" type="textarea" :rows="2" placeholder="选填" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="opVisible = false">取消</el-button>
        <el-button type="primary" :loading="opSaving" @click="submitBalance">确认</el-button>
      </template>
    </el-dialog>
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
.bal-user {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 14px;
  margin-bottom: 14px;
  background: var(--art-gray-50);
  border-radius: 8px;
}
.bal-user__email {
  min-width: 0;
  overflow: hidden;
  color: var(--art-gray-800);
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.bal-user__balance {
  flex-shrink: 0;
  color: var(--theme-color-deep);
  font-size: 12px;
}
.bal-amount {
  display: flex;
  width: 100%;
  gap: 8px;
}
</style>
