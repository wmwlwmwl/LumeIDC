<script setup lang="ts">
import { ref, computed, onBeforeUnmount, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Check, Wallet, CreditCard, Warning } from '@element-plus/icons-vue'
import { fetchPay, startPay, payByBalance, payStatus, type PayData } from '../api/pay'
import { formatMoney } from '@/utils/format'
import PublicContainer from '@/components/public/PublicContainer.vue'

const route = useRoute()
const invoiceId = computed(() => Number(route.params.id))
const data = ref<PayData | null>(null)
const loading = ref(true)
const loadError = ref(false)
const chosen = ref('')
const busy = ref(false)
let timer: ReturnType<typeof setTimeout> | null = null
let redirectTimer: ReturnType<typeof setTimeout> | null = null
let generation = 0
let disposed = false
let polling = false
let pollCount = 0
const MAX_POLLS = 60
let paused = false
const gatewayList = computed(() => (Array.isArray(data.value?.gateways) ? data.value.gateways : []))

// 组合支付：余额先抵扣，剩余本金走在线支付，手续费只按在线本金收取。
const useBalance = ref(false)
const amountNum = computed(() => Number(data.value?.invoice.amount || 0) || 0)
const balanceNum = computed(() => Number(data.value?.balance || 0) || 0)
const creditNum = computed(() => Number(data.value?.invoice.credit || 0) || 0)
// 重新发起支付时会先释放已抵扣余额再重新抵扣，故可用额度 = 当前余额 + 已抵扣。
const availableNum = computed(() => balanceNum.value + creditNum.value)
const isRecharge = computed(() => Boolean(data.value?.invoice.recharge))
const canFullBalance = computed(() => !isRecharge.value && amountNum.value > 0 && availableNum.value >= amountNum.value)
const showDeductToggle = computed(() => !isRecharge.value && availableNum.value > 0 && availableNum.value < amountNum.value)
const deductAmount = computed(() =>
  showDeductToggle.value && useBalance.value ? Math.min(availableNum.value, amountNum.value) : 0,
)
const onlineAmount = computed(() => Math.max(0, amountNum.value - deductAmount.value))

function gatewayFeeCents(g: { fee_percent: string }): number {
  const basis = Math.round((Number(g.fee_percent) || 0) * 100)
  const onlineCents = Math.round(onlineAmount.value * 100)
  if (basis <= 0 || onlineCents <= 0) return 0
  return Math.floor((onlineCents * basis + 5000) / 10000)
}
function gatewayFee(g: { fee_percent: string }): string {
  return formatMoney(gatewayFeeCents(g) / 100)
}
function gatewayPayable(g: { fee_percent: string }): string {
  return formatMoney((Math.round(onlineAmount.value * 100) + gatewayFeeCents(g)) / 100)
}

function isCurrent(id: number, version: number) {
  return !disposed && id === invoiceId.value && version === generation
}

function stopPolling() {
  if (timer !== null) clearTimeout(timer)
  timer = null
}

function clearRedirect() {
  if (redirectTimer !== null) clearTimeout(redirectTimer)
  redirectTimer = null
}

function schedulePolling(id: number, version: number) {
  if (!isCurrent(id, version) || loading.value || busy.value || !data.value || data.value.paid || data.value.expired) return
  if (paused) return
  if (pollCount >= MAX_POLLS) {
    stopPolling()
    ElMessage.warning('支付状态查询超时，如已完成支付请刷新页面或前往账单列表查看')
    return
  }
  stopPolling()
  timer = setTimeout(() => {
    timer = null
    void pollPaid(id, version)
  }, 4000)
}

async function load() {
  if (disposed) return false
  const id = invoiceId.value
  const version = ++generation
  stopPolling()
  clearRedirect()
  pollCount = 0
  loading.value = true
  loadError.value = false
  data.value = null
  chosen.value = ''
  useBalance.value = false
  busy.value = false
  try {
    const result = await fetchPay(id)
    if (!isCurrent(id, version)) return false
    data.value = result
    useBalance.value = Number(result.invoice.credit || 0) > 0
    if (gatewayList.value.length) chosen.value = gatewayList.value[0].code
  } catch (err: unknown) {
    if (!isCurrent(id, version)) return false
    loadError.value = true
    ElMessage.error((err as Error).message || '账单读取失败')
  } finally {
    if (isCurrent(id, version)) {
      loading.value = false
      schedulePolling(id, version)
    }
  }
  return isCurrent(id, version)
}

async function pollPaid(id: number, version: number) {
  if (!isCurrent(id, version)) return
  pollCount++
  // 切换账单后仍等待旧状态请求结束，避免跨代次请求重叠。
  if (polling) {
    schedulePolling(id, version)
    return
  }
  polling = true
  try {
    const status = await payStatus(id)
    if (!isCurrent(id, version)) return
    if (status.expired) {
      const refreshedVersion = generation + 1
      if (await load() && isCurrent(id, refreshedVersion)) ElMessage.warning('账单已过期，请重新下单')
      return
    }
    if (status.paid && data.value) {
      data.value.paid = true
      const recharge = data.value.invoice.recharge
      ElMessage.success(recharge ? '充值成功，余额已到账' : '订单支付成功')
      redirectTimer = setTimeout(() => {
        redirectTimer = null
        if (isCurrent(id, version)) location.href = recharge ? '/user/recharge' : '/services'
      }, 800)
    }
  } catch {
    /* 下次轮询重试 */
  } finally {
    polling = false
    schedulePolling(id, version)
  }
}

function onVisibilityChange() {
  if (document.hidden) {
    paused = true
    stopPolling()
  } else {
    paused = false
    // 页面回来时立即触发一次轮询，不等 4s 间隔
    if (isCurrent(invoiceId.value, generation) && !data.value?.paid && !data.value?.expired && !loading.value) {
      void pollPaid(invoiceId.value, generation)
    }
  }
}

watch(() => route.params.id, () => { void load() }, { immediate: true, flush: 'sync' })

onMounted(() => {
  document.addEventListener('visibilitychange', onVisibilityChange)
})

onBeforeUnmount(() => {
  disposed = true
  generation++
  stopPolling()
  clearRedirect()
  document.removeEventListener('visibilitychange', onVisibilityChange)
})

async function chooseGateway() {
  if (disposed || loading.value || busy.value || !data.value || data.value.paid || data.value.expired) return
  if (!chosen.value) {
    ElMessage.warning('请选择支付方式')
    return
  }
  const id = invoiceId.value
  const version = ++generation
  stopPolling()
  busy.value = true
  let redirected = false
  try {
    const res = await startPay(id, chosen.value, showDeductToggle.value && useBalance.value)
    if (!isCurrent(id, version)) return
    if (res.paid && res.redirect) {
      redirected = true
      location.href = res.redirect
      return
    }
    if (res.url) {
      redirected = true
      location.href = res.url
      return
    }
    // 网关未返回跳转地址：明确提示而非静默结束
    ElMessage.error('未获取到支付跳转地址，请稍后重试')
  } catch (err: unknown) {
    if (isCurrent(id, version)) ElMessage.error((err as Error).message || '发起支付失败')
  } finally {
    if (isCurrent(id, version)) {
      busy.value = false
      if (!redirected) schedulePolling(id, version)
    }
  }
}

async function payBalance() {
  if (disposed || loading.value || busy.value || !data.value || data.value.paid || data.value.expired) return
  const id = invoiceId.value
  const version = ++generation
  stopPolling()
  busy.value = true
  let redirected = false
  try {
    try {
      await ElMessageBox.confirm(
        `将使用余额 ￥${formatMoney(amountNum.value)} 完成支付，确认继续？`,
        '余额支付确认',
        { type: 'warning', confirmButtonText: '确认支付', cancelButtonText: '取消' },
      )
    } catch {
      return
    }
    if (!isCurrent(id, version)) return
    const res = await payByBalance(id)
    if (!isCurrent(id, version)) return
    if (String(res.ok) === '1') {
      ElMessage.success('余额支付成功')
      redirected = true
      location.href = res.redirect || '/services'
    } else {
      ElMessage.error(res.msg || '余额支付失败')
    }
  } catch (err: unknown) {
    if (isCurrent(id, version)) ElMessage.error((err as Error).message || '余额支付失败')
  } finally {
    if (isCurrent(id, version)) {
      busy.value = false
      if (!redirected) schedulePolling(id, version)
    }
  }
}

function goServices() {
  location.href = '/services'
}

function goAfterPaid() {
  location.href = data.value?.invoice.recharge ? '/user/recharge' : '/services'
}

const chosenGateway = computed(() => gatewayList.value.find((g) => g.code === chosen.value) || null)
</script>

<template>
  <PublicContainer>
    <div class="pay-page">
      <div v-if="loading" class="art-card pay-card"><el-skeleton :rows="6" animated /></div>

      <div v-else-if="loadError" class="art-card pay-state" role="alert">
        <p>账单读取失败，请检查网络后重试。</p>
        <el-button type="primary" @click="load">重新加载</el-button>
      </div>

      <div v-else-if="data" class="art-card pay-card">
        <header class="pay-head">
          <h1>{{ data.site_name }}</h1>
          <p>账单号 <b>{{ data.invoice.no }}</b> · {{ data.invoice.recharge ? '账户充值' : '订单支付' }}</p>
        </header>

        <template v-if="data.paid">
          <div class="pay-paid">
            <el-icon class="pay-paid__icon"><Check /></el-icon>
             <h2>{{ data.invoice.recharge ? '充值成功' : '订单支付成功' }}</h2>
             <p>{{ data.invoice.recharge ? '余额已到账' : '服务正在开通中' }}</p>
             <el-button type="primary" size="large" @click="goAfterPaid">{{ data.invoice.recharge ? '查看账户余额' : '前往我的服务' }}</el-button>
          </div>
        </template>

        <template v-else-if="data.expired">
          <div class="pay-expired">
            <el-icon class="pay-expired__icon"><Warning /></el-icon>
            <h2>账单已过期</h2>
            <p>该账单已超过支付时间，不能继续付款，请返回账单列表重新下单。</p>
            <el-button type="primary" size="large" @click="goServices">返回我的服务</el-button>
          </div>
        </template>

        <template v-else>
          <div class="pay-amount">
            <span>{{ data.invoice.recharge ? '充值金额' : '订单金额' }}</span>
            <div><strong>￥{{ formatMoney(data.invoice.amount) }}</strong><small>RMB</small></div>
          </div>

          <div class="pay-body">
            <div class="pay-section-head">
              <h2>选择支付方式</h2>
              <p>选择一个便捷的渠道完成付款</p>
            </div>
            <div v-if="gatewayList.length" class="pay-methods" role="radiogroup" aria-label="支付方式">
              <button
                v-for="g in gatewayList"
                :key="g.code"
                type="button"
                class="pay-method"
                role="radio"
                :aria-checked="chosen === g.code"
                :class="{ 'is-active': chosen === g.code }"
                @click="chosen = g.code"
              >
                <span class="pay-method__radio"><i /></span>
                <span class="pay-method__name"><el-icon><CreditCard /></el-icon><b>{{ g.name }}</b></span>
                <span class="pay-method__fee">
                  <small v-if="gatewayFeeCents(g) > 0">含手续费 ￥{{ gatewayFee(g) }}</small><b>￥{{ gatewayPayable(g) }}</b>
                </span>
              </button>
            </div>
            <el-alert
              v-else
              type="info"
              :closable="false"
              show-icon
              title="暂无可用的在线支付方式，请使用余额支付或联系管理员"
            />

            <div v-if="showDeductToggle" class="pay-balance">
              <span class="pay-balance__radio"><i>￥</i></span>
              <span class="pay-balance__copy">
                <b><el-icon><Wallet /></el-icon>使用余额抵扣</b>
                <small>可用 ￥{{ formatMoney(availableNum) }} · 抵扣部分免手续费</small>
              </span>
              <el-switch v-model="useBalance" />
            </div>

            <div v-if="canFullBalance" class="pay-balance">
              <span class="pay-balance__radio"><i>￥</i></span>
              <span class="pay-balance__copy">
                <b><el-icon><Wallet /></el-icon>余额支付</b>
                <small>当前余额 ￥{{ formatMoney(data.balance) }} · 免手续费</small>
              </span>
              <el-button :loading="busy" @click="payBalance">立即支付</el-button>
            </div>

            <el-button
              type="primary"
              size="large"
              class="pay-submit"
              :loading="busy"
              :disabled="!gatewayList.length || !chosen"
              @click="chooseGateway"
            >
              去支付 ￥{{ chosenGateway ? gatewayPayable(chosenGateway) : formatMoney(onlineAmount) }}
            </el-button>
            <p v-if="deductAmount > 0" class="pay-deduct-hint">
              余额已抵扣 ￥{{ formatMoney(deductAmount) }}，剩余在线支付 ￥{{ formatMoney(onlineAmount) }}
            </p>

            <p v-if="gatewayList.length" class="pay-waiting">
              <span />正在等待到账，支付完成后本页会自动跳转，无需手动刷新
            </p>
          </div>
        </template>
      </div>

      <el-empty v-else description="账单不存在或无权查看" />
    </div>
  </PublicContainer>
</template>

<style scoped>
.pay-page {
  width: 100%;
}

.pay-card {
  overflow: hidden;
}

.pay-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 14px;
  padding: 64px 26px;
  text-align: center;
}

.pay-state p {
  margin: 0;
  color: var(--art-gray-600);
  font-size: 14px;
}

.pay-head {
  padding: 24px 26px;
  background: var(--art-gray-100);
  border-bottom: 1px solid var(--art-card-border);
}

.pay-head h1 {
  margin: 0 0 4px;
  color: var(--art-gray-900);
  font-size: 19px;
  font-weight: 700;
}

.pay-head p {
  margin: 0;
  color: var(--art-gray-500);
  font-size: 12px;
}

.pay-head p b {
  color: var(--art-gray-700);
  font-weight: 600;
}

.pay-paid {
  padding: 56px 26px;
  text-align: center;
}

.pay-expired {
  padding: 56px 26px;
  text-align: center;
}

.pay-expired__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 58px;
  height: 58px;
  margin: 0 auto 18px;
  color: var(--el-color-warning);
  font-size: 30px;
  background: var(--el-color-warning-light-9);
  border-radius: 50%;
}

.pay-expired h2 {
  margin: 0;
  color: var(--art-gray-900);
  font-size: 19px;
  font-weight: 700;
}

.pay-expired p {
  margin: 7px 0 22px;
  color: var(--art-gray-500);
  font-size: 12px;
}

.pay-paid__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 58px;
  height: 58px;
  margin: 0 auto 18px;
  color: var(--theme-color-contrast);
  font-size: 30px;
  background: var(--el-color-success);
  border-radius: 50%;
}

.pay-paid h2 {
  margin: 0;
  color: var(--art-gray-900);
  font-size: 19px;
  font-weight: 700;
}

.pay-paid p {
  margin: 7px 0 22px;
  color: var(--art-gray-500);
  font-size: 12px;
}

.pay-amount {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 24px 26px 0;
  padding: 20px;
  background: var(--theme-color-soft);
  border-radius: var(--radius-lg);
}

.pay-amount > span {
  color: var(--art-gray-600);
  font-size: 12px;
}

.pay-amount div {
  display: flex;
  align-items: baseline;
  gap: 6px;
}

.pay-amount strong {
  color: var(--theme-color);
  font-size: 29px;
  letter-spacing: -0.04em;
}

.pay-amount small {
  color: var(--art-gray-500);
  font-size: 11px;
}

.pay-body {
  padding: 20px 26px 26px;
}

.pay-section-head {
  margin-bottom: 13px;
}

.pay-section-head h2 {
  margin: 0;
  color: var(--art-gray-800);
  font-size: 14px;
  font-weight: 600;
}

.pay-section-head p {
  margin: 3px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
}

.pay-methods {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.pay-method {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 13px 14px;
  text-align: left;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-md);
  cursor: pointer;
}

.pay-method:hover {
  border-color: var(--theme-color);
}

.pay-method.is-active {
  background: var(--theme-color-soft);
  border-color: var(--theme-color);
}

.pay-method__radio {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 18px;
  height: 18px;
  border: 2px solid var(--art-gray-300);
  border-radius: 50%;
}

.pay-method.is-active .pay-method__radio {
  border-color: var(--theme-color);
}

.pay-method__radio i {
  width: 8px;
  height: 8px;
  background: var(--theme-color);
  border-radius: 50%;
  opacity: 0;
}

.pay-method.is-active .pay-method__radio i {
  opacity: 1;
}

.pay-method__name {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  color: var(--art-gray-700);
  font-size: 13px;
}

.pay-method__name .el-icon {
  color: var(--theme-color);
}

.pay-method__fee {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 2px;
  color: var(--art-gray-700);
  font-size: 13px;
}

.pay-method__fee small {
  color: var(--art-gray-500);
  font-size: 11px;
}

.pay-method__fee b {
  font-size: 12px;
}

.pay-balance {
  display: flex;
  align-items: center;
  gap: 11px;
  margin-top: 11px;
  padding: 13px 14px;
  background: var(--theme-color-soft);
  border: 1px solid color-mix(in srgb, var(--theme-color) 32%, var(--art-card-border));
  border-radius: var(--radius-md);
}

.pay-balance__radio {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  color: var(--theme-color);
  font-size: 13px;
  font-weight: 700;
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
}

.pay-balance__copy {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 2px;
}

.pay-balance__copy b {
  display: flex;
  align-items: center;
  gap: 5px;
  color: var(--art-gray-800);
  font-size: 12px;
}

.pay-balance__copy small {
  color: var(--art-gray-500);
  font-size: 11px;
}

.pay-balance__copy .el-icon {
  color: var(--el-color-success);
}

.pay-submit {
  width: 100%;
  min-height: 45px;
  margin-top: 17px;
  justify-content: center;
}

.pay-waiting {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  margin: 13px 0 0;
  color: var(--art-gray-500);
  font-size: 11px;
}

.pay-waiting span {
  width: 7px;
  height: 7px;
  background: var(--el-color-success);
  border-radius: 50%;
  animation: pulse 1.6s infinite;
}

@keyframes pulse {
  70% {
    box-shadow: 0 0 0 8px transparent;
  }
  100% {
    box-shadow: 0 0 0 0 transparent;
  }
}
</style>
