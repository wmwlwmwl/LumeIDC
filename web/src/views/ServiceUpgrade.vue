<script setup lang="ts">
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowRight } from '@element-plus/icons-vue'
import { fetchUpgradeForm, upgradeService, type UpgradeForm } from '../api/user'
import { ApiError } from '../http'
import { formatMoney } from '@/utils/format'
import PublicPageHead from '@/components/public/PublicPageHead.vue'
import OsSelector from '@/components/public/OsSelector.vue'

const route = useRoute()
const router = useRouter()
const id = computed(() => Number(route.params.id))
// 后台代管视图（路由 meta.admin）：后台无前台支付页，下单后不跳转，改提示并回到服务详情。
const isAdminView = computed(() => route.meta.admin === true)

const loading = ref(false)
const loadError = ref(false)
const submitting = ref(false)
const form = ref<UpgradeForm | null>(null)
const targetId = ref(0)
const cycle = ref<'monthly' | 'quarterly' | 'yearly'>('monthly')
const sel = reactive<Record<string, string>>({})

const target = computed(() => form.value?.target)
const cycles = computed(() => {
  const t = target.value
  if (!t) return []
  const out: { key: 'monthly' | 'quarterly' | 'yearly'; label: string }[] = [
    { key: 'monthly', label: `按月付 ￥${formatMoney(t.base.monthly || 0)}` },
  ]
  if (form.value?.show_q) out.push({ key: 'quarterly', label: `按季付 ￥${formatMoney(t.base.quarterly || 0)}` })
  if (form.value?.show_y) out.push({ key: 'yearly', label: `按年付 ￥${formatMoney(t.base.yearly || 0)}` })
  return out
})

// 计价：与购买页/后端一致（利润按类型施加于基础价与各配置加价）
function priceOf(sub: { pricing?: Record<string, number> }, c: string): number {
  if (!sub.pricing) return 0
  if (sub.pricing[c] !== undefined) return sub.pricing[c]
  return sub.pricing.monthly || 0
}
// 选项唯一值：上游存在 value 重复的选项，按 value 绑定会无法切换；重复时回退 name，再重复加序号。
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
  const t = target.value
  if (!t) return { total: 0, lines: [] as { name: string; value: string; price: number }[] }
  const c = cycle.value
  const pRate = t.profit_type === 1 ? 0 : (t.profit_value || 0) / 100
  const pFixed = t.profit_type === 1 ? t.profit_value || 0 : 0
  let total = t.base[c] || 0
  const lines: { name: string; value: string; price: number }[] = []
  for (const opt of t.options.filter((o) => !o.hidden)) {
    const val = sel[opt.field]
    if (!val) continue
    if (opt.option_mode === 'range') {
      const v = parseFloat(val)
      if (Number.isNaN(v)) continue
      const step = opt.step && opt.step > 0 ? opt.step : 1
      const subs = (opt.sub || []).slice().sort((a, b) => (a.min || 0) - (b.min || 0))
      let picked: (typeof subs)[number] | null = null
      for (const s of subs) {
        const maxB = s.max || opt.max || 0
        if (v >= (s.min || 0) && v <= maxB) {
          picked = s
          if ((s.max ?? 0) <= (s.min || 0)) break
        }
      }
      if (picked) {
        const p =
          picked.max && picked.max > (picked.min || 0)
            ? priceOf(picked, c) * Math.floor(v / step)
            : priceOf(picked, c)
        lines.push({ name: opt.name, value: `${v}${opt.unit || ''}`, price: p })
        total += p
      }
    } else {
      const sub = findSubByChoice(opt.sub || [], val)
      if (sub) {
        const p = priceOf(sub, c)
        lines.push({ name: opt.name, value: sub.name, price: p })
        total += p
      }
    }
  }
  const grand = pFixed ? total + pFixed : total * (1 + pRate)
  return { total: grand, lines: lines.map((l) => ({ ...l, price: pRate ? l.price * (1 + pRate) : l.price })) }
})

// 差价预估：后端按「剩余天数 × 两侧周期日价之差」核算（proration），
// 这里用月价差做粗略展示，仅供参考，实际金额以生成的订单为准。
const diff = computed(() => totals.value.total - Number(form.value?.current_monthly || 0))
const isSame = computed(() => Math.abs(diff.value) < 0.005)

function ensureDefaults() {
  const t = target.value
  if (!t) return
  for (const k of Object.keys(sel)) delete sel[k]
  for (const opt of t.options) {
    const subs = opt.sub || []
    if (opt.hidden) {
      sel[opt.field] = opt.option_mode === 'range' ? String(opt.min ?? 0) : (subs.length ? choiceValue(subs, subs[0], 0) : '')
      continue
    }
    if (opt.option_mode === 'range') {
      sel[opt.field] = String(opt.min ?? 0)
      continue
    }
    if (!subs.length) continue
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

async function load(keepTarget = false) {
  loading.value = true
  loadError.value = false
  try {
    const data = await fetchUpgradeForm(id.value, keepTarget ? targetId.value : undefined)
    form.value = data
    if (!keepTarget) targetId.value = 0
    if (data.target) ensureDefaults()
  } catch (err: unknown) {
    form.value = null
    loadError.value = true
    ElMessage.error((err as Error).message || '读取升降级信息失败')
  } finally {
    loading.value = false
  }
}
onMounted(() => load())
watch(id, () => load())

async function chooseTarget() {
  if (!targetId.value) {
    ElMessage.warning('请选择目标套餐')
    return
  }
  await load(true)
}

async function submit() {
  if (!target.value) return
  const up = diff.value > 0
  const title = isSame.value ? '确认变更' : up ? '确认升级' : '确认降级'
  const message = isSame.value
    ? '套餐价格无变化，确认提交变更？'
    : up
      ? `升级需补差价 ￥${formatMoney(diff.value)}（按剩余天数折算，以订单金额为准），确认下单？`
      : `降级不退还差价，确认继续？`
  const ok = await ElMessageBox.confirm(message, title, {
    type: 'warning',
    confirmButtonText: '确认',
  }).catch(() => null)
  if (!ok) return
  submitting.value = true
  try {
    const res = await upgradeService(id.value, target.value.id, cycle.value, { ...sel })
    if (String(res.ok) === '1') {
      if (res.redirect) {
        // 后台代管：下单后回服务详情（订单在「订单管理」里处理），勿跳前台支付页
        if (isAdminView.value) {
          ElMessage.success('订单已生成，请在「订单管理」中处理')
          router.push(`/services/${id.value}`)
          return
        }
        location.href = res.redirect
        return
      }
      ElMessage.success(res.msg || '已提交')
    } else {
      ElMessage.error(res.msg || '操作失败')
    }
  } catch (err: unknown) {
    const code = err instanceof ApiError ? err.data.code : undefined
    // 上游价格已变：本地已同步为新价，重载页面（含目标套餐价格）让用户按新价重新确认。
    if (code === 'price_changed') {
      ElMessage.warning((err as Error).message || '商品价格已更新，请重新确认')
      await load(true)
      return
    }
    ElMessage.error((err as Error).message || '操作失败')
  } finally {
    submitting.value = false
  }
}
</script>
<template>
  <div v-loading="loading">
    <template v-if="form">
      <PublicPageHead
        title="服务升降级"
        :subtitle="`${form.svc.name} · 当前套餐月价 ￥${formatMoney(form.current_monthly)}`"
      >
        <template #extra>
          <el-tag effect="light">{{ form.svc.status_text }}</el-tag>
        </template>
      </PublicPageHead>

      <div class="su-layout">
        <div class="su-main">
          <section class="art-card su-panel">
            <h2>目标套餐</h2>
            <p class="su-hint">选择要升级或降级到的套餐</p>
            <div class="su-target-row">
              <el-select
                v-model="targetId"
                aria-label="选择目标套餐"
                placeholder="选择目标套餐"
                style="min-width: 240px"
              >
                <el-option v-for="t in form.targets" :key="t.product_id" :value="t.product_id" :label="t.name" />
              </el-select>
              <el-button type="primary" plain :disabled="submitting" @click="chooseTarget">加载配置</el-button>
            </div>
            <el-empty v-if="!form.targets.length" description="该服务暂不支持升降级" :image-size="60" />
          </section>

          <section v-if="target" class="art-card su-panel">
            <h2>计费周期与配置</h2>
            <p class="su-hint">选择周期并调整实例规格</p>
            <el-select v-model="cycle" class="su-cycle" aria-label="计费周期">
              <el-option v-for="c in cycles" :key="c.key" :value="c.key" :label="c.label" />
            </el-select>
            <div class="su-configs">
              <div
                v-for="opt in target.options.filter((o) => !o.hidden)"
                :key="opt.field"
                :class="{ 'su-config--full': opt.field === 'os' }"
              >
                <span class="su-label">{{ opt.name }}{{ opt.unit ? `（${opt.unit}）` : '' }}</span>
                <OsSelector
                  v-if="opt.field === 'os'"
                  :subs="opt.sub || []"
                  :model-value="sel[opt.field]"
                  @update:model-value="(v: string) => (sel[opt.field] = v)"
                />
                <div v-else-if="opt.option_mode === 'range'" class="su-range">
                  <el-slider
                    class="flex-1"
                    :aria-label="opt.name"
                    :disabled="submitting"
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
                    style="width: 104px"
                    :disabled="submitting"
                    @update:model-value="(v: number | undefined) => (sel[opt.field] = String(v ?? opt.min ?? 0))"
                  />
                </div>
                <el-select v-else v-model="sel[opt.field]" class="w-full" :aria-label="opt.name" :disabled="submitting">
                  <el-option
                    v-for="(s, i) in opt.sub || []"
                    :key="choiceValue(opt.sub || [], s, i)"
                    :value="choiceValue(opt.sub || [], s, i)"
                    :label="s.name"
                  />
                </el-select>
              </div>
            </div>
          </section>
        </div>

        <aside class="art-card su-summary">
          <h2>费用变化</h2>
          <small class="su-summary__tip">最终金额以服务端核算为准</small>
          <template v-if="target">
            <div class="su-summary__lines">
              <div v-for="(l, i) in totals.lines" :key="`${l.name}-${i}`">
                <span>{{ l.name }}</span><em>{{ l.value }}</em><b>+￥{{ formatMoney(l.price) }}</b>
              </div>
              <div><span>当前月价</span><em></em><b>￥{{ formatMoney(form.current_monthly) }}</b></div>
              <div><span>目标月价</span><em></em><b>￥{{ formatMoney(totals.total) }}</b></div>
            </div>
            <div class="su-summary__diff" :class="isSame ? 'is-same' : diff > 0 ? 'is-up' : 'is-down'">
              <span>{{ isSame ? '价格无变化' : diff > 0 ? '需补差价' : '差价不退' }}</span>
              <div v-if="!isSame">
                <strong>￥{{ formatMoney(Math.abs(diff)) }}</strong>
                <small>{{ diff > 0 ? '升级' : '降级' }}</small>
              </div>
            </div>
            <p class="su-hint">实际差价按剩余天数折算，以订单金额为准；降级不退还差价。</p>
            <el-button type="primary" size="large" class="su-submit" :loading="submitting" @click="submit">
              {{ isSame ? '确认变更' : diff > 0 ? '确认升级' : '确认降级' }} <el-icon><ArrowRight /></el-icon>
            </el-button>
          </template>
          <el-empty v-else description="请先选择目标套餐" :image-size="60" />
        </aside>
      </div>
    </template>

    <div v-else-if="loadError" class="su-state" role="alert">
      <p>升降级信息加载失败，请稍后重试。</p>
      <el-button size="small" @click="load()">重新加载</el-button>
    </div>

    <el-empty v-else-if="!loading" description="服务不存在或当前状态不可升降级" />
  </div>
</template>

<style scoped>
.su-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 330px;
  gap: 16px;
  align-items: start;
}

.su-main {
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-width: 0;
}

.su-panel {
  padding: 21px;
}

.su-panel h2 {
  margin: 0;
  color: var(--art-gray-800);
  font-size: 15px;
  font-weight: 600;
}

.su-hint {
  margin: 4px 0 14px;
  color: var(--art-gray-500);
  font-size: 12px;
}

.su-target-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.su-cycle {
  width: 220px;
}

.su-configs {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
  margin-top: 16px;
}

.su-config--full {
  grid-column: 1 / -1;
}

.su-label {
  display: block;
  margin-bottom: 8px;
  color: var(--art-gray-600);
  font-size: 12px;
  font-weight: 600;
}

.su-range {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}

.su-range .flex-1 {
  flex: 1 1 160px;
  min-width: 0;
}

.su-summary {
  position: sticky;
  top: 88px;
  padding: 21px;
}

.su-summary h2 {
  margin: 0;
  color: var(--art-gray-900);
  font-size: 16px;
  font-weight: 600;
}

.su-summary__tip {
  display: block;
  margin-top: 4px;
  color: var(--art-gray-500);
  font-size: 11px;
}

.su-summary__lines {
  margin-top: 14px;
}

.su-summary__lines > div {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 0;
  font-size: 12px;
  border-bottom: 1px dashed var(--art-card-border);
}

.su-summary__lines span {
  color: var(--art-gray-600);
}

.su-summary__lines em {
  flex: 1;
  color: var(--art-gray-500);
  font-style: normal;
  text-align: right;
}

.su-summary__lines b {
  min-width: 62px;
  color: var(--art-gray-700);
  font-weight: 600;
  text-align: right;
}

.su-summary__diff {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 14px;
  padding: 13px;
  border-radius: calc(var(--custom-radius) / 2);
}

.su-summary__diff.is-up {
  background: var(--theme-color-soft);
}

.su-summary__diff.is-down {
  background: var(--el-color-success-light-9);
}

.su-summary__diff.is-same {
  background: var(--art-gray-100);
}

.su-summary__diff > span {
  color: var(--art-gray-600);
  font-size: 12px;
  font-weight: 600;
}

.su-summary__diff div {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 1px;
}

.su-summary__diff.is-up strong {
  color: var(--theme-color);
}

.su-summary__diff.is-down strong {
  color: var(--el-color-success);
}

.su-summary__diff strong {
  font-size: 19px;
  letter-spacing: -0.03em;
}

.su-summary__diff small {
  color: var(--art-gray-500);
  font-size: 10px;
  font-weight: 600;
}

.su-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 56px 20px;
  text-align: center;
}

.su-state p {
  margin: 0;
  color: var(--art-gray-600);
  font-size: 14px;
}

.su-submit {
  width: 100%;
  min-height: 44px;
  margin-top: 15px;
  justify-content: center;
  gap: 5px;
}

@media (max-width: 800px) {
  .su-layout {
    grid-template-columns: 1fr;
  }

  .su-summary {
    position: static;
  }

  .su-configs {
    grid-template-columns: 1fr;
  }

  .su-range .el-input-number {
    width: 100% !important;
  }
}
</style>
