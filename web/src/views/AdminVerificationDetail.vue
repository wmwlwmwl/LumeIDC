<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { fetchAdminVerification, reviewVerification, type AdminVerificationDetail } from '../admin/api'
import { useSession } from '../http/session'

const session = useSession()
const editBase = session.adminPath || '/admin'
const route = useRoute()
const router = useRouter()
const id = ref(Number(route.params.id))
const data = ref<AdminVerificationDetail | null>(null)
const loading = ref(false)
const busy = ref(false)

async function load() {
  loading.value = true
  try {
    data.value = await fetchAdminVerification(id.value)
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '读取失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)
// 同一路由切换审核单时组件复用，需监听参数重新加载
watch(
  () => route.params.id,
  () => {
    id.value = Number(route.params.id)
    load()
  },
)

function photoUrl(p: string): string {
  if (!p) return ''
  return editBase + p.replace(/^\/admin/, '')
}

async function review(approve: boolean) {
  let reason = ''
  if (!approve) {
    const res = await ElMessageBox.prompt('请输入驳回原因', '驳回实名申请', {
      confirmButtonText: '确认驳回',
      cancelButtonText: '取消',
      inputValidator: (v: string) => (v && v.trim() ? true : '驳回原因不能为空'),
    }).catch(() => null)
    if (!res) return
    reason = String(res.value).trim()
  } else {
    const res = await ElMessageBox.confirm('确认通过该实名申请？', '通过审核', {
      type: 'success',
      confirmButtonText: '通过',
      cancelButtonText: '取消',
    }).catch(() => null)
    if (!res) return
  }
  busy.value = true
  try {
    await reviewVerification(id.value, approve, reason)
    ElMessage.success('审核结果已保存')
    router.push('/verifications')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '操作失败')
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <div class="art-full-height" v-loading="loading">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>审核 #{{ id }}</h4>
            <p>核对申请资料与证件照片后给出审核结论。</p>
          </div>
          <el-tag
            v-if="data"
            :type="data.raw_status === 'pending' ? 'warning' : data.raw_status === 'approved' ? 'success' : 'danger'"
            effect="light"
          >{{ data.status }}</el-tag>
        </div>
      </template>

      <ElRow v-if="data" :gutter="20">
        <ElCol :xs="24" :md="12">
          <el-descriptions :column="1" border>
            <el-descriptions-item label="邮箱">{{ data.email }}</el-descriptions-item>
            <el-descriptions-item label="手机号">{{ data.phone }}</el-descriptions-item>
            <el-descriptions-item label="姓名">{{ data.name }}</el-descriptions-item>
            <el-descriptions-item label="身份证号">{{ data.identity_number }}</el-descriptions-item>
            <el-descriptions-item label="状态">{{ data.status }}</el-descriptions-item>
            <el-descriptions-item label="提交时间">{{ data.submitted_at }}</el-descriptions-item>
            <el-descriptions-item v-if="data.reason" label="驳回原因">{{ data.reason }}</el-descriptions-item>
          </el-descriptions>
          <div v-if="data.raw_status === 'pending'" class="mt-4 flex gap-2.5">
            <el-button type="success" :loading="busy" @click="review(true)">通过</el-button>
            <el-button type="danger" plain :loading="busy" @click="review(false)">驳回</el-button>
          </div>
        </ElCol>

        <ElCol :xs="24" :md="12">
          <div class="admin-photo">
            <p>身份证正面</p>
            <el-image
              v-if="photoUrl(data.front_url)"
              :src="photoUrl(data.front_url)"
              :preview-src-list="[photoUrl(data.front_url)]"
              fit="contain"
              class="admin-photo__img"
            />
            <span v-else class="admin-photo__none">未上传</span>
          </div>
          <div class="admin-photo">
            <p>身份证背面</p>
            <el-image
              v-if="photoUrl(data.back_url)"
              :src="photoUrl(data.back_url)"
              :preview-src-list="[photoUrl(data.back_url)]"
              fit="contain"
              class="admin-photo__img"
            />
            <span v-else class="admin-photo__none">未上传</span>
          </div>
        </ElCol>
      </ElRow>
      <el-empty v-else-if="!loading" description="申请不存在" />
    </ElCard>
  </div>
</template>

<style scoped>
.admin-photo {
  margin-bottom: 15px;
}
.admin-photo p {
  margin: 0 0 7px;
  color: var(--art-gray-500);
  font-size: 12px;
}
.admin-photo__img {
  max-height: 290px;
  border: 1px solid var(--art-card-border);
  border-radius: 10px;
}
.admin-photo__none {
  padding: 24px;
  display: block;
  color: var(--art-gray-400);
  font-size: 12px;
  text-align: center;
  background: var(--art-gray-100);
  border-radius: 10px;
}
</style>
