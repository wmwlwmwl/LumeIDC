<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ArrowLeft } from '@element-plus/icons-vue'
import {
  fetchServiceDetail,
  refreshServiceHost,
  renewService,
  renameService,
  fetchServiceInvoices,
  fetchCancelRequestInfo,
  submitCancelRequest,
  withdrawCancelRequest,
  type DetailData,
  type SvcConfig,
  type ServiceInvoice,
  type CancelRequestInfo,
} from '../api/user'
import { formatDate, formatMoney } from '@/utils/format'
import ZjmfServicePanel from '../components/providers/ZjmfServicePanel.vue'
import EasyPanelServicePanel from '../components/providers/EasyPanelServicePanel.vue'
import ServiceInvoices from '../components/ServiceInvoices.vue'

// Provider → 面板组件注册表（新增 provider 只需在此添加）
const SERVICE_PANELS: Record<string, unknown> = {
  zjmf: ZjmfServicePanel,
  easypanel: EasyPanelServicePanel,
}
const panelComponent = computed(() => data.value?.svc.provider ? SERVICE_PANELS[data.value.svc.provider] : null)

const route = useRoute()
const router = useRouter()
const data = ref<DetailData | null>(null)
const loading = ref(false)
const loadError = ref(false)
const saving = ref(false)
const serviceInvoices = ref<ServiceInvoice[]>([])
const invoicesLoading = ref(false)

// 停用申请
const cancelDialog = ref(false)
const cancelBusy = ref(false)
const cancelReasons = ref<string[]>(['产品稳定性不足', '业务减少不需要', '其他'])
const cancelRequest = ref<CancelRequestInfo | null>(null)
const cancelType = ref('Immediate')
const cancelReason = ref('产品稳定性不足')
const cancelDetail = ref('')

// 编辑名称/备注
const editDialog = ref(false)
const name = ref('')
const remark = ref('')

// 是否已为该服务自动拉取过上游实例信息（避免重复请求/循环）。
let autoRefreshed = false

// 后台代管视图（路由 meta.admin）：由后台「服务实例 → 管理」进入。
// 后台没有支付/实名等前台页面，续费、付款需改道，避免把管理员带到前台登录页。
const isAdminView = computed(() => route.meta.admin === true)

// 本次续费正在人工处理中（上游涨价/欠费等待充值）：钱已收、到期时间已延长，
// 处理完成前不允许再次续费，否则会重复付费并重复往上游续期。
const renewing = computed(() => data.value?.svc?.transition === 'renew_pending')

// 后台代管下付款入口：账单本身在「订单管理」里处理。
function onPayInvoice(id: number) {
  if (isAdminView.value) {
    router.push('/orders')
    return
  }
  router.push(`/pay/${id}`)
}

// 服务切换竞态防护：请求发起时快照路由 id，响应返回时路由已变则丢弃本次结果
function svcStale(reqId: string): boolean {
  return String(route.params.id || '') !== reqId
}

async function load() {
  const reqId = String(route.params.id || '')
  loading.value = true
  loadError.value = false
  try {
    const detail = await fetchServiceDetail(reqId)
    if (svcStale(reqId)) return
    data.value = detail
    name.value = data.value.svc.name
    remark.value = data.value.svc.remark
    void loadServiceInvoices()
    void loadCancelRequest()
    // 首次打开且本地无快照时，自动向后端拉取一次实例信息（非阻塞）。
    if (
      !autoRefreshed &&
      !data.value.host &&
      (data.value.svc.provider === 'zjmf' || data.value.svc.provider === 'easypanel')
    ) {
      autoRefreshed = true
      void autoRefreshHost()
    }
  } catch (err: unknown) {
    if (svcStale(reqId)) return
    data.value = null
    loadError.value = true
    ElMessage.error((err as Error).message || '读取详情失败')
  } finally {
    if (!svcStale(reqId)) loading.value = false
  }
}

// 自动拉取上游实例信息；失败时静默，保留卡片内的手动「刷新信息」入口。
async function autoRefreshHost() {
  const reqId = String(route.params.id || '')
  try {
    const res = await refreshServiceHost(Number(reqId))
    if (svcStale(reqId)) return
    if (String(res.ok) === '1') await load()
  } catch {
    /* 上游不可达：保持空态 */
  }
}

onMounted(load)
// 同一路由切换到别的服务时组件复用，需监听参数重新加载
watch(
  () => route.params.id,
  () => {
    autoRefreshed = false
    load()
  },
)

async function loadServiceInvoices() {
  const reqId = String(route.params.id || '')
  if (!reqId) return
  invoicesLoading.value = true
  try {
    const list = await fetchServiceInvoices(reqId)
    if (svcStale(reqId)) return
    serviceInvoices.value = list
  } catch {
    if (svcStale(reqId)) return
    serviceInvoices.value = []
  } finally {
    if (!svcStale(reqId)) invoicesLoading.value = false
  }
}

const renewDialog = ref(false)
const renewCycle = ref('monthly')
const renewBusy = ref(false)

function openRenew() {
  renewCycle.value = data.value?.default_cycle || 'monthly'
  renewDialog.value = true
}

async function doRenew() {
  if (!data.value) return
  renewBusy.value = true
  try {
    const result = await renewService(data.value.svc.id, renewCycle.value)
    if (result.redirect) {
      // 后台代管：无前台支付/实名页，改为提示后原地刷新（订单已生成，去「订单管理」处理）
      if (isAdminView.value) {
        renewDialog.value = false
        if (String(result.ok) === '1') {
          ElMessage.success(result.paid ? '续费成功' : '续费订单已生成，请在「订单管理」中处理')
          await load()
        } else {
          ElMessage.error(result.msg || '续费失败')
        }
        return
      }
      location.href = result.redirect
      return
    }
    // HTTP 200 也可能业务失败（ok=0），失败时保留弹窗让用户重试
    if (String(result.ok) !== '1') {
      ElMessage.error(result.msg || '续费失败')
      return
    }
    renewDialog.value = false
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '续费失败')
  } finally {
    renewBusy.value = false
  }
}

async function saveRename() {
  if (!data.value || !name.value) return
  saving.value = true
  try {
    await renameService(data.value.svc.id, name.value, remark.value)
    editDialog.value = false
    ElMessage.success('已保存')
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function loadCancelRequest() {
  const reqId = String(route.params.id || '')
  if (!reqId) return
  try {
    const res = await fetchCancelRequestInfo(reqId)
    if (svcStale(reqId)) return
    cancelRequest.value = res.request
    if (res.reasons?.length) {
      cancelReasons.value = res.reasons
      if (!res.reasons.includes(cancelReason.value)) cancelReason.value = res.reasons[0]
    }
  } catch {
    if (svcStale(reqId)) return
    cancelRequest.value = null
  }
}

function openCancel() {
  cancelType.value = 'Immediate'
  cancelReason.value = cancelReasons.value[0] || '其他'
  cancelDetail.value = ''
  cancelDialog.value = true
}

async function submitCancel() {
  if (!data.value) return
  cancelBusy.value = true
  try {
    await submitCancelRequest(data.value.svc.id, {
      type: cancelType.value,
      reason: cancelReason.value,
      reason_detail: cancelDetail.value.trim(),
    })
    cancelDialog.value = false
    ElMessage.success('停用申请已提交，请等待处理')
    await loadCancelRequest()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '提交失败')
  } finally {
    cancelBusy.value = false
  }
}

async function withdrawCancel() {
  if (!data.value) return
  cancelBusy.value = true
  try {
    await withdrawCancelRequest(data.value.svc.id)
    ElMessage.success('已撤回停用申请')
    await loadCancelRequest()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '撤回失败')
  } finally {
    cancelBusy.value = false
  }
}

function priceText(c: SvcConfig): string {
  return c.price > 0 ? `+￥${formatMoney(c.price)}/月` : ''
}

// 后台手填的配置按行展示：换行、中文间隔号「·」都当作分隔符
const configLines = computed(() =>
  (data.value?.svc.config_desc || '')
    .split(/[\n·]+/)
    .map((s) => s.trim())
    .filter(Boolean),
)

function openUpgrade() {
  if (data.value) router.push(`/services/${data.value.svc.id}/upgrade`)
}

</script>
<template>
  <div v-loading="loading">
    <template v-if="data">
      <section class="art-card sd-hero" :class="`is-s${data.svc.status}`">
        <div class="sd-hero__main">
          <RouterLink to="/services" class="sd-hero__back" aria-label="返回我的服务" title="返回我的服务">
            <el-icon><ArrowLeft /></el-icon>
          </RouterLink>
          <div class="sd-hero__title">
            <div class="sd-hero__name-row">
              <h1>{{ data.svc.name }}</h1>
              <el-tag
                :type="renewing ? 'warning' : data.svc.status === 1 ? 'success' : data.svc.status === 2 ? 'warning' : 'info'"
                size="small"
                effect="light"
              >{{ renewing ? '续费处理中' : data.svc.status_text }}</el-tag>
            </div>
            <p v-if="data.svc.remark" class="sd-hero__remark">备注：{{ data.svc.remark }}</p>
            <p v-if="renewing" class="sd-hero__remark">
              本次续费正在人工处理中（上游价格变动或上游账户余额不足），处理完成前无法再次续费；到期时间已延长，不影响使用。
            </p>
          </div>
        </div>
        <div class="sd-hero__actions">
          <el-button size="small" @click="editDialog = true">编辑名称/备注</el-button>
          <el-button
            size="small"
            type="primary"
            plain
            :disabled="renewing"
            :title="renewing ? '本次续费正在人工处理中，处理完成前无法再次续费' : ''"
            @click="openRenew"
          >续费</el-button>
          <el-button v-if="data.can_upgrade" size="small" type="primary" plain @click="openUpgrade">升降级</el-button>
          <template v-if="cancelRequest">
            <el-tag size="small" type="warning" effect="light">停用申请审核中</el-tag>
            <el-button size="small" text type="danger" :loading="cancelBusy" @click="withdrawCancel">撤回申请</el-button>
          </template>
          <el-button
            v-else-if="data.svc.status === 1 || data.svc.status === 2"
            size="small"
            type="warning"
            plain
            @click="openCancel"
          >
            申请停用
          </el-button>
        </div>
      </section>

      <component :is="panelComponent" v-if="panelComponent" :data="data" @refresh="load">
        <template #invoices><ServiceInvoices class="art-card" :loading="invoicesLoading" :invoices="serviceInvoices" @pay="onPayInvoice" /></template>
      </component>

      <div class="sd-grid sd-grid--footer">
        <section class="art-card sd-panel sd-panel--pay">
          <div class="sd-panel__head"><div><h2>付费信息</h2><p>账单周期与实例基础属性</p></div></div>
          <div class="sd-pay-list">
            <div><span>计费方式</span><b>{{ data.svc.cycle || '-' }}</b></div>
            <div><span>续费价格</span><b class="has-strong">￥{{ data.svc.amount ? formatMoney(data.svc.amount) : '-' }}</b></div>
            <div><span>到期时间</span><b>{{ data.svc.expires_at ? formatDate(data.svc.expires_at) : '-' }}</b></div>
            <div><span>主机名</span><b>{{ data.svc.hostname || '-' }}</b></div>
          </div>
        </section>

        <section class="art-card sd-panel sd-panel--config">
          <div class="sd-panel__head"><div><h2>配置信息</h2><p>当前实例配置明细</p></div></div>
          <div v-if="configLines.length" class="sd-config-lines">
            <div v-for="(l, i) in configLines" :key="i">{{ l }}</div>
          </div>
          <div v-else-if="data.svc.configs?.length" class="sd-config-list">
            <div v-for="c in data.svc.configs" :key="c.name"><span>{{ c.name }}</span><b>{{ c.value }}<small> {{ priceText(c) }}</small></b></div>
          </div>
          <p v-else class="sd-config-empty">-</p>
        </section>
      </div>

      <el-dialog v-model="editDialog" title="编辑名称 / 备注" width="420px">
        <el-form label-position="top">
          <el-form-item label="服务名称">
            <el-input v-model="name" maxlength="100" />
          </el-form-item>
          <el-form-item label="备注（可选）">
            <el-input v-model="remark" maxlength="255" />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="editDialog = false">取消</el-button>
          <el-button type="primary" :loading="saving" @click="saveRename">保存</el-button>
        </template>
      </el-dialog>

      <el-dialog v-model="renewDialog" title="续费" width="400px">
        <el-form label-position="top">
          <el-form-item label="续费周期">
            <el-select v-model="renewCycle" class="w-full">
              <el-option
                v-if="data.show_monthly"
                value="monthly"
                :label="`按月付 · ￥${formatMoney(data.renew_prices?.monthly || data.svc.amount || 0)}`"
              />
              <el-option
                v-if="data.show_q"
                value="quarterly"
                :label="`按季付 · ￥${formatMoney(data.renew_prices?.quarterly || 0)}`"
              />
              <el-option
                v-if="data.show_y"
                value="yearly"
                :label="`按年付 · ￥${formatMoney(data.renew_prices?.yearly || 0)}`"
              />
            </el-select>
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="renewDialog = false">取消</el-button>
          <el-button type="primary" :loading="renewBusy" @click="doRenew">续费</el-button>
        </template>
      </el-dialog>

      <el-dialog v-model="cancelDialog" title="申请停用" width="460px">
        <p class="sd-cancel-hint">提交后服务实例保持不变，由管理员审核处理；处理结果将通过站内信通知你。</p>
        <el-form label-position="top">
          <el-form-item label="停用时间">
            <el-select v-model="cancelType" class="w-full">
              <el-option value="Immediate" label="立即" />
              <el-option value="Endofbilling" label="等待账单周期结束" />
            </el-select>
          </el-form-item>
          <el-form-item label="停用原因">
            <el-select v-model="cancelReason" class="w-full">
              <el-option v-for="r in cancelReasons" :key="r" :value="r" :label="r" />
            </el-select>
          </el-form-item>
          <el-form-item label="详细说明（可选）">
            <el-input
              v-model="cancelDetail"
              type="textarea"
              :rows="3"
              maxlength="500"
              show-word-limit
              placeholder="可补充说明，帮助管理员判断"
            />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="cancelDialog = false">取消</el-button>
          <el-button type="warning" :loading="cancelBusy" @click="submitCancel">提交申请</el-button>
        </template>
      </el-dialog>
    </template>

    <div v-else-if="loadError" class="sd-state" role="alert">
      <p>服务详情加载失败，请稍后重试。</p>
      <el-button size="small" @click="load">重新加载</el-button>
    </div>

    <el-empty v-else-if="!loading" description="服务不存在" />
  </div>
</template>

<style scoped>
.sd-hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  padding: 18px 20px;
}

.sd-hero__main {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.sd-hero__back {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  color: var(--art-gray-500);
  background: var(--art-gray-50);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-md);
  transition: color 0.16s ease, background 0.16s ease, border-color 0.16s ease;
}

.sd-hero__back:hover {
  color: var(--theme-color);
  background: var(--theme-color-soft);
  border-color: color-mix(in srgb, var(--theme-color) 40%, var(--art-card-border));
}

.sd-hero__title {
  min-width: 0;
}

.sd-hero__name-row {
  display: flex;
  align-items: center;
  gap: 9px;
  min-width: 0;
}

.sd-hero__name-row h1 {
  margin: 0;
  overflow: hidden;
  color: var(--art-gray-900);
  font-size: 18px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sd-hero__remark {
  margin: 5px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
}

.sd-hero__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 7px;
}

.sd-grid {
  margin-top: 16px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.35fr);
  gap: 16px;
  align-items: stretch;
}

.sd-panel {
  padding: 19px;
}

.sd-panel__head {
  margin-bottom: 14px;
  display: flex;
  align-items: center;
  gap: 10px;
}

.sd-panel__head h2 {
  margin: 0;
  color: var(--art-gray-800);
  font-size: 14px;
}

.sd-panel__head p {
  margin: 3px 0 0;
  color: var(--art-gray-500);
  font-size: 11px;
}

.sd-pay-list > div,
.sd-config-list > div {
  padding: 9px 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  font-size: 12px;
  border-bottom: 1px dashed var(--art-card-border);
}

.sd-pay-list > div:last-child,
.sd-config-list > div:last-child {
  border-bottom: 0;
}

.sd-pay-list span,
.sd-config-list span {
  color: var(--art-gray-500);
}

.sd-pay-list b,
.sd-config-list b {
  color: var(--art-gray-800);
  font-weight: 600;
  text-align: right;
}

.sd-pay-list b small,
.sd-config-list b small {
  color: var(--art-gray-500);
  font-weight: 400;
}

.sd-config-lines > div {
  padding: 9px 0;
  color: var(--art-gray-800);
  font-size: 12px;
  line-height: 1.6;
  word-break: break-word;
  border-bottom: 1px dashed var(--art-card-border);
}

.sd-config-lines > div:last-child {
  border-bottom: 0;
}

.sd-config-empty {
  margin: 6px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
}

.sd-cancel-hint {
  margin: 0 0 14px;
  color: var(--art-gray-500);
  font-size: 12px;
  line-height: 1.6;
}

.sd-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 56px 20px;
  text-align: center;
}

.sd-state p {
  margin: 0;
  color: var(--art-gray-600);
  font-size: 14px;
}

@media (max-width: 960px) {
  .sd-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 620px) {
  .sd-hero {
    flex-direction: column;
    /* 竖排后必须撑满宽度：否则子项按 fit-content 定宽，长实例名（nowrap 标题）会把卡片撑破 */
    align-items: stretch;
  }

  .sd-hero__actions {
    justify-content: flex-start;
  }

  /* 窄屏没有横向空间做单行省略，改为换行完整显示实例名 */
  .sd-hero__name-row {
    align-items: flex-start;
  }

  .sd-hero__name-row h1 {
    overflow: visible;
    white-space: normal;
  }
}
</style>
