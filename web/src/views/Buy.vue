<script setup lang="ts">
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowRight } from '@element-plus/icons-vue'
import { fetchBuy, createOrder, type BuyData } from '../api/store'
import { ApiError } from '../http'
import { useSession } from '../http/session'
import { formatMoney } from '@/utils/format'
import PublicContainer from '@/components/public/PublicContainer.vue'
import PublicPageHead from '@/components/public/PublicPageHead.vue'
import OsSelector from '@/components/public/OsSelector.vue'

const session = useSession()
const route = useRoute()
const router = useRouter()
const loading = ref(true)
const loadError = ref(false)
const submitting = ref(false)
const data = ref<BuyData | null>(null)
const cycle = ref<'monthly' | 'quarterly' | 'yearly'>('monthly')
const coupon = ref('')
const sel = reactive<Record<string, string>>({})

const cycleName = { monthly: '月付', quarterly: '季付', yearly: '年付' } as const

const product = computed(() => data.value?.product)
// 可见配置项（隐藏项由 ensureDefaults 填默认值，不展示）
const visibleOptions = computed(() => (data.value?.options || []).filter((o) => !o.hidden))
const cycles = computed(() => {
  if (!data.value) return []
  const labels = { monthly: '月付', quarterly: '季付', yearly: '年付' } as const
  return data.value.cycles.map((key) => ({
    key,
    label: `${labels[key]} · ￥${data.value!.cycle[key]}${key === 'monthly' ? ' 起' : ''}`,
  }))
})

// 定价（对齐旧 buy.html 的客户端 recalc）
function priceOf(sub: { pricing?: Record<string, number> }, c: string): number {
  if (!sub.pricing) return 0
  if (sub.pricing[c] !== undefined) return sub.pricing[c]
  return sub.pricing.monthly || 0
}
// 初装费（上游一次性费用，仅首购收取）。按周期取、不做跨周期回落，
// 与后端 repo.ConfigValue.SetupPrice 完全一致，否则前后端报价会不一致。
function setupOf(sub: { setup?: Record<string, number> }, c: string): number {
  const s = sub.setup
  if (!s) return 0
  if (c === 'yearly') return s.yearly ?? s.annually ?? 0
  return s[c] ?? 0
}
// 选项唯一值：上游部分商品存在 value 重复（同名/同值），
// 直接按 value 绑定会导致单选全部命中、无法切换。这里对重复值回退到 name，仍重复则附加序号。
function choiceValue(subs: { name: string; value?: string }[], s: { name: string; value?: string }, index: number): string {
  const raw = String(s.value ?? s.name)
  const sameRaw = subs.filter((x) => String(x.value ?? x.name) === raw).length
  if (sameRaw <= 1) return raw
  if (subs.filter((x) => x.name === s.name).length === 1) return s.name
  return `${raw}#${index}`
}
function findSubByChoice<T extends { name: string; value?: string }>(subs: T[] = [], val: string): T | null {
  return subs.find((s, i) => choiceValue(subs, s, i) === val) || null
}

const totals = computed(() => {
  const d = data.value
  if (!d) return { base: 0, lines: [] as { name: string; value: string; price: number; setup: number }[], setup: 0, total: 0 }
  const c = cycle.value
  const pRate = d.profit_type === 1 ? 0 : (d.profit_value || 0) / 100
  const pFixed = d.profit_type === 1 ? d.profit_value || 0 : 0
  const baseRaw = d.base[c] || 0

  let total = baseRaw
  let setupTotal = 0
  const lines: { name: string; value: string; price: number; setup: number }[] = []
  for (const opt of d.options) {
    if (opt.hidden || opt.field === 'os') continue
    const val = sel[opt.field]
    if (val === undefined || val === '') continue
    if (opt.option_mode === 'range') {
      const v = parseFloat(val)
      if (Number.isNaN(v)) continue
      const step = opt.step && opt.step > 0 ? opt.step : 1
      const subs = (opt.sub || []).slice().sort((a, b) => (a.min || 0) - (b.min || 0))
      const subSize = (s: { value?: string; name: string; min?: number }): number => {
        for (const str of [s.value, s.name]) {
          if (str == null) continue
          const m = String(str).match(/^[\d.]+/)
          if (m) return parseFloat(m[0])
        }
        return s.min || 0
      }
      let picked: (typeof subs)[number] | null = null
      let exact: (typeof subs)[number] | null = null
      let rangeSub: (typeof subs)[number] | null = null
      for (const s of subs) {
        const maxB = s.max || opt.max || 0
        if (v >= (s.min || 0) && v <= maxB) {
          if ((s.max ?? 0) > (s.min || 0)) {
            if (!rangeSub) rangeSub = s
          } else if (subSize(s) === v) {
            exact = s
            break
          }
        }
      }
      picked = exact || rangeSub
      if (picked) {
        const p = picked.max && picked.max > (picked.min || 0) ? priceOf(picked, c) * Math.floor(v / step) : priceOf(picked, c)
        const sf = setupOf(picked, c)
        lines.push({ name: opt.name, value: `${v}${opt.unit || ''}`, price: p, setup: sf })
        total += p
        setupTotal += sf
      }
    } else {
      const sub = findSubByChoice(opt.sub || [], val)
      if (sub) {
        const p = priceOf(sub, c)
        const sf = setupOf(sub, c)
        lines.push({ name: opt.name, value: sub.name, price: p, setup: sf })
        total += p
        setupTotal += sf
      }
    }
  }
  const base = pFixed ? baseRaw + pFixed : baseRaw * (1 + pRate)
  // 首购口径 = 周期费 + 初装费，一起参与利润加成（与后端 CreateOrder 一致）
  const chargeable = total + setupTotal
  const grand = pFixed ? chargeable + pFixed : chargeable * (1 + pRate)
  return {
    base,
    lines: lines.map((l) => ({
      ...l,
      price: pRate ? l.price * (1 + pRate) : l.price,
      setup: pRate ? l.setup * (1 + pRate) : l.setup,
    })),
    setup: pRate ? setupTotal * (1 + pRate) : setupTotal,
    total: grand,
  }
})

function ensureDefaults() {
  const d = data.value
  if (!d) return
  for (const opt of d.options) {
    const subs = opt.sub || []
    if (opt.hidden) {
      sel[opt.field] = opt.option_mode === 'range' ? String(opt.min ?? 0) : (subs.length ? choiceValue(subs, subs[0], 0) : '')
      continue
    }
    if (opt.option_mode === 'range') {
      sel[opt.field] = String(opt.min ?? 0)
      continue
    }
    // select：默认选择月度加价最低的子项（对齐旧 setSelectDefaults），os 除外
    if (!subs.length) continue
    if (opt.field === 'os') {
      sel[opt.field] = choiceValue(subs, subs[0], 0)
      continue
    }
    let best = subs[0]
    let bp = priceOf(subs[0], 'monthly')
    for (const s of subs) {
      const p = priceOf(s, 'monthly')
      if (p < bp) {
        bp = p
        best = s
      }
    }
    sel[opt.field] = choiceValue(subs, best, subs.indexOf(best))
  }
}

async function loadProduct() {
  loading.value = true
  loadError.value = false
  try {
    data.value = await fetchBuy(route.params.id as string)
    cycle.value = data.value.default_cycle
    ensureDefaults()
  } catch (err: unknown) {
    data.value = null
    loadError.value = true
    ElMessage.error((err as Error).message || '产品信息加载失败')
  } finally {
    loading.value = false
  }
}

onMounted(loadProduct)
// 同一路由换产品时组件复用，需监听参数重新加载
watch(() => route.params.id, loadProduct)

async function submit() {
  if (!data.value) return
  const missing = visibleOptions.value.find((o) => (o.sub?.length || 0) > 0 && !sel[o.field])
  if (missing) {
    ElMessage.warning(`请选择${missing.name}`)
    return
  }
  submitting.value = true
  try {
    const body: Record<string, unknown> = { product_id: data.value.product.id, cycle: cycle.value, coupon: coupon.value.trim() }
    for (const opt of data.value.options) {
      const v = sel[opt.field]
      if (v !== undefined && v !== '') body[`cfg_${opt.field}`] = v
    }
    const res = await createOrder(body)
    if (res.paid) {
      location.href = res.redirect || '/services'
      return
    }
    if (res.invoice_id) {
      router.push(`/pay/${res.invoice_id}`)
      return
    }
    if (res.redirect) {
      location.href = res.redirect
      return
    }
    ElMessage.success('下单成功')
  } catch (err: unknown) {
    const code = err instanceof ApiError ? err.data.code : undefined
    // 上游价格已变：本地已同步为新价，重载页面让用户按新价重新确认。
    if (code === 'price_changed') {
      ElMessage.warning((err as Error).message || '商品价格已更新，请重新确认后下单')
      await loadProduct()
      return
    }
    // 上游已下架：提示后回到产品中心。
    if (code === 'unshelved') {
      ElMessage.error((err as Error).message || '商品已下架')
      router.push('/cart')
      return
    }
    ElMessage.error((err as Error).message || '下单失败')
  } finally {
    submitting.value = false
  }
}
const currentCycleText = computed(() => cycleName[cycle.value] || cycle.value)
</script>

<template>
  <PublicContainer>
    <div v-if="loading" class="art-card buy-loading"><el-skeleton :rows="8" animated /></div>

    <div v-else-if="loadError" class="art-card buy-state" role="alert">
      <p>产品信息加载失败，请检查网络后重试。</p>
      <el-button type="primary" @click="loadProduct">重新加载</el-button>
    </div>

    <template v-else-if="product">
      <PublicPageHead :title="product.name" />

      <el-steps :active="0" align-center class="buy-steps">
        <el-step title="选择配置" />
        <el-step title="确认订单" />
        <el-step title="完成部署" />
      </el-steps>

      <div class="buy-layout">
        <div class="buy-main">
          <section class="art-card buy-section">
            <h2>计费周期</h2>
            <p class="buy-hint">选择适合你的付款方式，越长越划算。</p>
            <el-radio-group v-model="cycle" class="buy-cycle">
              <el-radio-button v-for="cy in cycles" :key="cy.key" :value="cy.key">{{ cy.label }}</el-radio-button>
            </el-radio-group>
          </section>

          <section class="art-card buy-section">
            <h2>服务配置</h2>
            <p class="buy-hint">按需调整实例规格，右侧实时计价。</p>
            <div class="buy-configs">
              <div
                v-for="opt in visibleOptions"
                :key="opt.field"
                class="buy-config"
                :class="{ 'buy-config--full': opt.field === 'os' }"
              >
                <label>{{ opt.name }}{{ opt.unit ? `（${opt.unit}）` : '' }}</label>
                <OsSelector
                  v-if="opt.field === 'os'"
                  :subs="opt.sub || []"
                  :model-value="sel[opt.field]"
                  @update:model-value="(v: string) => (sel[opt.field] = v)"
                />
                <template v-else-if="opt.option_mode === 'range'">
                  <div class="buy-range-row">
                    <el-slider
                      class="flex-1"
                      :model-value="Number(sel[opt.field] ?? opt.min ?? 0)"
                      :min="opt.min ?? 0"
                      :max="opt.max ?? 100"
                      :step="opt.step ?? 1"
                      @update:model-value="(v: number | number[]) => (sel[opt.field] = String(Array.isArray(v) ? v[0] : v))"
                    />
                    <el-input-number
                      :model-value="Number(sel[opt.field] ?? 0)"
                      :min="opt.min ?? 0"
                      :max="opt.max ?? 100"
                      :step="opt.step ?? 1"
                      size="small"
                      style="width: 108px"
                      @update:model-value="(v: number | undefined) => (sel[opt.field] = String(v ?? opt.min ?? 0))"
                    />
                  </div>
                </template>
                <el-radio-group v-else v-model="sel[opt.field]" class="buy-select-group">
                  <el-radio-button
                    v-for="(s, i) in opt.sub || []"
                    :key="choiceValue(opt.sub || [], s, i)"
                    :value="choiceValue(opt.sub || [], s, i)"
                  >
                    {{ s.name }}
                  </el-radio-button>
                </el-radio-group>
              </div>
            </div>
            <label class="buy-coupon-label" for="buy-coupon">优惠码（可选）</label>
            <el-input id="buy-coupon" v-model="coupon" placeholder="如有优惠码请填写" clearable style="max-width: 300px" />
          </section>
        </div>

        <aside class="art-card buy-summary">
          <div class="buy-summary__top">
            <strong>{{ product.name }}</strong>
            <small>{{ currentCycleText }} · 实时计价</small>
          </div>
          <div v-if="product.requires_identity" class="buy-warn is-amber" role="note">
            此产品购买和续费需要先通过实名认证。
          </div>
          <div v-if="product.stock === 0" class="buy-warn is-red" role="alert">
            该产品已售罄，暂时无法下单。
          </div>
          <div class="buy-summary__lines">
            <div v-for="(l, i) in totals.lines" :key="`${l.name}-${i}`">
              <span>{{ l.name }}</span><em>{{ l.value }}</em>
              <b>+￥{{ formatMoney(l.price) }}<small v-if="l.setup">+￥{{ formatMoney(l.setup) }} 初装费</small></b>
            </div>
            <div><span>基础价格</span><em></em><b>￥{{ formatMoney(totals.base) }}</b></div>
          </div>
          <div class="buy-summary__total">
            <span>合计费用</span>
            <div class="buy-summary__total-main">
              <div class="buy-summary__total-amount">
                <strong>￥{{ formatMoney(totals.total) }}</strong>
                <small>/ {{ currentCycleText }}</small>
              </div>
              <small v-if="totals.setup" class="buy-summary__total-note">
                首次开通（含初装费 ￥{{ formatMoney(totals.setup) }}）
              </small>
            </div>
          </div>
          <el-button
            v-if="session.user"
            type="primary"
            size="large"
            class="buy-submit"
            :loading="submitting"
            :disabled="product.stock === 0"
            @click="submit"
          >
            立即购买 <el-icon><ArrowRight /></el-icon>
          </el-button>
          <template v-else>
            <RouterLink
              v-if="product.stock !== 0"
              :to="{ path: '/login', query: { next: `/buy/${product.id}` } }"
            >
              <el-button type="primary" size="large" class="buy-submit">
                登录后购买 <el-icon><ArrowRight /></el-icon>
              </el-button>
            </RouterLink>
            <el-button v-else type="primary" size="large" class="buy-submit" disabled>
              登录后购买 <el-icon><ArrowRight /></el-icon>
            </el-button>
            <p class="buy-login-hint">配置无需登录，提交订单时需要账号。</p>
          </template>
          <div class="buy-summary__foot">
            <span>分钟级开通</span><span>弹性升降级</span><span>7×24 支持</span>
          </div>
        </aside>
      </div>
    </template>
    <el-empty v-else description="产品不存在或已下架" />
  </PublicContainer>
</template>

<style scoped>
.buy-loading {
  padding: 22px;
}

.buy-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 14px;
  padding: 56px 22px;
  text-align: center;
}

.buy-state p {
  margin: 0;
  color: var(--art-gray-600);
  font-size: 14px;
}

.buy-steps {
  margin-bottom: 22px;
  padding: 18px 20px;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-lg);
}

.buy-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 340px;
  gap: 18px;
  align-items: start;
}

.buy-main {
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-width: 0;
}

.buy-section {
  padding: 22px;
}

.buy-section h2 {
  margin: 0;
  color: var(--art-gray-800);
  font-size: 15px;
  font-weight: 600;
}

.buy-hint {
  margin: 4px 0 15px;
  color: var(--art-gray-500);
  font-size: 12px;
}

.buy-cycle {
  display: flex;
  gap: 8px;
}

.buy-cycle :deep(.el-radio-button__inner) {
  min-width: 96px;
  padding: 10px 14px;
  background: var(--art-gray-100);
  border: 1px solid var(--art-card-border);
  border-radius: calc(var(--custom-radius) / 2) !important;
}

.buy-configs {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 17px;
}

.buy-config--full {
  grid-column: 1 / -1;
}

.buy-config > label,
.buy-coupon-label {
  display: block;
  margin-bottom: 8px;
  color: var(--art-gray-600);
  font-size: 12px;
  font-weight: 600;
}

.buy-range-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}

.buy-range-row .flex-1 {
  flex: 1 1 160px;
  min-width: 0;
}

.buy-select-group {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.buy-select-group :deep(.el-radio-button__inner) {
  padding: 8px 12px;
  background: var(--art-gray-100);
  border: 1px solid var(--art-card-border);
  border-radius: calc(var(--custom-radius) / 2) !important;
}

.buy-coupon-label {
  margin-top: 17px;
}

.buy-summary {
  position: sticky;
  top: 88px;
  padding: 21px;
}

.buy-summary__top {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding-bottom: 13px;
  border-bottom: 1px solid var(--art-card-border);
}

.buy-summary__top strong {
  color: var(--art-gray-900);
  font-size: 15px;
}

.buy-summary__top small {
  color: var(--art-gray-500);
  font-size: 11px;
}

.buy-warn {
  margin-top: 12px;
  padding: 9px 11px;
  font-size: 12px;
  border-radius: var(--radius-md);
}

.buy-warn.is-amber {
  color: var(--el-color-warning);
  background: var(--el-color-warning-light-9);
}

.buy-warn.is-red {
  color: var(--el-color-danger);
  background: var(--el-color-danger-light-9);
}

.buy-summary__lines {
  margin-top: 12px;
}

.buy-summary__lines > div {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 0;
  font-size: 12px;
  border-bottom: 1px dashed var(--art-card-border);
}

/* 末行紧邻合计的实线分隔，去掉虚线避免双线 */
.buy-summary__lines > div:last-child {
  border-bottom: none;
}

.buy-summary__lines span {
  color: var(--art-gray-600);
}

.buy-summary__lines em {
  flex: 1;
  color: var(--art-gray-500);
  font-style: normal;
  text-align: right;
}

.buy-summary__lines b {
  min-width: 58px;
  color: var(--art-gray-700);
  font-weight: 600;
  text-align: right;
  white-space: nowrap;
}

/* 初装费另起一行：与主价同格会挤成一坨、两个 + 号连在一起 */
.buy-summary__lines b small {
  display: block;
  margin-top: 2px;
  color: var(--art-gray-400);
  font-size: 11px;
  font-weight: 400;
}

.buy-summary__total {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-top: 13px;
  padding-top: 13px;
  border-top: 1px solid var(--art-card-border);
}

.buy-summary__total > span {
  flex-shrink: 0;
  color: var(--art-gray-700);
  font-size: 12px;
  font-weight: 600;
}

/* 金额与说明上下堆叠、右对齐：同一行会让小字紧贴大数字并被挤到换行 */
.buy-summary__total > div {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 3px;
  min-width: 0;
  text-align: right;
}

/* 金额与周期单位同行、基线对齐：单位单独占一行会显得孤立 */
.buy-summary__total-amount {
  display: flex;
  align-items: baseline;
  gap: 4px;
  white-space: nowrap;
}

.buy-summary__total strong {
  color: var(--theme-color);
  font-size: 22px;
  line-height: 1.1;
  letter-spacing: -0.04em;
}

.buy-summary__total small {
  color: var(--art-gray-500);
  font-size: 11px;
  line-height: 1.4;
}

.buy-submit {
  width: 100%;
  min-height: 44px;
  margin-top: 15px;
  justify-content: center;
  gap: 5px;
}

.buy-login-hint {
  margin: 9px 0 0;
  color: var(--art-gray-500);
  font-size: 11px;
  text-align: center;
}

.buy-summary__foot {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 15px;
  padding-top: 12px;
  color: var(--art-gray-500);
  font-size: 11px;
  border-top: 1px solid var(--art-card-border);
}

@media (max-width: 860px) {
  .buy-layout {
    grid-template-columns: 1fr;
  }

  .buy-summary {
    position: static;
  }

  .buy-configs {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .buy-steps {
    padding: 14px 8px;
  }

  .buy-steps :deep(.el-step__title) {
    font-size: 12px;
  }

  .buy-steps :deep(.el-step__icon) {
    width: 22px;
    height: 22px;
    font-size: 12px;
  }

  .buy-section {
    padding: 18px 16px;
  }

  .buy-range-row .el-input-number {
    width: 100% !important;
  }
}
</style>
