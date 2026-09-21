<script setup lang="ts">
import { ref, reactive, computed } from 'vue'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { Lock } from '@element-plus/icons-vue'
import { changePassword } from '@/api/user'
import PublicPageHead from '@/components/public/PublicPageHead.vue'

const formRef = ref<FormInstance>()
const form = reactive({ oldP: '', newP: '', confirmP: '' })
const loading = ref(false)

const rules: FormRules = {
  oldP: [{ required: true, message: '请输入旧密码', trigger: 'blur' }],
  newP: [
    { required: true, message: '请输入新密码', trigger: 'blur' },
    { min: 8, message: '新密码至少 8 位', trigger: 'blur' },
    {
      validator: (_rule, value, callback) => {
        if (value && value === form.oldP) callback(new Error('新密码不能与旧密码相同'))
        else callback()
      },
      trigger: 'blur',
    },
  ],
  confirmP: [
    { required: true, message: '请再次输入新密码', trigger: 'blur' },
    {
      validator: (_rule, value, callback) => {
        if (value !== form.newP) callback(new Error('两次输入的新密码不一致'))
        else callback()
      },
      trigger: 'blur',
    },
  ],
}

// 简易强度评估：长度 + 字符种类（仅作提示，后端只校验长度）
const strength = computed(() => {
  const v = form.newP
  if (!v) return { level: 0, label: '', color: '' }
  let score = 0
  if (v.length >= 8) score++
  if (v.length >= 12) score++
  if (/[a-z]/.test(v) && /[A-Z]/.test(v)) score++
  if (/\d/.test(v)) score++
  if (/[^\w\s]/.test(v)) score++
  if (score <= 2) return { level: 1, label: '弱', color: 'is-weak' }
  if (score <= 3) return { level: 2, label: '中', color: 'is-mid' }
  return { level: 3, label: '强', color: 'is-strong' }
})

async function submit() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  loading.value = true
  try {
    const res = await changePassword(form.oldP, form.newP)
    if (String(res.ok) === '1') {
      ElMessage.success('密码已修改，请重新登录')
      // 后端会吊销该用户的全部会话，必须重新登录
      location.href = '/login'
    } else {
      ElMessage.error(res.msg || '修改失败')
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '修改失败')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div>
    <PublicPageHead title="安全设置" subtitle="修改登录密码，保护账户安全。" />

    <div class="password-page">
      <div class="art-card password-card">
        <div class="password-card__title">
          <span><el-icon><Lock /></el-icon></span>
          <div>
            <h2>修改密码</h2>
            <p>定期更新密码，保持账户安全。</p>
          </div>
        </div>
        <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @submit.prevent="submit">
          <el-form-item label="旧密码" prop="oldP">
            <el-input
              v-model="form.oldP"
              type="password"
              show-password
              autocomplete="current-password"
              :disabled="loading"
            />
          </el-form-item>
          <el-form-item label="新密码（至少 8 位）" prop="newP">
            <el-input
              v-model="form.newP"
              type="password"
              show-password
              autocomplete="new-password"
              :disabled="loading"
            />
          </el-form-item>
          <div v-if="form.newP" class="password-strength">
            <div class="password-strength__segments">
              <span v-for="i in 3" :key="i" :class="i <= strength.level ? strength.color : ''" />
            </div>
            <p>强度：{{ strength.label }}（建议混合大小写字母、数字与符号）</p>
          </div>
          <el-form-item label="确认新密码" prop="confirmP">
            <el-input
              v-model="form.confirmP"
              type="password"
              show-password
              autocomplete="new-password"
              :disabled="loading"
              @keyup.enter="submit"
            />
          </el-form-item>
          <el-button type="primary" class="password-submit" :loading="loading" @click="submit">修改密码</el-button>
        </el-form>
      </div>

      <div class="art-card password-advice">
        <h3>安全建议</h3>
        <ul>
          <li>使用 12 位以上、且不含个人信息的密码</li>
          <li>不要在其它网站重复使用同一密码</li>
          <li>修改密码后需重新登录（其它设备会话同时失效）</li>
        </ul>
      </div>
    </div>
  </div>
</template>

<style scoped>
.password-page {
  display: grid;
  grid-template-columns: minmax(0, 1.3fr) minmax(0, 1fr);
  gap: 14px;
  align-items: start;
}

.password-card,
.password-advice {
  padding: 22px;
}

.password-card h2 {
  margin: 0;
  color: var(--art-gray-800);
  font-size: 16px;
  font-weight: 600;
}

.password-card__title {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 18px;
}

.password-card__title > span {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  color: var(--theme-color);
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
}

.password-card__title p {
  margin: 4px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
}

.password-strength {
  margin: 0 0 18px;
}

.password-strength__segments {
  display: flex;
  gap: 5px;
}

.password-strength__segments span {
  position: relative;
  height: 4px;
  flex: 1;
  background: var(--art-gray-100);
  border-radius: 4px;
}

.password-strength__segments span.is-weak {
  background: var(--el-color-danger);
}

.password-strength__segments span.is-mid {
  background: var(--el-color-warning);
}

.password-strength__segments span.is-strong {
  background: var(--el-color-success);
}

.password-strength p {
  margin: 6px 0 0;
  color: var(--art-gray-500);
  font-size: 11px;
}

.password-submit {
  width: 100%;
  min-height: 42px;
  justify-content: center;
}

.password-advice h3 {
  margin: 0 0 12px;
  color: var(--art-gray-900);
  font-size: 16px;
  font-weight: 600;
}

.password-advice ul {
  margin: 0;
  padding-left: 18px;
  list-style: disc;
}

.password-advice li {
  padding: 5px 0;
  color: var(--art-gray-500);
  font-size: 12px;
  line-height: 1.7;
}

@media (max-width: 700px) {
  .password-page {
    grid-template-columns: 1fr;
  }
}
</style>
