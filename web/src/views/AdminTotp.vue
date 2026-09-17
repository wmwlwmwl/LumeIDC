<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { http } from '../http/index'
import { useSession } from '../http/session'
import { copyText } from '@/utils/clipboard'

const data = ref<{ ok: number; secret: string; enabled: boolean; uri: string } | null>(null)
const code = ref('')
const loading = ref(false)
const busy = ref(false)

// 后台可能挂在自定义路径（如 /panel），二维码请求需走当前后台基址
const session = useSession()
const qrSrc = computed(() => {
  const base = (session.adminPath || '/').replace(/\/$/, '')
  return base + '/totp/qr'
})

async function load() {
  loading.value = true
  try {
    data.value = (await http.get('/totp')) as unknown as typeof data.value
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '读取失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function enable() {
  if (!code.value) {
    ElMessage.warning('请输入验证码')
    return
  }
  busy.value = true
  try {
    const res = (await http.post('/totp', { action: 'enable', code: code.value })) as { ok: number; msg?: string }
    if (String(res.ok) === '1') {
      ElMessage.success('两步验证已启用')
      await load()
    } else {
      ElMessage.error(res.msg || '操作失败')
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '操作失败')
  } finally {
    busy.value = false
  }
}

async function disable() {
  busy.value = true
  try {
    const res = (await http.post('/totp', { action: 'disable' })) as { ok: number; msg?: string }
    if (String(res.ok) === '1') {
      ElMessage.success('两步验证已关闭')
      await load()
    } else {
      ElMessage.error(res.msg || '操作失败')
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '操作失败')
  } finally {
    busy.value = false
  }
}

async function copySecret() {
  // 不再判断 navigator.clipboard：纯 HTTP 下它不存在，会让按钮静默失效（copyText 内部会降级）
  if (await copyText(data.value?.secret)) ElMessage.success('密钥已复制')
  else ElMessage.error('复制失败，请手动复制')
}
</script>

<template>
  <div class="art-full-height" v-loading="loading">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>安全验证</h4>
            <p>两步验证（TOTP），登录时额外校验动态口令。</p>
          </div>
        </div>
      </template>

      <div v-if="data">
        <template v-if="data.enabled">
          <el-result icon="success" title="两步验证已启用" sub-title="登录管理员账号时需输入动态验证码。" />
          <el-button type="danger" plain :loading="busy" @click="disable">关闭两步验证</el-button>
        </template>
        <template v-else>
          <p class="admin-totp-hint">
            用 Authenticator 应用（Google / Microsoft 等）扫描二维码，或手动输入下方密钥：
          </p>
          <!-- 二维码按当前后台路径拼接（自定义路径时 /admin 被屏蔽） -->
          <div class="admin-totp-body">
            <img :src="qrSrc" alt="两步验证二维码" class="admin-totp-qr" />
            <div class="admin-totp-secret">
              <div class="admin-totp-secret__label">密钥（手动输入用）</div>
              <div class="admin-totp-secret__row">
                <code>{{ data.secret }}</code>
                <el-button size="small" @click="copySecret">复制</el-button>
              </div>
            </div>
          </div>
          <el-form label-position="top" @submit.prevent="enable">
            <el-form-item label="当前动态验证码（6 位）"><el-input v-model="code" inputmode="numeric" maxlength="6" placeholder="输入应用显示的验证码" /></el-form-item>
            <el-button type="primary" :loading="busy" @click="enable">启用两步验证</el-button>
          </el-form>
        </template>
      </div>
    </ElCard>
  </div>
</template>

<style scoped>
.admin-totp-hint {
  margin: 0 0 13px;
  color: var(--art-gray-600);
  font-size: 12px;
  line-height: 1.7;
}
.admin-totp-body {
  margin-bottom: 15px;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 17px;
}
.admin-totp-qr {
  width: 155px;
  height: 155px;
  padding: 5px;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: 11px;
}
.admin-totp-secret {
  min-width: 0;
  flex: 1;
}
.admin-totp-secret__label {
  margin-bottom: 6px;
  color: var(--art-gray-500);
  font-size: 11px;
}
.admin-totp-secret__row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.admin-totp-secret__row code {
  min-width: 0;
  flex: 1;
  padding: 9px 12px;
  color: var(--art-gray-700);
  font-size: 12px;
  word-break: break-all;
  background: var(--art-gray-50);
  border-radius: 8px;
}
</style>
