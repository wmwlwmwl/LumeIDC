<script setup lang="ts">
import { reactive, ref } from 'vue'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { http } from '../http'

const formRef = ref<FormInstance>()
const form = reactive({
  db_host: '127.0.0.1',
  db_port: '5432',
  db_name: '',
  db_user: 'postgres',
  db_pass: '',
  admin_user: '',
  admin_pass: '',
})
const loading = ref(false)
const done = ref(false)

const rules: FormRules = {
  db_host: [{ required: true, message: '请输入数据库地址', trigger: 'blur' }],
  db_port: [{ required: true, message: '请输入数据库端口', trigger: 'blur' }],
  db_name: [{ required: true, message: '请输入数据库名', trigger: 'blur' }],
  db_user: [{ required: true, message: '请输入数据库用户', trigger: 'blur' }],
  admin_user: [{ required: true, message: '请输入管理员账号', trigger: 'blur' }],
  admin_pass: [{ required: true, min: 8, message: '管理员密码至少 8 位', trigger: 'blur' }],
}

async function submit() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  loading.value = true
  try {
    const res = await http.post<{ ok: number; msg?: string }>('/install', { ...form })
    if (res.ok) {
      done.value = true
      ElMessage.success('安装完成，正在进入管理后台…')
      setTimeout(() => {
        window.location.href = '/admin/login'
      }, 1500)
    } else {
      ElMessage.error(res.msg || '安装失败')
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '安装失败')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="install-page">
    <section class="art-card install-card">
      <h1 class="install-title">安装 LumeIDC</h1>
      <p class="install-sub">填写数据库连接信息与管理员账号，一步完成初始化。</p>

      <template v-if="!done">
        <el-form
          ref="formRef"
          :model="form"
          :rules="rules"
          label-position="top"
          class="install-form"
        >
          <el-divider content-position="left">数据库</el-divider>
          <div class="install-grid">
            <el-form-item label="地址" prop="db_host">
              <el-input v-model="form.db_host" />
            </el-form-item>
            <el-form-item label="端口" prop="db_port">
              <el-input v-model="form.db_port" />
            </el-form-item>
            <el-form-item label="库名" prop="db_name">
              <el-input v-model="form.db_name" placeholder="lumeidc" />
            </el-form-item>
            <el-form-item label="用户" prop="db_user">
              <el-input v-model="form.db_user" />
            </el-form-item>
          </div>
          <el-form-item label="数据库密码">
            <el-input v-model="form.db_pass" type="password" show-password />
          </el-form-item>

          <el-divider content-position="left">管理员</el-divider>
          <div class="install-grid">
            <el-form-item label="管理员账号" prop="admin_user">
              <el-input v-model="form.admin_user" placeholder="邮箱或用户名" />
            </el-form-item>
            <el-form-item label="管理员密码" prop="admin_pass">
              <el-input v-model="form.admin_pass" type="password" show-password placeholder="至少 8 位" />
            </el-form-item>
          </div>

          <el-button
            type="primary"
            size="large"
            class="install-submit"
            :loading="loading"
            @click="submit"
          >
            开始安装
          </el-button>
        </el-form>
      </template>

      <el-result v-else icon="success" title="安装完成" sub-title="正在进入管理后台…" />
    </section>
  </div>
</template>

<style scoped>
.install-page {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  padding: 40px 20px;
  background: var(--default-bg-color);
}

.install-card {
  box-sizing: border-box;
  width: min(100%, 620px);
  padding: 36px 40px 32px;
}

.install-title {
  margin: 0;
  color: var(--art-gray-900);
  font-size: 24px;
  font-weight: 600;
}

.install-sub {
  margin: 8px 0 0;
  color: var(--art-gray-500);
  font-size: 13px;
}

.install-form {
  margin-top: 22px;
}

.install-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 16px;
}

.install-submit {
  width: 100%;
  margin-top: 8px;
}

@media (max-width: 640px) {
  .install-card {
    padding: 28px 20px 24px;
  }

  .install-grid {
    grid-template-columns: 1fr;
  }
}
</style>
