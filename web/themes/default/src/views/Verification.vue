<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import type { UploadFile } from 'element-plus'
import { ElMessage } from 'element-plus'
import { Postcard, CircleCheck } from '@element-plus/icons-vue'
import {
  fetchVerification,
  startVerificationPlugin,
  pollVerificationPlugin,
  type VerificationData,
} from '@/api/user'
import { http } from '@/http/index'
import PublicPageHead from '@/components/public/PublicPageHead.vue'

const data = ref<VerificationData | null>(null)
const loading = ref(false)
const legalName = ref('')
const idNumber = ref('')
const front = ref<File>()
const back = ref<File>()
const frontUrl = ref('')
const backUrl = ref('')
const submitting = ref(false)

// 自动实名（插件）流程
const pluginName = ref('')
const pluginIdNumber = ref('')
const pluginStarting = ref(false)
const pluginPolling = ref(false)

const autoTagType = computed(() => {
  const s = data.value?.automatic_status
  if (s === 'approved') return 'success'
  if (s === 'pending') return 'warning'
  if (s === 'rejected' || s === 'failed' || s === 'expired') return 'danger'
  return 'info'
})

async function startPlugin() {
  if (!data.value?.plugin_provider) return
  if (!pluginName.value || !pluginIdNumber.value) {
    ElMessage.warning('请填写姓名与身份证号')
    return
  }
  if (!/^\d{17}[\dXx]$/.test(pluginIdNumber.value.trim())) {
    ElMessage.warning('请输入正确的 18 位身份证号')
    return
  }
  pluginStarting.value = true
  try {
    const res = await startVerificationPlugin({
      provider: data.value.plugin_provider,
      legal_name: pluginName.value,
      identity_number: pluginIdNumber.value,
    })
    if (res.url) window.open(res.url, '_blank', 'noopener')
    ElMessage.success('已开始认证，请在认证页面完成核验')
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '启动失败')
  } finally {
    pluginStarting.value = false
  }
}

async function pollPlugin() {
  if (!data.value?.automatic_id) {
    await load()
    return
  }
  pluginPolling.value = true
  try {
    const res = await pollVerificationPlugin(data.value.automatic_id)
    ElMessage.success(res.message || '已查询认证结果')
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '查询失败')
  } finally {
    pluginPolling.value = false
  }
}

async function load() {
  loading.value = true
  try {
    data.value = await fetchVerification()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '读取实名状态失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

// 预览用 objectURL 需手动回收，避免内存泄漏
function setPreview(which: 'front' | 'back', raw: File) {
  const url = URL.createObjectURL(raw)
  if (which === 'front') {
    if (frontUrl.value) URL.revokeObjectURL(frontUrl.value)
    front.value = raw
    frontUrl.value = url
  } else {
    if (backUrl.value) URL.revokeObjectURL(backUrl.value)
    back.value = raw
    backUrl.value = url
  }
}

function onFile(f: UploadFile, which: 'front' | 'back') {
  if (!f.raw) return
  const raw = f.raw
  if (!raw.type.startsWith('image/')) {
    ElMessage.warning('请上传图片文件（JPG / PNG）')
    return
  }
  if (raw.size > 5 * 1024 * 1024) {
    ElMessage.warning('图片不能超过 5MB')
    return
  }
  setPreview(which, raw)
}

function triggerUpload(e: KeyboardEvent) {
  const target = e.currentTarget as HTMLElement | null
  target?.closest('.el-upload')?.querySelector<HTMLInputElement>('input[type="file"]')?.click()
}

onBeforeUnmount(() => {
  if (frontUrl.value) URL.revokeObjectURL(frontUrl.value)
  if (backUrl.value) URL.revokeObjectURL(backUrl.value)
})

async function submit() {
  if (!legalName.value || !idNumber.value || !front.value || !back.value) {
    ElMessage.warning('请填写姓名、身份证号并上传正反面照片')
    return
  }
  if (!/^\d{17}[\dXx]$/.test(idNumber.value.trim())) {
    ElMessage.warning('请输入正确的 18 位身份证号')
    return
  }
  submitting.value = true
  try {
    const fd = new FormData()
    fd.append('legal_name', legalName.value)
    fd.append('identity_number', idNumber.value)
    fd.append('front', front.value)
    fd.append('back', back.value)
    const res = (await http.post('/user/verification', fd)) as { ok: number; msg?: string }
    if (String(res.ok) === '1') {
      ElMessage.success('实名资料已提交，等待人工审核')
      await load()
    } else {
      ElMessage.error(res.msg || '提交失败')
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '提交失败')
  } finally {
    submitting.value = false
  }
}

</script>

<template>
  <div>
    <PublicPageHead title="实名认证" subtitle="完成实名后即可购买需实名的产品。" />

    <div v-loading="loading" class="verify-stack">
      <div v-if="data">
        <div v-if="data.status && data.status_text" class="art-card verify-status">
          <span class="verify-status__icon"><el-icon><Postcard /></el-icon></span>
          <div class="verify-status__copy">
            <small>当前认证状态</small>
            <strong :class="`is-${data.status}`">{{ data.status_text }}</strong>
          </div>
          <p v-if="data.masked_id">证件号：{{ data.masked_id }}</p>
          <p v-if="data.reason" class="is-reason">驳回原因：{{ data.reason }}</p>
        </div>

        <div v-if="data.can_submit && data.manual_enabled" class="art-card verify-card">
          <div class="verify-card__title"><span><el-icon><CircleCheck /></el-icon></span><div><h2>提交实名资料</h2><p>完成认证后即可使用需要实名的产品。</p></div></div>
          <p class="verify-card__hint">仅用于购买需实名的产品，资料将由平台人工审核。</p>
          <el-form label-position="top">
            <el-form-item label="真实姓名">
              <el-input v-model="legalName" placeholder="与身份证一致" />
            </el-form-item>
            <el-form-item label="身份证号">
              <el-input v-model="idNumber" placeholder="请输入 18 位身份证号" />
            </el-form-item>
            <div class="verify-uploads">
              <el-form-item label="身份证正面（人像面）">
                <el-upload
                  class="w-full"
                  accept="image/*"
                  :auto-upload="false"
                  :show-file-list="false"
                  :on-change="(f: UploadFile) => onFile(f, 'front')"
                >
                  <div
                    class="verify-upload"
                    :class="{ 'has-file': front }"
                    role="button"
                    tabindex="0"
                    :aria-label="front ? '身份证正面已选择，点击更换' : '上传身份证正面'"
                    @keydown.enter.prevent="triggerUpload"
                    @keydown.space.prevent="triggerUpload"
                  >
                    <img v-if="frontUrl" :src="frontUrl" alt="正面预览" class="verify-upload__img" />
                    <template v-else>
                      <span class="verify-upload__plus">＋</span>
                      <span class="verify-upload__hint">点击上传正面</span>
                    </template>
                  </div>
                </el-upload>
              </el-form-item>
              <el-form-item label="身份证背面（国徽面）">
                <el-upload
                  class="w-full"
                  accept="image/*"
                  :auto-upload="false"
                  :show-file-list="false"
                  :on-change="(f: UploadFile) => onFile(f, 'back')"
                >
                  <div
                    class="verify-upload"
                    :class="{ 'has-file': back }"
                    role="button"
                    tabindex="0"
                    :aria-label="back ? '身份证背面已选择，点击更换' : '上传身份证背面'"
                    @keydown.enter.prevent="triggerUpload"
                    @keydown.space.prevent="triggerUpload"
                  >
                    <img v-if="backUrl" :src="backUrl" alt="背面预览" class="verify-upload__img" />
                    <template v-else>
                      <span class="verify-upload__plus">＋</span>
                      <span class="verify-upload__hint">点击上传背面</span>
                    </template>
                  </div>
                </el-upload>
              </el-form-item>
            </div>
            <el-button type="primary" class="verify-submit" :loading="submitting" @click="submit">提交审核</el-button>
          </el-form>
        </div>

        <p v-else-if="!data.can_submit" class="art-card verify-idle">已提交或正在审核，无需重复提交。</p>
        <p v-else class="art-card verify-idle">
          当前站点未开启人工提交，请使用下方自动实名方式或联系客服协助完成认证。
        </p>

        <div v-if="data.plugin_provider" class="art-card verify-card">
          <div class="verify-card__title">
            <span><el-icon><Postcard /></el-icon></span>
            <div>
              <h2>自动实名认证</h2>
              <p>由认证服务商实时核验，通过后立即生效，不进入人工审核队列。</p>
            </div>
          </div>
          <div class="verify-plugin-row">
            <el-tag type="info" effect="plain">{{ data.plugin_provider }}</el-tag>
            <el-tag v-if="data.automatic_status_text" :type="autoTagType">
              {{ data.automatic_status_text }}
            </el-tag>
          </div>

          <template v-if="data.automatic_status === 'pending'">
            <p class="verify-card__hint">
              认证处理中<template v-if="data.automatic_url"
                >，可<a :href="data.automatic_url" target="_blank" rel="noopener">打开认证页面</a
                >继续</template
              >。
            </p>
            <el-button :loading="pluginPolling" @click="pollPlugin">查询认证结果</el-button>
          </template>
          <template
            v-else-if="
              !data.automatic_status ||
              ['rejected', 'failed', 'expired'].includes(data.automatic_status)
            "
          >
            <el-form label-position="top">
              <el-form-item label="法定姓名">
                <el-input v-model="pluginName" placeholder="与身份证一致" />
              </el-form-item>
              <el-form-item label="身份证号">
                <el-input v-model="pluginIdNumber" placeholder="请输入 18 位身份证号" />
              </el-form-item>
              <el-button type="primary" :loading="pluginStarting" @click="startPlugin">
                开始自动认证
              </el-button>
            </el-form>
          </template>
        </div>
      </div>
      <el-empty v-else-if="!loading" description="实名服务未配置" />
    </div>
  </div>
</template>

<style scoped>
.verify-stack {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.verify-status {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  padding: 18px 20px;
}

.verify-status__icon {
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

.verify-status__copy {
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.verify-status__copy small {
  color: var(--art-gray-500);
  font-size: 12px;
}

.verify-status__copy strong {
  color: var(--art-gray-900);
  font-size: 15px;
}

.verify-status__copy strong.is-approved {
  color: var(--el-color-success);
}

.verify-status__copy strong.is-rejected {
  color: var(--el-color-danger);
}

.verify-status__copy strong.is-pending {
  color: var(--el-color-warning);
}

.verify-status p {
  width: 100%;
  margin: 4px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
}

.verify-status p.is-reason {
  color: var(--el-color-danger);
}

.verify-card {
  padding: 21px;
}

.verify-card__title {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 4px;
}

.verify-card__title > span {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  color: var(--theme-color);
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
}

.verify-card__title h2 {
  margin: 0;
}

.verify-card__title p {
  margin: 4px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
}

.verify-card h2 {
  margin: 0;
  color: var(--art-gray-800);
  font-size: 15px;
  font-weight: 600;
}

.verify-card__hint {
  margin: 4px 0 16px;
  color: var(--art-gray-500);
  font-size: 12px;
}

.verify-uploads {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}

.verify-uploads :deep(.el-upload) {
  display: block;
  width: 100%;
}

.verify-upload {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 6px;
  width: 100%;
  height: 160px;
  overflow: hidden;
  cursor: pointer;
  background: var(--art-gray-100);
  border: 1.5px dashed var(--art-gray-300);
  border-radius: var(--radius-md);
  transition: border-color 0.2s, background-color 0.2s;
}

.verify-upload:focus-visible {
  outline: 2px solid var(--theme-color);
  outline-offset: 2px;
}

.verify-upload:hover {
  background: var(--theme-color-soft);
  border-color: var(--theme-color);
}

.verify-upload.has-file {
  border-color: var(--theme-color);
  border-style: solid;
}

.verify-upload__img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.verify-upload__plus {
  color: var(--art-gray-500);
  font-size: 26px;
  line-height: 1;
}

.verify-upload:hover .verify-upload__plus,
.verify-upload:hover .verify-upload__hint {
  color: var(--theme-color);
}

.verify-upload__hint {
  color: var(--art-gray-500);
  font-size: 12px;
}

.verify-submit {
  width: 100%;
  min-height: 42px;
  justify-content: center;
}

.verify-idle {
  margin: 0;
  padding: 18px 20px;
  color: var(--art-gray-600);
  font-size: 13px;
  line-height: 1.7;
}

.verify-plugin-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-bottom: 14px;
}

.verify-card__hint a {
  color: var(--theme-color);
  font-weight: 600;
}

@media (max-width: 560px) {
  .verify-uploads {
    grid-template-columns: 1fr;
  }
}
</style>
