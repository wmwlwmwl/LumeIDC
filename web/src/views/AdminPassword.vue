<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { fetchAdminUsername, changeAdminUsername, changeAdminPassword } from '../admin/api'

// ---- 登录名 ----
const currentUsername = ref('')
const username = ref('')
const usernamePassword = ref('')
const savingName = ref(false)
const loadingName = ref(false)

async function loadUsername() {
  loadingName.value = true
  try {
    currentUsername.value = await fetchAdminUsername()
    username.value = currentUsername.value
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '读取账户信息失败')
  } finally {
    loadingName.value = false
  }
}
onMounted(loadUsername)

const nameChanged = computed(() => username.value.trim() !== '' && username.value.trim() !== currentUsername.value)

async function saveUsername() {
  if (!nameChanged.value) {
    ElMessage.warning('请输入新的登录名')
    return
  }
  if (!usernamePassword.value) {
    ElMessage.warning('请输入当前密码以确认修改')
    return
  }
  savingName.value = true
  try {
    await changeAdminUsername(username.value.trim(), usernamePassword.value)
    ElMessage.success('登录名已更新，下次登录请使用新登录名')
    usernamePassword.value = ''
    await loadUsername()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '修改失败')
  } finally {
    savingName.value = false
  }
}

// ---- 密码 ----
const oldP = ref('')
const newP = ref('')
const confirmP = ref('')
const savingPwd = ref(false)

const strength = computed(() => {
  const v = newP.value
  if (!v) return { level: 0, label: '', color: '' }
  let score = 0
  if (v.length >= 8) score++
  if (v.length >= 12) score++
  if (/[a-z]/.test(v) && /[A-Z]/.test(v)) score++
  if (/\d/.test(v)) score++
  if (/[^\w\s]/.test(v)) score++
  if (score <= 2) return { level: 1, label: '弱', color: 'bg-danger' }
  if (score <= 3) return { level: 2, label: '中', color: 'bg-warning' }
  return { level: 3, label: '强', color: 'bg-success' }
})

async function savePassword() {
  if (!oldP.value) {
    ElMessage.warning('请输入当前密码')
    return
  }
  if (newP.value.length < 8) {
    ElMessage.warning('新密码至少 8 位')
    return
  }
  if (newP.value === oldP.value) {
    ElMessage.warning('新密码不能与当前密码相同')
    return
  }
  if (newP.value !== confirmP.value) {
    ElMessage.warning('两次输入的新密码不一致')
    return
  }
  savingPwd.value = true
  try {
    await changeAdminPassword(oldP.value, newP.value)
    ElMessage.success('密码已修改，请重新登录')
    // 后端已吊销会话：整页重载，路由守卫会把未登录态送回后台登录页
    setTimeout(() => location.reload(), 600)
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '修改失败')
  } finally {
    savingPwd.value = false
  }
}
</script>

<template>
  <div class="art-full-height" v-loading="loadingName">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>账户设置</h4>
            <p>管理员本人的登录名与密码。</p>
          </div>
        </div>
      </template>

      <div class="admin-two-col">
        <!-- 登录名 -->
        <section class="admin-pwd-block">
          <h5>登录名</h5>
          <p class="admin-card-hint">
            当前登录名：<strong>{{ currentUsername || '-' }}</strong>。修改后下次登录需使用新登录名（当前会话不受影响）。
          </p>
          <el-form label-position="top">
            <el-form-item label="新登录名">
              <el-input v-model="username" placeholder="新的登录名" />
            </el-form-item>
            <el-form-item label="当前密码（确认修改）">
              <el-input
                v-model="usernamePassword"
                type="password"
                show-password
                autocomplete="current-password"
                @keyup.enter="saveUsername"
              />
            </el-form-item>
            <el-button type="primary" :loading="savingName" :disabled="!nameChanged" @click="saveUsername">
              保存登录名
            </el-button>
          </el-form>
        </section>

        <!-- 密码 -->
        <section class="admin-pwd-block">
          <h5>修改密码</h5>
          <p class="admin-card-hint">修改密码会吊销当前会话，需重新登录。</p>
          <el-form label-position="top" @submit.prevent="savePassword">
            <el-form-item label="当前密码">
              <el-input v-model="oldP" type="password" show-password autocomplete="current-password" />
            </el-form-item>
            <el-form-item label="新密码（至少 8 位）">
              <el-input v-model="newP" type="password" show-password autocomplete="new-password" />
            </el-form-item>
            <div v-if="newP" class="-mt-3 mb-4">
              <div class="flex gap-1">
                <span
                  v-for="i in 3"
                  :key="i"
                  class="h-1 flex-1 rounded-full"
                  :class="i <= strength.level ? strength.color : 'bg-g-300'"
                ></span>
              </div>
              <p class="mt-1 text-xs text-g-500">强度：{{ strength.label }}（建议混合大小写字母、数字与符号）</p>
            </div>
            <el-form-item label="确认新密码">
              <el-input v-model="confirmP" type="password" show-password autocomplete="new-password" @keyup.enter="savePassword" />
            </el-form-item>
            <el-button type="primary" :loading="savingPwd" @click="savePassword">修改密码</el-button>
          </el-form>
        </section>
      </div>
    </ElCard>
  </div>
</template>

<style scoped>
.admin-two-col {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 28px;
}
.admin-pwd-block + .admin-pwd-block {
  padding-left: 28px;
  border-left: 1px solid var(--art-card-border);
}
.admin-pwd-block h5 {
  margin: 0 0 6px;
  color: var(--art-gray-800);
  font-size: 14px;
  font-weight: 600;
}
.admin-card-hint {
  margin: 0 0 14px;
  color: var(--art-gray-500);
  font-size: 12px;
  line-height: 1.7;
}
.admin-card-hint strong {
  color: var(--art-gray-700);
}
@media (max-width: 720px) {
  .admin-two-col {
    grid-template-columns: 1fr;
    gap: 22px;
  }
  .admin-pwd-block + .admin-pwd-block {
    padding-left: 0;
    border-left: 0;
    border-top: 1px solid var(--art-card-border);
    padding-top: 22px;
  }
}
</style>
