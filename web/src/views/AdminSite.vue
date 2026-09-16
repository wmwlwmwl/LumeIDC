<script setup lang="ts">
import { reactive, ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { http } from '../http/index'
import { useSession } from '../http/session'

const session = useSession()
const loading = ref(true)
const saving = ref(false)
interface SiteContact {
  type: string
  name: string
  value: string
  link: string
}

const contacts = ref<SiteContact[]>([])
const form = reactive<Record<string, string>>({
  site_name: '',
  site_description: '',
  site_keywords: '',
  service_email: '',
  service_phone: '',
  service_hours: '',
  site_url: '',
  listen_port: '',
  admin_path: '',
  upstream_timezone: '',
})

onMounted(async () => {
  try {
    const res = (await http.get('/site')) as Record<string, string>
    for (const k of Object.keys(form)) form[k] = res[k] || ''
    try {
      const parsed = JSON.parse(res.service_contacts || '[]')
      contacts.value = Array.isArray(parsed) ? parsed.map((c) => ({
        type: String(c.type || 'custom'), name: String(c.name || ''), value: String(c.value || ''), link: String(c.link || ''),
      })) : []
    } catch {
      contacts.value = []
    }
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '读取设置失败')
  } finally {
    loading.value = false
  }
})

function addContact(type = 'custom') {
  contacts.value.push({ type, name: type === 'qq' ? 'QQ' : type === 'wechat' ? '微信' : '', value: '', link: '' })
}
function removeContact(index: number) {
  contacts.value.splice(index, 1)
}

async function save() {
  saving.value = true
  try {
    const res = (await http.post('/site', { ...form, service_contacts: JSON.stringify(contacts.value) })) as { ok: number; msg?: string; admin_path?: string }
    if (String(res.ok) === '1') {
      ElMessage.success('已保存')
      const prevBase = (session.adminPath || '/').replace(/\/$/, '')
      // 后台路径可能已改：重新拉会话，并整页回到新后台根（SPA 外壳）。
      // 不能跳到 /site —— 该路径在新基址下命中的是 SSR 表单页，会跳出 SPA。
      await import('../http/session').then((m) => m.loadSession())
      const nextBase = (res.admin_path || prevBase).replace(/\/$/, '')
      if (nextBase !== prevBase) {
        session.adminPath = nextBase
        location.href = nextBase + '/' // 旧前缀已失效，必须整页加载新地址
      }
    } else {
      ElMessage.error(res.msg || '保存失败')
    }
  } catch (err: unknown) {
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
            <h4>站点设置</h4>
            <p>站点品牌、联系方式与后台访问入口。</p>
          </div>
        </div>
      </template>

      <el-form label-position="top">
        <el-form-item label="站点名称" required>
          <el-input v-model="form.site_name" maxlength="128" />
        </el-form-item>
        <el-form-item label="站点描述">
          <el-input v-model="form.site_description" type="textarea" :rows="2" />
        </el-form-item>
        <el-form-item label="关键词">
          <el-input v-model="form.site_keywords" type="textarea" :rows="2" />
        </el-form-item>

        <el-divider content-position="left">联系方式</el-divider>
        <div class="admin-form-grid">
          <el-form-item label="服务邮箱">
            <el-input v-model="form.service_email" />
          </el-form-item>
          <el-form-item label="服务电话">
            <el-input v-model="form.service_phone" />
          </el-form-item>
        </div>
        <el-form-item label="工作时间">
          <el-input v-model="form.service_hours" placeholder="如 9:00-18:00" />
        </el-form-item>
        <el-form-item label="其他联系方式">
          <div class="contact-editor">
            <div v-for="(contact, index) in contacts" :key="index" class="contact-row">
              <el-select v-model="contact.type" style="width: 110px">
                <el-option label="QQ" value="qq" />
                <el-option label="微信" value="wechat" />
                <el-option label="自定义" value="custom" />
              </el-select>
              <el-input v-model="contact.name" placeholder="名称，如技术支持" style="width: 140px" />
              <el-input v-model="contact.value" placeholder="账号或联系方式" class="contact-value" />
              <el-input v-model="contact.link" placeholder="链接（可选，如 https://...）" class="contact-link" />
              <el-button text type="danger" @click="removeContact(index)">删除</el-button>
            </div>
            <div class="contact-actions">
              <el-button size="small" @click="addContact('qq')">+ QQ</el-button>
              <el-button size="small" @click="addContact('wechat')">+ 微信</el-button>
              <el-button size="small" @click="addContact()">+ 自定义</el-button>
            </div>
          </div>
        </el-form-item>

        <el-divider content-position="left">访问</el-divider>
        <el-form-item label="站点地址（site_url）">
          <el-input v-model="form.site_url" placeholder="留空自动按访问请求推断" />
        </el-form-item>
        <div class="admin-form-grid">
          <el-form-item label="监听端口">
            <el-input v-model="form.listen_port" placeholder="留空使用 config.yaml 的 listen" />
          </el-form-item>
          <el-form-item label="后台访问路径（admin_path）">
            <el-input v-model="form.admin_path" placeholder="留空使用 /admin" />
          </el-form-item>
        </div>
        <el-form-item label="上游面板时区（upstream_timezone）">
          <el-input v-model="form.upstream_timezone" placeholder="留空按本机时区，如 Asia/Shanghai / UTC" />
        </el-form-item>
        <p class="admin-warn">上游到期时间若与本机时区不一致（如容器内为 UTC 而上游面板为北京时间），请在此填写上游面板的 IANA 时区名，否则到期时间会偏移。</p>
        <p class="admin-warn">修改监听端口 / 后台路径会立即生效：端口切换后如无法重连请用新地址访问；路径修改后旧前缀即刻失效。</p>

        <el-button type="primary" :loading="saving" @click="save">保存设置</el-button>
      </el-form>
    </ElCard>
  </div>
</template>

<style scoped>
.contact-editor {
  display: flex;
  flex-direction: column;
  width: 100%;
  gap: 8px;
}
.contact-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.contact-value,
.contact-link {
  flex: 1 1 180px;
  min-width: 160px;
}
.contact-actions {
  display: flex;
  gap: 8px;
}
.admin-form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}
.admin-warn {
  margin: 0 0 14px;
  padding: 10px 12px;
  color: var(--el-color-warning);
  font-size: 11px;
  line-height: 1.7;
  background: color-mix(in srgb, var(--el-color-warning) 12%, transparent);
  border-radius: 8px;
}
@media (max-width: 620px) {
  .admin-form-grid {
    grid-template-columns: 1fr;
  }
}
</style>
