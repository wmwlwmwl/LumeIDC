<script setup lang="ts">
import { onMounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { http } from '@/http'

interface Props {
  /** 有值为编辑回填，空为新增 */
  recordId?: number
}

const props = defineProps<Props>()
const emit = defineEmits<{ (e: 'saved', id: number): void }>()

interface FormOptions {
  types: string[]
  actions: string[]
  levels: Record<string, string>
  defaultPublic: boolean
}

interface UserOption {
  id: number
  email: string
  name: string
}

interface RecordItem {
  user_id?: number
  user_name?: string
  user_email?: string
  type?: string
  level?: string
  description?: string
  evidence_url?: string
  action?: string
  starts_at?: string
  expires_at?: string
  public?: boolean
  note?: string
}

const options = ref<FormOptions>({ types: [], actions: [], levels: {}, defaultPublic: false })
const userOptions = ref<UserOption[]>([])
const userSearching = ref(false)
const saving = ref(false)

const form = reactive({
  user_id: undefined as number | undefined,
  type: '',
  level: '',
  description: '',
  evidence_url: '',
  action: '',
  starts_at: '',
  expires_at: '',
  public: false,
  note: '',
})

function resetForm() {
  form.user_id = undefined
  form.type = ''
  form.level = ''
  form.description = ''
  form.evidence_url = ''
  form.action = ''
  form.starts_at = ''
  form.expires_at = ''
  form.public = options.value.defaultPublic
  form.note = ''
  userOptions.value = []
}

async function loadForm() {
  try {
    const res = await http.get<{ ok: number; item?: RecordItem; options?: FormOptions }>('/plugin/violation/form', {
      id: props.recordId || '',
    })
    options.value = res.options || { types: [], actions: [], levels: {}, defaultPublic: false }
    const item = res.item || {}
    form.user_id = item.user_id
    form.type = item.type || ''
    form.level = item.level || ''
    form.description = item.description || ''
    form.evidence_url = item.evidence_url || ''
    form.action = item.action || ''
    form.starts_at = item.starts_at || ''
    form.expires_at = item.expires_at || ''
    form.public = props.recordId ? !!item.public : options.value.defaultPublic
    form.note = item.note || ''
    userOptions.value = item.user_id
      ? [{ id: item.user_id, email: item.user_email || '', name: item.user_name || '' }]
      : []
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '表单初始化失败')
  }
}

async function searchUsers(keyword: string) {
  if (!keyword.trim()) return
  userSearching.value = true
  try {
    const res = await http.get<{ ok: number; list?: UserOption[] }>('/plugin/violation/users/search', {
      keyword: keyword.trim(),
    })
    if (String(res.ok) === '1') userOptions.value = res.list || []
  } catch {
    /* 搜索失败保持原选项 */
  } finally {
    userSearching.value = false
  }
}

function userLabel(u: UserOption) {
  return u.name ? `${u.name}（${u.email}）` : u.email
}

function validate(): string | null {
  if (!form.user_id) return '请选择违规用户'
  if (!form.level) return '请选择违规等级'
  if (!form.type.trim()) return '请填写违规类型'
  if (!form.description.trim()) return '请填写违规描述'
  if (form.starts_at && form.expires_at && form.expires_at <= form.starts_at) {
    return '过期时间必须晚于生效时间'
  }
  return null
}

async function submit() {
  const message = validate()
  if (message) {
    ElMessage.error(message)
    return
  }
  saving.value = true
  try {
    const res = await http.post<{ ok: number; msg?: string }>('/plugin/violation/save', {
      id: props.recordId || 0,
      user_id: form.user_id,
      level: form.level,
      type: form.type.trim(),
      description: form.description.trim(),
      evidence_url: form.evidence_url.trim(),
      action: form.action,
      starts_at: form.starts_at,
      expires_at: form.expires_at,
      public: form.public,
      note: form.note.trim(),
    })
    if (String(res.ok) !== '1') {
      ElMessage.error(res.msg || '保存失败')
      return
    }
    ElMessage.success('已保存')
    emit('saved', props.recordId || 0)
    if (!props.recordId) resetForm()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    saving.value = false
  }
}

onMounted(loadForm)
watch(() => props.recordId, loadForm)
</script>

<template>
  <el-form label-position="top" :model="form" @submit.prevent>
    <div class="form-grid">
      <el-form-item label="违规用户" required>
        <el-select
          v-model="form.user_id"
          class="w-full"
          filterable
          remote
          reserve-keyword
          clearable
          placeholder="输入邮箱或用户名搜索"
          :remote-method="searchUsers"
          :loading="userSearching"
        >
          <el-option v-for="u in userOptions" :key="u.id" :label="userLabel(u)" :value="u.id" />
        </el-select>
      </el-form-item>
      <el-form-item label="违规类型" required>
        <el-select v-if="options.types.length" v-model="form.type" class="w-full" placeholder="选择违规类型" clearable>
          <el-option v-for="t in options.types" :key="t" :label="t" :value="t" />
        </el-select>
        <el-input v-else v-model="form.type" maxlength="50" placeholder="如：滥用资源" />
      </el-form-item>
      <el-form-item label="违规等级" required>
        <el-radio-group v-model="form.level">
          <el-radio v-for="(label, value) in options.levels" :key="value" :value="value">{{ label }}</el-radio>
        </el-radio-group>
      </el-form-item>
    </div>

    <el-form-item label="违规描述" required>
      <el-input
        v-model="form.description"
        type="textarea"
        :rows="4"
        maxlength="2000"
        show-word-limit
        placeholder="描述违规事实、发生时间与影响范围"
      />
    </el-form-item>

    <div class="form-grid">
      <el-form-item label="处置措施">
        <el-select v-if="options.actions.length" v-model="form.action" class="w-full" placeholder="选择处置措施" clearable>
          <el-option v-for="a in options.actions" :key="a" :label="a" :value="a" />
        </el-select>
        <el-input v-else v-model="form.action" maxlength="50" placeholder="如：警告" />
      </el-form-item>
      <el-form-item label="生效时间">
        <el-date-picker
          v-model="form.starts_at"
          class="w-full"
          type="datetime"
          value-format="YYYY-MM-DD HH:mm"
          placeholder="留空则立即生效"
        />
      </el-form-item>
      <el-form-item label="过期时间">
        <el-date-picker
          v-model="form.expires_at"
          class="w-full"
          type="datetime"
          value-format="YYYY-MM-DD HH:mm"
          placeholder="留空则长期有效"
        />
      </el-form-item>
    </div>

    <div class="form-grid">
      <el-form-item label="凭证链接">
        <el-input v-model="form.evidence_url" maxlength="500" placeholder="https://..." />
      </el-form-item>
      <el-form-item label="是否公示">
        <el-switch v-model="form.public" />
      </el-form-item>
    </div>

    <el-form-item label="内部备注">
      <el-input v-model="form.note" type="textarea" :rows="2" maxlength="1000" placeholder="仅管理员可见" />
    </el-form-item>

    <div class="form-actions">
      <el-button type="primary" :loading="saving" @click="submit">保存</el-button>
      <el-button :disabled="saving" @click="resetForm">重置</el-button>
    </div>
  </el-form>
</template>

<style scoped>
.form-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0 20px; }
.form-actions { display: flex; gap: 12px; }
@media (max-width: 1100px) {
  .form-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 640px) {
  .form-grid { grid-template-columns: minmax(0, 1fr); }
}
</style>
