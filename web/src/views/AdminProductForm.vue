<script setup lang="ts">
import { ref, reactive, computed, onMounted, watch, h } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElTag } from 'element-plus'
import type { ColumnOption } from '@/types'
import ArtButtonTable from '../components/core/forms/art-button-table/index.vue'
import {
  fetchAdminProductForm,
  saveAdminProduct,
  fetchUpstreamOptions,
  fetchUpstreamConfig,
  type AdminProductFormData,
  type ProductFormField,
  type ProductFormConfigOption,
  type ProductFormHints,
} from '../admin/api'

interface ConfigValue {
  name: string
  value?: string
  min?: number
  max?: number
  pricing?: Record<string, number>
  /** 各周期初装费（上游一次性费用，仅首购收取）：monthly/quarterly/yearly */
  setup?: Record<string, number>
}
interface ConfigOption {
  field: string
  name: string
  option_mode: string
  min?: number
  max?: number
  step?: number
  unit?: string
  required?: boolean
  hidden?: boolean
  sub?: ConfigValue[]
}
interface SubRow {
  name: string
  value: string
  monthly: string
  quarterly: string
  yearly: string
  setupMonthly: string
  setupQuarterly: string
  setupYearly: string
}

const configColumns: ColumnOption<ConfigOption>[] = [
  {
    prop: 'name',
    label: '显示名',
    minWidth: 140,
    formatter: (row) =>
      h('span', { class: 'inline-flex items-center gap-1' }, [
        row.name,
        row.hidden ? h(ElTag, { size: 'small', type: 'info' }, () => '隐藏') : null,
      ]),
  },
  { prop: 'field', label: '标识', minWidth: 110 },
  {
    prop: 'option_mode',
    label: '类型',
    width: 100,
    formatter: (row) => (row.option_mode === 'range' ? '数量范围' : '单选'),
  },
  {
    prop: 'sub',
    label: '子项 / 价格',
    minWidth: 200,
    formatter: (row) => {
      if (!row.sub || !row.sub.length) return h('span', { class: 'text-xs text-g-500' }, '-')
      return h(
        'span',
        { class: 'text-xs text-g-600' },
        row.sub.map((s, si) =>
          h('span', { key: si, class: 'mr-2' }, [
            s.name,
            s.pricing && s.pricing.monthly ? ` ￥${s.pricing.monthly}` : '',
            s.setup && s.setup.monthly ? ` +初装费￥${s.setup.monthly}` : '',
          ]),
        ),
      )
    },
  },
  {
    prop: 'operation',
    label: '操作',
    width: 120,
    fixed: 'right',
    formatter: (row) => {
      const index = configOptions.value.indexOf(row)
      return h('div', [
        h(ArtButtonTable, { type: 'edit', onClick: () => openCfgModal(index) }),
        h(ArtButtonTable, { type: 'delete', onClick: () => delCfg(index) }),
      ])
    },
  },
]

const route = useRoute()
const router = useRouter()
const id = computed(() => {
  const v = route.params.id
  return v ? Number(v) : undefined
})

const loading = ref(false)
const saving = ref(false)
const data = ref<AdminProductFormData | null>(null)

const form = reactive({
  name: '',
  description: '',
  type_id: '',
  server_id: '',
  upstream_pid: '',
  monthly: '0.00',
  quarterly: '',
  yearly: '',
  stock: '-1',
  hidden: false,
  requires_identity: false,
  profit_type: '0',
  profit_value: '0',
})

const specValues = reactive<Record<string, string>>({})
const dynGroups = reactive<Record<string, { name: string; options: { value: string; label: string }[] }[]>>({})
const configOptions = ref<ConfigOption[]>([])

const typeGroups = computed(() => {
  const all = data.value?.types || []
  const firsts = all.filter((t) => t.parent_id === 0)
  return firsts.map((f) => ({
    id: f.id,
    name: f.name,
    children: all.filter((t) => t.parent_id === f.id),
  }))
})

const currentProvider = computed(() => {
  const sid = Number(form.server_id)
  return (data.value?.servers || []).find((s) => s.id === sid)?.provider || ''
})
const specFields = computed<ProductFormField[]>(() => {
  const code = currentProvider.value
  if (!code || !data.value) return []
  return data.value.form_spec[code] || []
})
const hints = computed<ProductFormHints>(() => {
  const code = currentProvider.value
  return (code && data.value?.hints?.[code]) || {}
})
const markupFree = computed(() => !!hints.value.markupFree)
const fieldSuggestions = computed(() => hints.value.fieldSuggestions || [])
const pidField = computed(() => specFields.value.find((f) => f.key === 'upstream_pid'))
const otherFields = computed(() => specFields.value.filter((f) => f.key !== 'upstream_pid'))

function groupsFor(f: ProductFormField) {
  if (f.options_url && dynGroups[f.key]?.length) return dynGroups[f.key]
  const opts = f.options || []
  if (!opts.length) return []
  const byGroup = new Map<string, { value: string; label: string }[]>()
  for (const o of opts) {
    const g = o.group || ''
    if (!byGroup.has(g)) byGroup.set(g, [])
    byGroup.get(g)!.push({ value: o.value, label: o.label })
  }
  return [...byGroup.entries()].map(([name, options]) => ({ name, options }))
}
function flatOptions(f: ProductFormField) {
  const out: { value: string; label: string }[] = []
  for (const g of groupsFor(f)) {
    for (const o of g.options) out.push({ value: o.value, label: g.name ? `${g.name} / ${o.label}` : o.label })
  }
  return out
}
const pidOptions = computed(() => (pidField.value ? flatOptions(pidField.value) : []))

async function loadOptions(f: ProductFormField) {
  const sid = Number(form.server_id)
  if (!sid || !f.options_url) return
  try {
    const groups = await fetchUpstreamOptions(sid)
    dynGroups[f.key] = groups.map((g) => ({
      name: g.name,
      options: g.items.map((it) => ({ value: String(it.pid), label: `${it.name}（PID: ${it.pid}）` })),
    }))
  } catch (err) {
    ElMessage.error((err as Error).message || '拉取上游商品失败')
  }
}
async function refreshDynOptions() {
  for (const f of specFields.value) {
    if (f.options_url) await loadOptions(f)
  }
}

function optionLabel(value: string): string {
  for (const f of specFields.value) {
    for (const o of flatOptions(f)) if (o.value === value) return o.label
  }
  return ''
}
function fmt(n: number): string {
  return n ? n.toFixed(2) : ''
}

async function onPidChange() {
  const f = pidField.value
  if (f?.sync_name && form.upstream_pid) {
    const label = optionLabel(form.upstream_pid)
    if (label) form.name = label.replace(/（PID: \d+）$/, '')
  }
  if (f?.pull_config) await pullConfig()
}

async function pullConfig() {
  const sid = Number(form.server_id)
  const pid = Number(form.upstream_pid)
  if (!sid || !pid) return
  try {
    const r = await fetchUpstreamConfig(sid, pid)
    try {
      const parsed = JSON.parse(r.json)
      if (Array.isArray(parsed)) configOptions.value = parsed as ConfigOption[]
    } catch {
      // 上游返回非数组：保留现有配置
    }
    if (r.price.monthly > 0) form.monthly = fmt(r.price.monthly)
    if (r.price.quarterly > 0) form.quarterly = fmt(r.price.quarterly)
    if (r.price.yearly > 0) form.yearly = fmt(r.price.yearly)
    if (r.description) form.description = r.description
    if (r.stock !== -1) form.stock = String(r.stock)
    applyConfigByValue()
    ElMessage.success(`已拉取 ${r.count} 项配置`)
  } catch (err) {
    ElMessage.error((err as Error).message || '拉取上游配置失败')
  }
}

function removeManaged(field: string) {
  const i = configOptions.value.findIndex((o) => o.field === field && o.hidden)
  if (i >= 0) configOptions.value.splice(i, 1)
}
function ensureManaged(cfg: ProductFormConfigOption) {
  if (configOptions.value.some((o) => o.field === cfg.field && o.hidden)) return
  configOptions.value.push({
    field: cfg.field,
    name: cfg.name,
    option_mode: cfg.option_mode,
    hidden: cfg.hidden,
    sub: (cfg.sub || []).map((s) => ({ name: s.name, value: s.value })),
  })
}
function applyConfigByValue() {
  for (const f of specFields.value) {
    if (!f.config_by_value) continue
    const val = f.key === 'upstream_pid' ? form.upstream_pid : specValues[f.key] || ''
    const produced = f.config_by_value[val]
    for (const [v, cfg] of Object.entries(f.config_by_value)) {
      if (v !== val) removeManaged(cfg.field)
    }
    if (produced) ensureManaged(produced)
  }
}

let hydrating = false
async function load() {
  loading.value = true
  hydrating = true
  try {
    const d = await fetchAdminProductForm(id.value)
    data.value = d
    if (d.product) {
      form.name = d.product.name || ''
      form.description = d.product.description || ''
      form.type_id = d.product.type_id ? String(d.product.type_id) : ''
      form.server_id = d.product.server_id ? String(d.product.server_id) : ''
      form.upstream_pid = d.product.upstream_pid ? String(d.product.upstream_pid) : ''
      form.stock = String(d.product.stock ?? -1)
      form.hidden = !!d.product.hidden
      form.requires_identity = !!d.product.requires_identity
      form.profit_type = String(d.product.profit_type ?? 0)
      form.profit_value = String(d.product.profit_value ?? 0)
    }
    form.monthly = d.prices?.monthly || form.monthly
    form.quarterly = d.prices?.quarterly || ''
    form.yearly = d.prices?.yearly || ''
    try {
      const parsed = JSON.parse(d.config_json || '[]')
      configOptions.value = Array.isArray(parsed) ? (parsed as ConfigOption[]) : []
    } catch {
      configOptions.value = []
    }
    for (const f of specFields.value) {
      if (f.key === 'upstream_pid') continue
      specValues[f.key] = ''
      // 已有隐藏配置项 → 反推站点类型等瞬态字段当前值
      if (f.config_by_value) {
        for (const [v, cfg] of Object.entries(f.config_by_value)) {
          if (configOptions.value.some((o) => o.field === cfg.field && o.hidden)) specValues[f.key] = v
        }
      }
    }
    await refreshDynOptions()
  } catch (err) {
    ElMessage.error((err as Error).message || '读取产品失败')
  } finally {
    hydrating = false
    loading.value = false
  }
}
onMounted(load)
watch(id, load)

watch(
  () => form.server_id,
  async (nv, ov) => {
    if (hydrating || nv === ov) return
    for (const k of Object.keys(dynGroups)) delete dynGroups[k]
    for (const k of Object.keys(specValues)) specValues[k] = ''
    if (nv !== ov) form.upstream_pid = ''
    applyConfigByValue()
    await refreshDynOptions()
  },
)

const showCfg = ref(false)
const cfgEditIdx = ref(-1)
const cfg = reactive({ field: '', name: '', option_mode: 'select', unit: '', min: '', max: '', step: '' })
const cfgSubs = ref<SubRow[]>([])

function openCfgModal(idx: number) {
  cfgEditIdx.value = idx
  const o = idx >= 0 ? configOptions.value[idx] : null
  cfg.field = o?.field || ''
  cfg.name = o?.name || ''
  cfg.option_mode = o?.option_mode || 'select'
  cfg.unit = o?.unit || ''
  cfg.min = o && o.min != null ? String(o.min) : ''
  cfg.max = o && o.max != null ? String(o.max) : ''
  cfg.step = o && o.step != null ? String(o.step) : ''
  cfgSubs.value = (o?.sub || []).map((s) => ({
    name: s.name || '',
    value: s.value || '',
    monthly: s.pricing?.monthly != null ? String(s.pricing.monthly) : '',
    quarterly: s.pricing?.quarterly != null ? String(s.pricing.quarterly) : '',
    yearly: s.pricing?.yearly != null ? String(s.pricing.yearly) : '',
    setupMonthly: s.setup?.monthly != null ? String(s.setup.monthly) : '',
    setupQuarterly: s.setup?.quarterly != null ? String(s.setup.quarterly) : '',
    setupYearly: s.setup?.yearly != null ? String(s.setup.yearly) : '',
  }))
  showCfg.value = true
}
function addSubRow() {
  cfgSubs.value.push({
    name: '', value: '', monthly: '', quarterly: '', yearly: '',
    setupMonthly: '', setupQuarterly: '', setupYearly: '',
  })
}
function removeSubRow(i: number) {
  cfgSubs.value.splice(i, 1)
}
function saveCfgModal() {
  const field = cfg.field.trim() || cfg.name.trim().toLowerCase().replace(/\s+/g, '_')
  const name = cfg.name.trim()
  if (!field || !name) {
    ElMessage.warning('请填写配置标识与显示名')
    return
  }
  const opt: ConfigOption = { field, name, option_mode: cfg.option_mode, unit: cfg.unit.trim() }
  const prev = cfgEditIdx.value >= 0 ? configOptions.value[cfgEditIdx.value] : null
  if (prev?.hidden) opt.hidden = true
  if (cfg.option_mode === 'range') {
    opt.min = parseFloat(cfg.min) || 0
    opt.max = parseFloat(cfg.max) || 0
    opt.step = parseFloat(cfg.step) || 1
  }
  const subs: ConfigValue[] = []
  for (const r of cfgSubs.value) {
    if (!r.name.trim()) continue
    const s: ConfigValue = { name: r.name.trim() }
    if (r.value.trim()) s.value = r.value.trim()
    const pricing: Record<string, number> = {}
    const m = parseFloat(r.monthly)
    const q = parseFloat(r.quarterly)
    const y = parseFloat(r.yearly)
    if (!isNaN(m)) pricing.monthly = m
    if (!isNaN(q)) pricing.quarterly = q
    if (!isNaN(y)) pricing.yearly = y
    if (Object.keys(pricing).length) s.pricing = pricing
    // 初装费（一次性）：必须原样带回，漏掉就会被保存动作抹掉（上游同步来的值会凭空消失）
    const setup: Record<string, number> = {}
    const sm = parseFloat(r.setupMonthly)
    const sq = parseFloat(r.setupQuarterly)
    const sy = parseFloat(r.setupYearly)
    if (!isNaN(sm)) setup.monthly = sm
    if (!isNaN(sq)) setup.quarterly = sq
    if (!isNaN(sy)) setup.yearly = sy
    if (Object.keys(setup).length) s.setup = setup
    subs.push(s)
  }
  if (subs.length) opt.sub = subs
  if (cfgEditIdx.value >= 0) configOptions.value[cfgEditIdx.value] = opt
  else configOptions.value.push(opt)
  showCfg.value = false
}
function delCfg(i: number) {
  configOptions.value.splice(i, 1)
}

const showJson = ref(false)
const configJsonText = ref('')
function toggleJson() {
  if (!showJson.value) configJsonText.value = JSON.stringify(configOptions.value, null, 2)
  showJson.value = !showJson.value
}
function applyJson(): boolean {
  try {
    const parsed = JSON.parse(configJsonText.value || '[]')
    if (!Array.isArray(parsed)) throw new Error('必须是数组')
    configOptions.value = parsed as ConfigOption[]
    return true
  } catch (e) {
    ElMessage.error('配置项 JSON 格式错误: ' + (e as Error).message)
    return false
  }
}
watch(
  configOptions,
  () => {
    if (showJson.value) configJsonText.value = JSON.stringify(configOptions.value, null, 2)
  },
  { deep: true },
)

async function save() {
  if (!form.name.trim()) {
    ElMessage.warning('请填写产品名称')
    return
  }
  if (!form.type_id) {
    ElMessage.warning('请选择二级分类')
    return
  }
  if (showJson.value && !applyJson()) return
  saving.value = true
  try {
    await saveAdminProduct(id.value, {
      name: form.name.trim(),
      description: form.description,
      type_id: Number(form.type_id),
      server_id: form.server_id ? Number(form.server_id) : '',
      upstream_pid: form.upstream_pid ? Number(form.upstream_pid) : 0,
      monthly: form.monthly,
      quarterly: form.quarterly,
      yearly: form.yearly,
      stock: Number(form.stock),
      hidden: form.hidden ? '1' : '0',
      requires_identity: form.requires_identity ? '1' : '0',
      profit_type: markupFree.value ? '0' : form.profit_type,
      profit_value: markupFree.value ? '0' : form.profit_value,
      configoption: JSON.stringify(configOptions.value),
    })
    ElMessage.success('已保存')
    router.push('/products')
  } catch (err) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="art-full-height" v-loading="loading">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>{{ id ? '编辑产品' : '新增产品' }}</h4>
            <p>维护产品信息、售价、库存和购买配置。</p>
          </div>
        </div>
      </template>

      <el-form label-position="top">
        <el-divider content-position="left">基本信息</el-divider>
        <div class="grid gap-4 sm:grid-cols-2">
          <el-form-item label="产品名称" required>
            <el-input v-model="form.name" placeholder="如 香港 2核2G 云服务器" />
          </el-form-item>
          <el-form-item label="所属分类" required>
            <el-select v-model="form.type_id" filterable placeholder="请选择二级分类" class="w-full">
              <el-option-group v-for="g in typeGroups" :key="g.id" :label="g.name">
                <el-option v-for="c in g.children" :key="c.id" :value="String(c.id)" :label="c.name" />
              </el-option-group>
            </el-select>
          </el-form-item>
        </div>
        <el-form-item label="产品描述">
          <el-input v-model="form.description" type="textarea" :rows="3" placeholder="前台商品卡片会展示这段描述" />
        </el-form-item>

        <el-divider content-position="left">上游自动开通</el-divider>
        <el-form-item label="绑定上游服务器">
          <el-select v-model="form.server_id" clearable placeholder="不自动开通（仅本地服务）" class="w-full">
            <el-option
              v-for="s in data?.servers || []"
              :key="s.id"
              :value="String(s.id)"
              :label="`${s.name}（${s.provider}）`"
            />
          </el-select>
        </el-form-item>

        <template v-if="form.server_id">
          <el-form-item v-if="pidField && pidField.type === 'select'" :label="pidField.label" required>
            <el-select
              v-model="form.upstream_pid"
              filterable
              clearable
              placeholder="搜索并选择上游商品"
              class="w-full"
              @change="onPidChange()"
            >
              <el-option v-for="o in pidOptions" :key="o.value" :value="o.value" :label="o.label" />
            </el-select>
            <div v-if="pidField.hint" class="mt-1 text-xs text-g-500">{{ pidField.hint }}</div>
          </el-form-item>
          <el-form-item v-else label="上游商品 PID">
            <el-input v-model="form.upstream_pid" placeholder="填上游商品 ID（弹性模式可留空）" />
            <div v-if="hints.pidHint" class="mt-1 text-xs text-g-500">{{ hints.pidHint }}</div>
          </el-form-item>

          <el-form-item v-for="f in otherFields" :key="f.key" :label="f.label">
            <el-select
              v-if="f.type === 'select'"
              v-model="specValues[f.key]"
              clearable
              class="w-full"
              style="max-width: 300px"
              @change="applyConfigByValue()"
            >
              <el-option v-for="o in flatOptions(f)" :key="o.value" :value="o.value" :label="o.label" />
            </el-select>
            <el-input v-else v-model="specValues[f.key]" style="max-width: 300px" />
            <div v-if="f.hint" class="mt-1 text-xs text-g-500">{{ f.hint }}</div>
          </el-form-item>
        </template>

        <el-divider content-position="left">价格（元）</el-divider>
        <div class="grid gap-4 sm:grid-cols-3">
          <el-form-item label="月付">
            <el-input v-model="form.monthly" placeholder="0.00" />
          </el-form-item>
          <el-form-item label="季付">
            <el-input v-model="form.quarterly" placeholder="0.00" />
          </el-form-item>
          <el-form-item label="年付">
            <el-input v-model="form.yearly" placeholder="0.00" />
          </el-form-item>
        </div>

        <template v-if="!markupFree">
          <el-divider content-position="left">利润加成</el-divider>
          <p class="mb-3 mt-0 text-xs text-g-500">合计 =（基础价+配置费用）×(1+比例%) 或 +固定金额。0 表示不加成。</p>
          <div class="grid gap-4 sm:grid-cols-2">
            <el-form-item label="利润方式">
              <el-select v-model="form.profit_type" class="w-full">
                <el-option label="按比例（%）" value="0" />
                <el-option label="固定金额（元）" value="1" />
              </el-select>
            </el-form-item>
            <el-form-item label="利润值">
              <el-input v-model="form.profit_value" placeholder="如 20 表示 20% 或 20 元" />
            </el-form-item>
          </div>
        </template>

        <el-divider content-position="left">销售设置</el-divider>
        <div class="grid gap-4 sm:grid-cols-3">
          <el-form-item label="库存（-1 为不限）">
            <el-input v-model="form.stock" type="number" />
          </el-form-item>
          <el-form-item label="前台显示">
            <el-switch v-model="form.hidden" active-text="隐藏" inactive-text="显示" />
          </el-form-item>
          <el-form-item label="购买实名认证">
            <el-switch v-model="form.requires_identity" active-text="需要" inactive-text="不需要" />
          </el-form-item>
        </div>
        <p class="text-xs text-g-500">开启实名后，用户必须通过人工或已配置实名插件后才能购买或续费此产品。</p>

        <el-divider content-position="left">配置项</el-divider>
        <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
          <p class="m-0 text-xs text-g-500">购买时展示，可选。选中上游商品可自动拉取。</p>
          <div class="flex gap-2">
            <el-button size="small" @click="toggleJson">高级 JSON</el-button>
            <el-button size="small" type="primary" @click="openCfgModal(-1)">+ 新增配置项</el-button>
          </div>
        </div>
        <ArtTable
          v-if="!showJson"
          :data="configOptions"
          :columns="configColumns"
          :height="360"
          :show-table-header="false"
          border
          empty-text="暂无配置项"
        />
        <el-input v-else v-model="configJsonText" type="textarea" :rows="12" />
        <p class="mt-2 text-xs text-g-500">select 型子项填加价；range 型填数量区间与单价。</p>

        <div class="mt-4 flex gap-2">
          <el-button type="primary" :loading="saving" @click="save">保存产品</el-button>
          <el-button @click="router.push('/products')">取消</el-button>
        </div>
      </el-form>
    </ElCard>

    <el-dialog v-model="showCfg" :title="cfgEditIdx >= 0 ? '编辑配置项' : '新增配置项'" width="680px">
      <el-form label-position="top">
        <div class="grid gap-4 sm:grid-cols-2">
          <el-form-item label="配置标识 field">
            <el-select v-model="cfg.field" filterable allow-create default-first-option class="w-full" placeholder="如 cpu / memory / web_quota">
              <el-option v-for="s in fieldSuggestions" :key="s.field" :value="s.field" :label="`${s.field}（${s.label}）`" />
            </el-select>
          </el-form-item>
          <el-form-item label="显示名 name">
            <el-input v-model="cfg.name" />
          </el-form-item>
          <el-form-item label="类型">
            <el-select v-model="cfg.option_mode" class="w-full">
              <el-option label="单选 select" value="select" />
              <el-option label="数量范围 range" value="range" />
            </el-select>
          </el-form-item>
          <el-form-item label="单位 unit（可空）">
            <el-input v-model="cfg.unit" placeholder="核 / GB / Mbps" />
          </el-form-item>
        </div>
        <div v-if="cfg.option_mode === 'range'" class="grid gap-4 sm:grid-cols-3">
          <el-form-item label="最小值 min"><el-input v-model="cfg.min" type="number" /></el-form-item>
          <el-form-item label="最大值 max"><el-input v-model="cfg.max" type="number" /></el-form-item>
          <el-form-item label="步长 step"><el-input v-model="cfg.step" type="number" /></el-form-item>
        </div>

        <el-divider content-position="left">子项 / 价格档位</el-divider>
        <div v-for="(r, i) in cfgSubs" :key="i" class="mb-2 flex flex-wrap items-center gap-2">
          <el-input v-model="r.name" placeholder="显示名" style="width: 150px" />
          <el-input v-model="r.value" placeholder="提交值(可空)" style="width: 140px" />
          <el-input v-model="r.monthly" placeholder="月付" style="width: 90px" />
          <el-input v-model="r.quarterly" placeholder="季付" style="width: 90px" />
          <el-input v-model="r.yearly" placeholder="年付" style="width: 90px" />
          <el-input v-model="r.setupMonthly" placeholder="初装费月付" style="width: 110px" />
          <el-input v-model="r.setupQuarterly" placeholder="初装费季付" style="width: 110px" />
          <el-input v-model="r.setupYearly" placeholder="初装费年付" style="width: 110px" />
          <el-button text type="danger" @click="removeSubRow(i)">×</el-button>
        </div>
        <el-button size="small" @click="addSubRow">+ 子项</el-button>
      </el-form>
      <template #footer>
        <el-button @click="showCfg = false">取消</el-button>
        <el-button type="primary" @click="saveCfgModal">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>
