<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { ElMessage } from 'element-plus'
import { Wallet } from '@element-plus/icons-vue'
import type { ColumnOption } from '@/types'
import { http } from '../http/index'
import { useSession } from '../http/session'
import { formatMoney } from '@/utils/format'
import { balanceTypeLabel } from '@/utils/admin-labels'
import PublicPageHead from '@/components/public/PublicPageHead.vue'

const session = useSession()
const amount = ref(20)
const balanceLoading = ref(true)
const submitting = ref(false)
const PRESETS = [10, 20, 50, 100, 200, 500]

interface BalanceLog {
  time?: string
  amount?: string
  after?: string
  type?: string
  note?: string
}
const logs = ref<BalanceLog[]>([])

const columns = ref<ColumnOption[]>([
  { prop: 'time', label: '时间', width: 150, sortable: 'custom' },
  { prop: 'type', label: '类型', width: 110, sortable: 'custom', formatter: (row) => balanceTypeLabel(row.type) },
  {
    prop: 'amount',
    label: '金额',
    width: 130,
    sortable: 'custom',
    formatter: (row) => {
      const n = Number(row.amount) || 0
      const color = n >= 0 ? 'var(--el-color-success)' : 'var(--art-gray-800)'
      return h(
        'b',
        { style: `color:${color};font-weight:600` },
        `${n >= 0 ? '+' : '-'}￥${formatMoney(Math.abs(n))}`,
      )
    },
  },
  { prop: 'after', label: '余额', width: 120, sortable: 'custom', formatter: (row) => `￥${formatMoney(row.after)}` },
  { prop: 'note', label: '备注', minWidth: 160, sortable: 'custom', formatter: (row) => row.note || '-' },
])

// 列表排序：数据在切片分页前先排序，否则只能排当前页
const sortKey = ref('')
const sortOrder = ref<1 | -1>(1)
const NUMERIC_SORT_KEYS = new Set(['amount', 'after'])
const sorted = computed(() => {
  if (!sortKey.value) return logs.value
  const k = sortKey.value
  const arr = [...logs.value]
  arr.sort((a, b) => {
    const va = (a as unknown as Record<string, unknown>)[k]
    const vb = (b as unknown as Record<string, unknown>)[k]
    const cmp = NUMERIC_SORT_KEYS.has(k)
      ? Number(va || 0) - Number(vb || 0)
      : String(va ?? '').localeCompare(String(vb ?? ''), 'zh-Hans-CN')
    return cmp * sortOrder.value
  })
  return arr
})

const page = ref(1)
const pageSize = ref(10)
const pagedLogs = computed(() =>
  sorted.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value),
)
function handleSortChange({ prop, order }: { prop: string; order: 'ascending' | 'descending' | null }) {
  sortKey.value = order ? prop : ''
  sortOrder.value = order === 'ascending' ? 1 : -1
}
const pagination = computed(() => ({ current: page.value, size: pageSize.value, total: logs.value.length }))
function handleLogPage(p: number) {
  page.value = p
}

async function submit() {
  // 按钮 loading 只拦点击，表单 @submit.prevent 仍可被 Enter 反复触发，
  // 不拦会下出两笔充值单。
  if (submitting.value) return
  if (!amount.value || amount.value <= 0) {
    ElMessage.warning('请输入充值金额')
    return
  }
  submitting.value = true
  try {
    const res = (await http.post('/user/recharge', { amount: String(amount.value) })) as {
      ok: number
      redirect?: string
      msg?: string
    }
    if (String(res.ok) !== '1') {
      ElMessage.error(res.msg || '充值请求失败，请稍后重试')
      return
    }
    if (res.redirect) location.href = res.redirect
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '充值失败')
  } finally {
    submitting.value = false
  }
}
onMounted(async () => {
  try {
    const d = (await http.get('/user/recharge')) as { balance?: string; logs?: BalanceLog[] }
    if (session.user && d.balance !== undefined) {
      session.user = { ...session.user, balance: d.balance }
    }
    logs.value = (d.logs || []) as BalanceLog[]
  } catch {
    /* 余额读取失败不阻塞充值表单 */
  } finally {
    balanceLoading.value = false
  }
})
</script>

<template>
  <div>
    <PublicPageHead title="账户充值" subtitle="充值金额计入账户余额，可用于支付与续费。" />

    <div class="recharge-stack">
      <div class="art-card recharge-balance">
        <span class="recharge-balance__icon"><el-icon><Wallet /></el-icon></span>
        <div class="recharge-balance__copy">
          <small>当前余额</small>
          <strong v-if="!balanceLoading">￥{{ formatMoney(session.user?.balance) }}</strong>
          <strong v-else class="recharge-balance__skeleton" aria-hidden="true" />
        </div>
        <div class="recharge-balance__aside">
          <small>到账为充值本金</small>
          <span>在线支付手续费由用户承担</span>
        </div>
      </div>

      <div class="art-card recharge-card">
        <h2>选择金额</h2>
        <div class="recharge-presets" role="group" aria-label="快捷金额">
          <button
            v-for="p in PRESETS"
            :key="p"
            type="button"
            :aria-pressed="amount === p"
            :class="{ 'is-active': amount === p }"
            @click="amount = p"
          >
            ￥{{ p }}
          </button>
        </div>

        <el-form label-position="top" class="recharge-form" @submit.prevent="submit">
          <el-form-item label="自定义金额（元）">
            <el-input-number
              v-model="amount"
              aria-label="自定义充值金额"
              :min="0.01"
              :precision="2"
              :step="10"
              style="width: 100%"
            />
          </el-form-item>
          <el-button type="primary" size="large" class="recharge-submit" :loading="submitting" @click="submit">
            确认充值 ￥{{ formatMoney(amount || 0) }}
          </el-button>
        </el-form>
      </div>
    </div>

    <div class="art-card recharge-logs">
      <div class="art-card-header">
        <div class="title">
          <h4>余额流水</h4>
          <p>最近的余额变动记录</p>
        </div>
      </div>
      <ArtTable
        class="mt-4"
        :loading="balanceLoading"
        :data="pagedLogs"
        :columns="columns"
        :pagination="pagination"
        :show-table-header="false"
        :height="360"
        empty-text="暂无流水"
        @sort-change="handleSortChange"
        @pagination:current-change="handleLogPage"
      />
    </div>
  </div>
</template>

<style scoped>
.recharge-stack {
  display: grid;
  gap: 14px;
}

.recharge-balance {
  display: flex;
  align-items: center;
  gap: 13px;
  padding: 19px 21px;
}

.recharge-balance__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 44px;
  height: 44px;
  color: var(--theme-color);
  font-size: 21px;
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
}

.recharge-balance__skeleton {
  display: inline-block;
  width: 108px;
  height: 24px;
  background: var(--art-gray-200);
  border-radius: var(--radius-sm);
  animation: recharge-pulse 1.4s ease-in-out infinite;
}

@keyframes recharge-pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.45;
  }
}

.recharge-balance__copy {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.recharge-balance__copy small {
  color: var(--art-gray-500);
  font-size: 12px;
}

.recharge-balance__copy strong {
  color: var(--art-gray-900);
  font-size: 24px;
  letter-spacing: -0.03em;
}

.recharge-balance__aside {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 4px;
  margin-left: auto;
  color: var(--art-gray-500);
  font-size: 11px;
  text-align: right;
}

.recharge-card {
  padding: 21px;
}

.recharge-logs {
  margin-top: 14px;
  padding: 21px;
}

.recharge-card h2 {
  margin: 0 0 15px;
  color: var(--art-gray-800);
  font-size: 15px;
  font-weight: 600;
}

.recharge-presets {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.recharge-presets button {
  min-width: 78px;
  padding: 10px 13px;
  color: var(--art-gray-600);
  font-size: 13px;
  font-weight: 600;
  background: var(--art-gray-100);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: color 0.16s ease, background 0.16s ease, border-color 0.16s ease;
}

.recharge-presets button:hover {
  color: var(--theme-color);
  border-color: var(--theme-color);
}

.recharge-presets button.is-active {
  color: var(--theme-color);
  background: var(--theme-color-soft);
  border-color: var(--theme-color);
}

.recharge-form {
  display: flex;
  gap: 12px;
  align-items: flex-end;
  margin-top: 18px;
}

.recharge-form :deep(.el-form-item) {
  flex: 1;
  margin-bottom: 0;
}

.recharge-submit {
  min-width: 220px;
  min-height: 40px;
  justify-content: center;
}

@media (max-width: 560px) {
  .recharge-balance {
    flex-wrap: wrap;
  }

  .recharge-balance__aside {
    flex-basis: 100%;
    align-items: flex-start;
    margin-left: 0;
    text-align: left;
  }

  .recharge-form {
    flex-direction: column;
    align-items: stretch;
  }

  .recharge-submit {
    width: 100%;
  }
}
</style>
