<script setup lang="ts">
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import type { ColumnOption } from '@/types'
import { balanceTypeLabel, adminActionLabel } from '../utils/admin-labels'
import {
  fetchAdminUser,
  fetchUserRealname,
  saveAdminUser,
  type AdminUserDetail,
  type AdminUserRealname,
} from '../admin/api'
import { useSession } from '../http/session'
import PhoneInput from '@/components/phone/PhoneInput.vue'

const route = useRoute()
const session = useSession()
const phoneInputRef = ref<InstanceType<typeof PhoneInput>>()
const adminBase = computed(() => (session.adminPath || '/admin').replace(/\/$/, ''))

const id = ref(Number(route.params.id))
const data = ref<AdminUserDetail | null>(null)
const loading = ref(false)
const saving = ref(false)
const realname = ref<AdminUserRealname | null>(null)
const realnameLoading = ref(false)

const form = reactive({
  email: '',
  phone: '',
  name: '',
  status: '1',
  new_password: '',
  email_verified: '0',
  phone_verified: '0',
})

async function load() {
  loading.value = true
  try {
    realname.value = null
    const d = await fetchAdminUser(id.value)
    data.value = d
    form.email = d.user.email || ''
    form.phone = d.user.phone || ''
    form.name = d.user.name || ''
    form.status = d.user.status === 1 ? '1' : '0'
    form.email_verified = d.user.email_verified ? '1' : '0'
    form.phone_verified = d.user.phone_verified ? '1' : '0'
    form.new_password = ''
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '读取用户失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)
watch(
  () => route.params.id,
  () => {
    id.value = Number(route.params.id)
    load()
  },
)

async function save() {
  if (!form.email && !form.phone) {
    ElMessage.warning('邮箱与手机号至少填写一个')
    return
  }
  if (form.phone) {
    const r = phoneInputRef.value?.check()
    if (!r?.ok) {
      ElMessage.warning(r?.msg || '手机号格式不正确')
      return
    }
  }
  if (form.new_password && form.new_password.length < 8) {
    ElMessage.warning('新密码至少 8 位')
    return
  }
  saving.value = true
  try {
    const body: Record<string, string> = {
      email: form.email,
      phone: form.phone,
      name: form.name,
      status: form.status,
    }
    if (form.new_password) body.new_password = form.new_password
    // 验证状态下拉只在对应渠道仍填有内容时提交，避免「解绑后把空渠道标记为已验证」。
    if (form.email) body.email_verified = form.email_verified
    if (form.phone) body.phone_verified = form.phone_verified
    await saveAdminUser(id.value, body)
    ElMessage.success('已保存')
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    saving.value = false
  }
}

// 证件照：按当前后台路径拼接（自定义路径下 /admin 被屏蔽）
function photoUrl(u?: string): string {
  if (!u) return ''
  return adminBase.value + u.replace(/^\/admin/, '')
}

// 主动查看实名资料：解密姓名/证件号，后端会记录 real_name_viewed。
async function viewRealname() {
  if (!id.value) return
  realnameLoading.value = true
  try {
    realname.value = await fetchUserRealname(id.value)
    if (!realname.value) ElMessage.info('该用户暂无实名申请')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '读取实名资料失败')
  } finally {
    realnameLoading.value = false
  }
}

const balanceLogs = computed(() => data.value?.balance_logs || [])
const balanceColumns: ColumnOption[] = [
  { prop: 'time', label: '时间', width: 150 },
  { prop: 'type', label: '类型', width: 100, formatter: (row) => balanceTypeLabel(row.type) },
  { prop: 'amount', label: '金额', width: 100 },
  { prop: 'note', label: '备注', minWidth: 140, formatter: (row) => row.note || '-' },
]

const adminLogs = computed(() => data.value?.admin_logs || [])
const adminLogColumns: ColumnOption[] = [
  { prop: 'created_at', label: '时间', width: 150 },
  { prop: 'admin_name', label: '管理员', width: 110, formatter: (row) => row.admin_name || '—' },
  { prop: 'action', label: '操作', width: 150, formatter: (row) => adminActionLabel(row.action) },
  { prop: 'detail', label: '详情', minWidth: 150, formatter: (row) => row.detail || '-' },
]
</script>

<template>
  <div class="art-full-height" v-loading="loading">
    <div v-if="data" class="grid gap-4 xl:grid-cols-3">
      <!-- 编辑表单 -->
      <div class="space-y-4 xl:col-span-2">
        <ElCard class="art-card">
          <template #header>
            <div class="art-card-header">
              <div class="title"><h4>账户信息</h4><p>邮箱、手机号、状态与密码。</p></div>
            </div>
          </template>
          <el-form label-position="top">
            <div class="grid gap-4 sm:grid-cols-2">
              <el-form-item label="邮箱（登录名）">
                <el-input v-model="form.email" placeholder="请输入邮箱" />
              </el-form-item>
              <el-form-item :label="`手机号（当前：${data.user.phone_masked || '未绑定'}）`">
                <PhoneInput ref="phoneInputRef" v-model="form.phone" placeholder="留空表示解绑" />
              </el-form-item>
              <el-form-item label="显示名称">
                <el-input v-model="form.name" placeholder="选填" />
              </el-form-item>
              <el-form-item label="账号状态">
                <el-select v-model="form.status" class="w-full">
                  <el-option label="正常" value="1" />
                  <el-option label="禁用" value="0" />
                </el-select>
              </el-form-item>
              <el-form-item label="邮箱验证状态">
                <el-select
                  v-model="form.email_verified"
                  class="w-full"
                  :disabled="!form.email"
                >
                  <el-option label="已验证" value="1" />
                  <el-option label="未验证" value="0" />
                </el-select>
              </el-form-item>
              <el-form-item label="手机号验证状态">
                <el-select
                  v-model="form.phone_verified"
                  class="w-full"
                  :disabled="!form.phone"
                >
                  <el-option label="已验证" value="1" />
                  <el-option label="未验证" value="0" />
                </el-select>
              </el-form-item>
              <el-form-item label="重置密码（留空不修改）">
                <el-input v-model="form.new_password" type="password" show-password placeholder="至少 8 位" />
              </el-form-item>
            </div>
            <el-button type="primary" :loading="saving" @click="save">保存</el-button>
            <p class="mt-2 text-xs text-warning">
              修改邮箱/手机号/密码或禁用账号后，该用户的登录会话会被吊销。
            </p>
          </el-form>
        </ElCard>

        <!-- 实名认证 -->
        <ElCard class="art-card">
          <template #header>
            <div class="art-card-header">
              <div class="title"><h4>实名认证</h4><p>身份资料与证件照片（查看会记入操作日志）。</p></div>
              <el-button
                v-if="data.realname.has_submission && !realname"
                type="primary"
                :loading="realnameLoading"
                @click="viewRealname"
              >
                查看实名资料
              </el-button>
            </div>
          </template>
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="状态">{{ data.realname.status }}</el-descriptions-item>
            <el-descriptions-item label="提交时间">{{ data.realname.submitted_at || '-' }}</el-descriptions-item>
            <el-descriptions-item label="审核时间">{{ data.realname.reviewed_at || '-' }}</el-descriptions-item>
            <el-descriptions-item label="姓名">{{ realname?.name || '—' }}</el-descriptions-item>
            <el-descriptions-item label="证件号">{{ realname?.number || '—' }}</el-descriptions-item>
          </el-descriptions>
          <div v-if="realname?.front_url" class="mt-3 flex flex-wrap gap-4">
            <div>
              <p class="mb-1 text-xs text-g-500">身份证正面</p>
              <el-image :src="photoUrl(realname.front_url)" :preview-src-list="[photoUrl(realname.front_url)]" fit="cover" class="h-36 w-56 rounded-lg border" />
            </div>
            <div v-if="realname.back_url">
              <p class="mb-1 text-xs text-g-500">身份证背面</p>
              <el-image :src="photoUrl(realname.back_url)" :preview-src-list="[photoUrl(realname.back_url)]" fit="cover" class="h-36 w-56 rounded-lg border" />
            </div>
          </div>
          <p v-else-if="data.realname.has_submission" class="mt-2 text-xs text-g-500">
            点击右上角「查看实名资料」以解密查看姓名、证件号与证件照片。
          </p>
          <p v-else class="mt-2 text-xs text-g-500">该用户尚未提交实名认证。</p>
        </ElCard>
      </div>

      <!-- 只读概览 -->
      <div class="space-y-4">
        <ElCard class="art-card">
          <template #header>
            <div class="art-card-header">
              <div class="title"><h4>账户概览</h4></div>
            </div>
          </template>
          <el-descriptions :column="1" size="small">
            <el-descriptions-item label="余额">￥{{ data.user.balance }}</el-descriptions-item>
            <el-descriptions-item label="邮箱状态">{{ data.user.email_status }}</el-descriptions-item>
            <el-descriptions-item label="手机状态">{{ data.user.phone_status }}</el-descriptions-item>
            <el-descriptions-item label="注册时间">{{ data.user.registered_at }}</el-descriptions-item>
            <el-descriptions-item label="最近登录">{{ data.user.last_login_at }}</el-descriptions-item>
          </el-descriptions>
        </ElCard>

        <ElCard class="art-card">
          <template #header>
            <div class="art-card-header">
              <div class="title"><h4>服务与财务统计</h4></div>
            </div>
          </template>
          <div class="grid grid-cols-2 gap-3 text-sm">
            <div><div class="text-xs text-g-500">服务总数</div><div class="text-lg font-bold">{{ data.stats.service_count }}</div></div>
            <div><div class="text-xs text-g-500">激活中</div><div class="text-lg font-bold text-success">{{ data.stats.active_count }}</div></div>
            <div><div class="text-xs text-g-500">待支付账单</div><div class="text-lg font-bold" :class="data.stats.unpaid_count ? 'text-warning' : ''">{{ data.stats.unpaid_count }}</div></div>
            <div><div class="text-xs text-g-500">累计消费</div><div class="text-lg font-bold">￥{{ data.stats.paid_total }}</div></div>
          </div>
        </ElCard>
      </div>
    </div>

    <!-- 流水与日志 -->
    <div v-if="data" class="mt-4 grid gap-4 xl:grid-cols-2">
      <ElCard class="art-card">
        <template #header>
          <div class="art-card-header">
            <div class="title"><h4>余额流水</h4></div>
          </div>
        </template>
        <ArtTable
          :data="balanceLogs"
          :columns="balanceColumns"
          :height="320"
          :show-table-header="false"
          empty-text="暂无流水"
        />
      </ElCard>

      <ElCard class="art-card">
        <template #header>
          <div class="art-card-header">
            <div class="title"><h4>管理员操作记录</h4></div>
          </div>
        </template>
        <ArtTable
          :data="adminLogs"
          :columns="adminLogColumns"
          :height="320"
          :show-table-header="false"
          empty-text="暂无记录"
        />
      </ElCard>
    </div>

    <el-empty v-else-if="!loading" description="用户不存在" />
  </div>
</template>

<style scoped></style>
