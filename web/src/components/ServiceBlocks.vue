<script setup lang="ts">
/**
 * 功能面板（纯 Vue）：上游模块方块（NAT 转发 / 共享建站 / 安全组 / 实例设置 / 快照备份）。
 * 数据与操作走本站 JSON 端点（/blocks、/block/{fn}、/block-rules、/snapshot*），
 * 请求字段名对齐上游表单（与 zjmf widget.html 一致），勿随意改名。
 * ponytail: 仅覆盖上述已知方块 key；上游新增未知模块时本组件不展示，必要时后端扩 key。
 */
import { computed, h, onMounted, ref, watch } from 'vue'
import { ElButton, ElMessage, ElMessageBox } from 'element-plus'
import type { ColumnOption } from '@/types'
import {
  blockAction, fetchBlockRules, fetchServiceBlocks, fetchSnapshotInfo, snapshotAction,
  type NatRule, type NatWebEntry, type SecurityGroup, type SecurityRule, type SnapshotInfo, type SnapshotItem,
} from '../api/user'

const props = withDefaults(defineProps<{
  serviceId: number
  areas: string[]
  activeTab?: string
  showTabs?: boolean
}>(), {
  activeTab: '',
  showTabs: true,
})

const TABS = [
  { key: 'nat_acl', label: 'NAT 转发' },
  { key: 'nat_web', label: '共享建站' },
  { key: 'security_groups', label: '安全组' },
  { key: 'setting', label: '实例设置' },
  { key: 'snapshot', label: '快照/备份' },
]
const tabs = computed(() => TABS.filter((t) => props.areas.includes(t.key)))
// tabs 模式：组件内自管理激活页；嵌入模式：由父组件 activeTab 驱动（el-tabs 头部以 CSS 隐藏）。
const active = ref(props.showTabs ? '' : props.activeTab)
watch(
  () => props.activeTab,
  (v) => {
    if (!props.showTabs && v) active.value = v
  },
)

const loading = ref(false)
const busy = ref(false)
const refreshing = ref(false)
const loadErr = ref('')
const nat = ref<NatRule[]>([])
const web = ref<NatWebEntry[]>([])
const groups = ref<SecurityGroup[]>([])
const iso = ref<{ value: string; name: string; selected: boolean }[]>([])
const boot = ref<{ value: string; name: string; selected: boolean }[]>([])
const snap = ref<SnapshotInfo | null>(null)

// ---- 表单 ----
const natName = ref(''); const natInt = ref(''); const natExt = ref(''); const natProto = ref('1')
const webDomain = ref(''); const webInt = ref(''); const webExt = ref('80')
const sgName = ref(''); const sgDesc = ref('')
const isoVal = ref(''); const bootVal = ref('')
const snapDisk = ref<number | ''>(''); const snapName = ref('')

// ---- ArtTable 列定义（formatter 即 el-table-column formatter，首参为 row）----
const fitH = (n: number) => Math.min(420, Math.max(120, n * 40 + 46))

const natCols = computed<ColumnOption<NatRule>[]>(() => [
  { prop: 'name', label: '名称', minWidth: 120 },
  { prop: 'external', label: '外部地址', minWidth: 150 },
  { prop: 'internal', label: '内部端口', width: 100 },
  { prop: 'protocol', label: '协议', width: 80 },
  {
    label: '操作',
    width: 90,
    formatter: (row) =>
      row.id > 0
        ? h(ElButton, { size: 'small', type: 'danger', text: true, disabled: busy.value, onClick: () => natDel(row) }, () => '删除')
        : h('span', { class: 'text-xs text-g-500' }, '默认'),
  },
])

const webCols = computed<ColumnOption<NatWebEntry>[]>(() => [
  { prop: 'domain', label: '域名', minWidth: 160 },
  { prop: 'external', label: '外部端口', width: 110 },
  { prop: 'internal', label: '内部端口', width: 110 },
  {
    label: '操作',
    width: 90,
    formatter: (row) =>
      h(ElButton, { size: 'small', type: 'danger', text: true, disabled: busy.value, onClick: () => webDel(row) }, () => '删除'),
  },
])

const groupCols = computed<ColumnOption<SecurityGroup>[]>(() => [
  { prop: 'name', label: '名称', minWidth: 130 },
  { label: '描述', minWidth: 150, formatter: (row) => row.description || '-' },
  {
    label: '操作',
    width: 200,
    formatter: (row) =>
      h('div', { style: 'display:flex;gap:6px;align-items:center;white-space:nowrap' }, [
        h(ElButton, { size: 'small', text: true, type: 'primary', style: 'margin-left:0', onClick: () => openRules(row) }, () => '规则'),
        h(ElButton, { size: 'small', text: true, style: 'margin-left:0', disabled: busy.value, onClick: () => sgLink(row) }, () => '应用'),
        h(ElButton, { size: 'small', text: true, type: 'danger', style: 'margin-left:0', disabled: busy.value, onClick: () => sgDel(row) }, () => '删除'),
      ]),
  },
])

const snapCols = computed<ColumnOption<SnapshotItem>[]>(() => [
  { prop: 'name', label: '名称', minWidth: 130 },
  { label: '类型', width: 80, formatter: (row) => (row.type === 'snap' ? '快照' : '备份') },
  { prop: 'create_time', label: '创建时间', width: 160 },
  { label: '备注', minWidth: 110, formatter: (row) => row.remarks || '-' },
  {
    label: '操作',
    width: 150,
    formatter: (row) =>
      row.status === 1
        ? h('div', { style: 'display:flex;gap:6px;align-items:center;white-space:nowrap' }, [
            h(ElButton, { size: 'small', text: true, type: 'primary', style: 'margin-left:0', disabled: busy.value, onClick: () => snapAct(row, 'restore') }, () => '恢复'),
            h(ElButton, { size: 'small', text: true, type: 'danger', style: 'margin-left:0', disabled: busy.value, onClick: () => snapAct(row, 'del') }, () => '删除'),
          ])
        : h('span', { style: 'color:var(--el-color-warning);font-size:12px' }, '处理中'),
  },
])

const ruleCols = computed<ColumnOption<SecurityRule>[]>(() => [
  { label: '描述', minWidth: 110, formatter: (row) => row.description || '-' },
  { label: '策略', width: 70, formatter: (row) => (row.action === 'accept' ? '允许' : '拒绝') },
  { label: '方向', width: 70, formatter: (row) => (row.direction === 'in' ? '入' : '出') },
  { prop: 'protocol', label: '协议', width: 80 },
  { label: '端口', width: 110, formatter: (row) => row.port_range || '-' },
  { label: 'IP', minWidth: 120, formatter: (row) => row.ip || '-' },
  {
    label: '操作',
    width: 80,
    formatter: (row) => h(ElButton, { size: 'small', text: true, type: 'danger', disabled: busy.value, onClick: () => ruleDel(row) }, () => '删除'),
  },
])

// 面板数据的请求代次：服务切换后丢弃旧服务的响应，防止旧数据覆盖新面板
let loadSeq = 0

async function load() {
  if (!tabs.value.length) return
  if (!tabs.value.some((t) => t.key === active.value)) active.value = tabs.value[0].key
  const seq = ++loadSeq
  loading.value = true
  loadErr.value = ''
  try {
    await Promise.all(reloadJobs())
  } catch {
    if (seq !== loadSeq) return
    // 上游偶发超时：自动重试一次，仍失败才显示错误
    try {
      await Promise.all(reloadJobs())
    } catch (err: unknown) {
      if (seq !== loadSeq) return
      loadErr.value = (err as Error).message || '面板数据拉取失败'
    }
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

// 拉取方块/快照数据并写入本地状态（load 与操作后刷新共用）。
// 以发起时的 serviceId 为准，响应返回时服务已切换则丢弃，防止旧实例数据渲染到新面板。
function reloadJobs(): Promise<unknown>[] {
  const sid = props.serviceId
  const stale = () => props.serviceId !== sid
  const jobs: Promise<unknown>[] = []
  if (tabs.value.some((t) => t.key !== 'snapshot')) {
    jobs.push(fetchServiceBlocks(sid).then((b) => {
      if (stale()) return
      nat.value = b.nat_acl || []
      web.value = b.nat_web || []
      groups.value = b.security_groups || []
      iso.value = b.setting?.iso || []
      boot.value = b.setting?.boot || []
      isoVal.value = iso.value.find((x) => x.selected)?.value || ''
      bootVal.value = boot.value.find((x) => x.selected)?.value || ''
    }))
  }
  if (props.areas.includes('snapshot')) {
    jobs.push(fetchSnapshotInfo(sid).then((i) => {
      if (stale()) return
      snap.value = i
      snapDisk.value = i.disk[0]?.id ?? ''
    }))
  }
  return jobs
}
onMounted(load)
watch(() => props.serviceId, load)

// 统一动作：可选确认 → 执行 → 提示 → 本地更新 → 后台刷新。
// 刷新不阻塞不报错：上游慢时 UI 已先行更新，避免"已删除的行还挂着再点报 ID 错误"。
// 返回是否成功：调用方仅在成功时清空表单，失败保留用户输入供重试。
async function submit(opts: {
  confirm?: string
  exec: () => Promise<{ ok?: number | string | boolean; msg?: string }>
  after?: () => void
  reload?: () => Promise<void> | void
}): Promise<boolean> {
  if (opts.confirm) {
    const go = await ElMessageBox.confirm(opts.confirm, '操作确认', { confirmButtonText: '确认' }).catch(() => false)
    if (!go) return false
  }
  busy.value = true
  try {
    const res = await opts.exec()
    // 上游结果经 moduleResultJSON 归一，ok 可能是 1 或 true
    const ok = res.ok === true || String(res.ok) === '1'
    if (ok) {
      ElMessage.success(res.msg || '操作成功')
      opts.after?.()
      // 成功后立即后台重新拉取，保证列表与上游一致
      if (opts.reload) void Promise.resolve(opts.reload()).catch(() => {})
      return true
    }
    ElMessage.error(res.msg || '操作失败')
    return false
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '操作失败')
    return false
  } finally {
    busy.value = false
  }
}

async function reloadBlocks() {
  refreshing.value = true
  try {
    const b = await fetchServiceBlocks(props.serviceId)
    nat.value = b.nat_acl || []
    web.value = b.nat_web || []
    groups.value = b.security_groups || []
    iso.value = b.setting?.iso || []
    boot.value = b.setting?.boot || []
  } finally {
    refreshing.value = false
  }
}
async function reloadSnapshot() {
  refreshing.value = true
  try {
    snap.value = await fetchSnapshotInfo(props.serviceId)
  } finally {
    refreshing.value = false
  }
}

// ---- NAT 转发 ----
function natAdd() {
  if (!natInt.value) { ElMessage.warning('请填写内部端口'); return }
  submit({
    exec: () => blockAction(props.serviceId, 'addNatAcl', {
      name: natName.value || `nat-${Date.now()}`,
      int_port: natInt.value,
      ext_port: natExt.value,
      'select-protocol': natProto.value,
    }),
    reload: reloadBlocks,
  }).then((ok) => {
    if (ok) { natName.value = ''; natInt.value = ''; natExt.value = '' }
  })
}
function natDel(row: NatRule) {
  submit({ confirm: '确认删除该转发？', exec: () => blockAction(props.serviceId, 'delNatAcl', { id: row.id }), reload: reloadBlocks })
}

// ---- 共享建站 ----
function webAdd() {
  if (!webDomain.value || !webInt.value) { ElMessage.warning('请填写域名与内部端口'); return }
  submit({
    exec: () => blockAction(props.serviceId, 'addNatWeb', { domain: webDomain.value, int_port: webInt.value, ext_port: webExt.value }),
    reload: reloadBlocks,
  }).then((ok) => {
    if (ok) { webDomain.value = ''; webInt.value = '' }
  })
}
function webDel(row: NatWebEntry) {
  submit({ confirm: '确认删除该站点？', exec: () => blockAction(props.serviceId, 'delNatWeb', { id: row.id }), reload: reloadBlocks })
}

// ---- 安全组 ----
function sgAdd() {
  if (!sgName.value) { ElMessage.warning('请填写安全组名称'); return }
  submit({
    exec: () => blockAction(props.serviceId, 'createSecurityGroup', { name: sgName.value, description: sgDesc.value }),
    reload: reloadBlocks,
  }).then((ok) => {
    if (ok) { sgName.value = ''; sgDesc.value = '' }
  })
}
function sgLink(row: SecurityGroup) {
  submit({ confirm: '确认将该安全组应用到本实例？', exec: () => blockAction(props.serviceId, 'linkSecurityGroup', { id: row.id }), reload: reloadBlocks })
}
function sgDel(row: SecurityGroup) {
  submit({ confirm: '确认删除该安全组？', exec: () => blockAction(props.serviceId, 'delSecurityGroup', { id: row.id }), reload: reloadBlocks })
}

// ---- 安全组规则（弹窗） ----
const rulesDialog = ref(false)
const rulesGid = ref(0)
const rules = ref<SecurityRule[]>([])
const rulesLoading = ref(false)
const ruleForm = ref({ direction: 'in', protocol: 'all', port: '', ip: '', description: '' })

async function openRules(g: SecurityGroup) {
  rulesGid.value = g.id
  rulesDialog.value = true
  await reloadRules()
}
async function reloadRules() {
  rulesLoading.value = true
  try {
    rules.value = await fetchBlockRules(props.serviceId, rulesGid.value)
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '规则拉取失败')
  } finally {
    rulesLoading.value = false
  }
}
function ruleAdd() {
  if (!ruleForm.value.port || !ruleForm.value.ip) { ElMessage.warning('请填写端口与 IP'); return }
  submit({
    exec: () => blockAction(props.serviceId, 'createSecurityRule', {
      id: rulesGid.value,
      direction: ruleForm.value.direction,
      protocol: ruleForm.value.protocol,
      port: ruleForm.value.port,
      ip: ruleForm.value.ip,
      description: ruleForm.value.description,
    }),
    reload: reloadRules,
  }).then((ok) => {
    if (ok) ruleForm.value = { direction: 'in', protocol: 'all', port: '', ip: '', description: '' }
  })
}
function ruleDel(row: SecurityRule) {
  submit({
    confirm: '确认删除该规则？',
    exec: () => blockAction(props.serviceId, 'delSecurityRule', { id: row.id }),
    after: () => { rules.value = rules.value.filter((x) => x.id !== row.id) },
    reload: reloadRules,
  })
}

// ---- 实例设置 ----
function isoMount() {
  if (!isoVal.value) { ElMessage.warning('请选择要挂载的 ISO'); return }
  submit({ confirm: '确认挂载所选 ISO？', exec: () => blockAction(props.serviceId, 'mountIso', { id: isoVal.value }), reload: reloadBlocks })
}
function bootSet() {
  if (!bootVal.value) { ElMessage.warning('请选择启动顺序'); return }
  submit({ confirm: '确认更改启动顺序？', exec: () => blockAction(props.serviceId, 'setBootOrder', { id: bootVal.value }), reload: reloadBlocks })
}

// ---- 快照 / 备份 ----
function snapCreate(kind: 'snap' | 'backup') {
  if (!snapDisk.value) { ElMessage.warning('没有可用磁盘'); return }
  submit({
    exec: () => snapshotAction(props.serviceId, kind === 'snap' ? 'createSnap' : 'createBackup', {
      disk_id: snapDisk.value as number,
      name: snapName.value || `auto-${Date.now()}`,
    }),
    reload: reloadSnapshot,
  }).then((ok) => {
    if (ok) snapName.value = ''
  })
}
function snapAct(row: { id: number; type: string }, fn: 'restore' | 'del') {
  const f = row.type === 'snap' ? (fn === 'restore' ? 'restoreSnap' : 'delSnap') : fn === 'restore' ? 'restoreBackup' : 'delBackup'
  submit({
    confirm: fn === 'restore' ? '确认恢复该' + (row.type === 'snap' ? '快照' : '备份') + '？' : '确认删除该' + (row.type === 'snap' ? '快照' : '备份') + '？',
    exec: () => snapshotAction(props.serviceId, f, { id: row.id }),
    after: fn === 'del' ? () => { if (snap.value) snap.value.list = snap.value.list.filter((x) => x.id !== row.id) } : undefined,
    reload: reloadSnapshot,
  })
}
</script>

<template>
  <div class="art-card p-5" :class="{ 'service-blocks-embedded': !showTabs }" v-loading="loading || refreshing" element-loading-text="刷新中…">
    <h2 v-if="showTabs" class="mb-3 text-sm font-medium text-g-800">功能面板</h2>

    <el-alert v-if="loadErr" type="error" :title="loadErr" show-icon class="mb-3">
      <el-button size="small" text type="primary" @click="load">重试</el-button>
    </el-alert>

    <el-tabs v-if="!loadErr" v-model="active">
      <!-- NAT 转发 -->
      <el-tab-pane v-if="areas.includes('nat_acl')" label="NAT 转发" name="nat_acl">
        <div class="mb-3 flex flex-wrap items-center gap-2">
          <el-input v-model="natName" placeholder="名称（留空自动生成）" style="width: 170px" size="small" />
          <el-input v-model="natInt" type="number" placeholder="内部端口" style="width: 110px" size="small" />
          <el-input v-model="natExt" type="number" placeholder="外部端口（留空随机）" style="width: 160px" size="small" />
          <el-select v-model="natProto" style="width: 90px" size="small">
            <el-option value="1" label="TCP" />
            <el-option value="2" label="UDP" />
          </el-select>
          <el-button type="primary" size="small" :loading="busy" @click="natAdd">创建转发</el-button>
        </div>
        <ArtTable :data="nat" :columns="natCols" :height="fitH(nat.length)" :empty-height="'120px'" empty-text="暂无转发规则" size="small" border :show-table-header="false" />
      </el-tab-pane>

      <!-- 共享建站 -->
      <el-tab-pane v-if="areas.includes('nat_web')" label="共享建站" name="nat_web">
        <div class="mb-3 flex flex-wrap items-center gap-2">
          <el-input v-model="webDomain" placeholder="域名" style="width: 190px" size="small" />
          <el-input v-model="webInt" type="number" placeholder="内部端口" style="width: 110px" size="small" />
          <el-select v-model="webExt" style="width: 90px" size="small">
            <el-option value="80" label="80" />
            <el-option value="443" label="443" />
          </el-select>
          <el-button type="primary" size="small" :loading="busy" @click="webAdd">创建站点</el-button>
        </div>
        <ArtTable :data="web" :columns="webCols" :height="fitH(web.length)" :empty-height="'120px'" empty-text="暂无站点" size="small" border :show-table-header="false" />
      </el-tab-pane>

      <!-- 安全组 -->
      <el-tab-pane v-if="areas.includes('security_groups')" label="安全组" name="security_groups">
        <div class="mb-3 flex flex-wrap items-center gap-2">
          <el-input v-model="sgName" placeholder="安全组名称" style="width: 170px" size="small" />
          <el-input v-model="sgDesc" placeholder="备注" style="width: 170px" size="small" />
          <el-button type="primary" size="small" :loading="busy" @click="sgAdd">新建安全组</el-button>
        </div>
        <ArtTable :data="groups" :columns="groupCols" :height="fitH(groups.length)" :empty-height="'120px'" empty-text="暂无安全组" size="small" border :show-table-header="false" />
      </el-tab-pane>

      <!-- 实例设置 -->
      <el-tab-pane v-if="areas.includes('setting')" label="实例设置" name="setting">
        <div class="flex flex-col gap-4">
          <div class="flex flex-wrap items-center gap-2">
            <span class="w-20 text-sm text-g-500">挂载 ISO</span>
            <el-select v-model="isoVal" placeholder="选择 ISO" style="width: 220px" size="small">
              <el-option v-for="o in iso" :key="o.value" :value="o.value" :label="o.name || o.value || '无'" />
            </el-select>
            <el-button size="small" :loading="busy" :disabled="!iso.length" @click="isoMount">挂载</el-button>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <span class="w-20 text-sm text-g-500">启动顺序</span>
            <el-select v-model="bootVal" placeholder="选择启动顺序" style="width: 220px" size="small">
              <el-option v-for="o in boot" :key="o.value" :value="o.value" :label="o.name || o.value" />
            </el-select>
            <el-button size="small" :loading="busy" :disabled="!boot.length" @click="bootSet">保存</el-button>
          </div>
        </div>
      </el-tab-pane>

      <!-- 快照 / 备份 -->
      <el-tab-pane v-if="areas.includes('snapshot')" label="快照/备份" name="snapshot">
        <template v-if="snap">
          <p class="mb-2 text-xs text-g-500">
            快照 {{ snap.list.filter((x) => x.type === 'snap').length }}/{{ snap.snap_num }}
            · 备份 {{ snap.list.filter((x) => x.type === 'backup').length }}/{{ snap.backup_num }}
          </p>
          <div class="mb-3 flex flex-wrap items-center gap-2">
            <el-select v-model="snapDisk" placeholder="选择磁盘" style="width: 220px" size="small">
              <el-option v-for="d in snap.disk" :key="d.id" :value="d.id" :label="`${d.name || d.dev}（${d.size}GB）`" />
            </el-select>
            <el-input v-model="snapName" placeholder="名称/备注" style="width: 170px" size="small" />
            <el-button size="small" :loading="busy" @click="snapCreate('snap')">创建快照</el-button>
            <el-button size="small" :loading="busy" @click="snapCreate('backup')">创建备份</el-button>
          </div>
          <ArtTable :data="snap.list" :columns="snapCols" :height="fitH(snap.list.length)" :empty-height="'120px'" empty-text="暂无快照/备份" size="small" border :show-table-header="false" />
        </template>
      </el-tab-pane>
    </el-tabs>

    <!-- 安全组规则弹窗 -->
    <el-dialog v-model="rulesDialog" :title="`安全组规则 - ${groups.find((g) => g.id === rulesGid)?.name || ''}`" width="720px">
      <div class="mb-3 flex flex-wrap items-center gap-2">
        <el-select v-model="ruleForm.direction" style="width: 100px" size="small">
          <el-option value="in" label="入方向" />
          <el-option value="out" label="出方向" />
        </el-select>
        <el-select v-model="ruleForm.protocol" style="width: 120px" size="small">
          <el-option v-for="p in ['all', 'all_tcp', 'all_udp', 'tcp', 'udp', 'icmp', 'gre']" :key="p" :value="p" :label="p" />
        </el-select>
        <el-input v-model="ruleForm.port" placeholder="端口: 22 或 22-12345" style="width: 160px" size="small" />
        <el-input v-model="ruleForm.ip" placeholder="IP: 10.0.0.1/32" style="width: 150px" size="small" />
        <el-input v-model="ruleForm.description" placeholder="规则描述" style="width: 140px" size="small" />
        <el-button type="primary" size="small" :loading="busy" @click="ruleAdd">新增策略</el-button>
      </div>
      <ArtTable
        :loading="rulesLoading"
        :data="rules"
        :columns="ruleCols"
        :height="fitH(rules.length)"
        :empty-height="'160px'"
        empty-text="暂无规则"
        size="small"
        border
        :show-table-header="false"
      />
    </el-dialog>
  </div>
</template>

<style scoped>
.service-blocks-embedded {
  border: 0;
  border-radius: 0;
  box-shadow: none;
}
/* 嵌入模式：激活页由父组件驱动，隐藏 el-tabs 头部只留内容区 */
.service-blocks-embedded :deep(.el-tabs__header) {
  display: none;
}
</style>
