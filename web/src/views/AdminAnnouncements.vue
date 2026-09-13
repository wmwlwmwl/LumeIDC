<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Bell } from '@element-plus/icons-vue'
import { fetchAdminAnnouncements, saveAnnouncement, deleteAnnouncement, type AdminAnnouncement } from '../admin/api'

const list = ref<AdminAnnouncement[]>([])
const loading = ref(false)
const saving = ref(false)

const dialog = ref(false)
const editing = ref<AdminAnnouncement | null>(null)
const form = ref({ title: '', category: '', summary: '', content: '', cover: '', hidden: false, pinned: false })

async function load() {
  loading.value = true
  try {
    list.value = await fetchAdminAnnouncements()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '查询失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

function openNew() {
  editing.value = null
  form.value = { title: '', category: '', summary: '', content: '', cover: '', hidden: false, pinned: false }
  dialog.value = true
}
function openEdit(an: AdminAnnouncement) {
  editing.value = an
  form.value = {
    title: an.title,
    category: an.category || '',
    summary: an.summary || '',
    content: an.content || '',
    cover: an.cover || '',
    hidden: an.hidden,
    pinned: an.pinned,
  }
  dialog.value = true
}

async function save() {
  if (!form.value.title.trim()) {
    ElMessage.warning('请输入标题')
    return
  }
  saving.value = true
  try {
    await saveAnnouncement({ id: editing.value?.id, ...form.value, title: form.value.title.trim() })
    ElMessage.success('已保存')
    dialog.value = false
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function del(an: AdminAnnouncement) {
  const ok = await ElMessageBox.confirm(`确认删除公告「${an.title}」？`, '删除公告', {
    type: 'warning',
    confirmButtonText: '删除',
    cancelButtonText: '取消',
  }).catch(() => null)
  if (!ok) return
  try {
    await deleteAnnouncement(an.id)
    ElMessage.success('已删除')
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '删除失败')
  }
}
</script>

<template>
  <div class="art-full-height">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>系统公告</h4>
            <p>管理前台展示的公告内容。</p>
          </div>
          <el-button type="primary" @click="openNew"><el-icon><Plus /></el-icon>新增公告</el-button>
        </div>
      </template>

      <div v-loading="loading" class="admin-announcements">
        <div v-for="an in list" :key="an.id" class="admin-announcement">
          <span class="admin-announcement__icon"><el-icon><Bell /></el-icon></span>
          <div class="admin-announcement__body">
            <div class="admin-announcement__title">
              <span v-if="an.pinned" class="admin-announcement__pin">置顶</span>
              <span v-if="an.hidden" class="admin-announcement__hidden">隐藏</span>
              <span v-if="an.category" class="admin-announcement__cat">{{ an.category }}</span>
              <strong>{{ an.title }}</strong>
              <span class="admin-announcement__reads">阅读 {{ an.reads ?? 0 }}</span>
              <time>{{ an.created_at }}</time>
            </div>
            <p v-if="an.summary || an.content">{{ an.summary || an.content }}</p>
          </div>
          <div class="admin-announcement__ops">
            <el-button size="small" text type="primary" @click="openEdit(an)">编辑</el-button>
            <el-button size="small" text type="danger" @click="del(an)">删除</el-button>
          </div>
        </div>
        <el-empty v-if="!list.length && !loading" description="暂无公告" />
      </div>
    </ElCard>

    <el-dialog v-model="dialog" :title="editing ? '编辑公告' : '新增公告'" width="560px">
      <el-form label-position="top">
        <el-form-item label="公告标题" required><el-input v-model="form.title" maxlength="200" /></el-form-item>
        <el-form-item label="分类（可选，如：公告 / 活动 / 维护）"><el-input v-model="form.category" maxlength="50" placeholder="用于前台公告分类筛选" /></el-form-item>
        <el-form-item label="摘要（可选，列表展示）"><el-input v-model="form.summary" type="textarea" :rows="2" maxlength="200" /></el-form-item>
        <el-form-item label="封面图 URL（可选）"><el-input v-model="form.cover" placeholder="https://..." /></el-form-item>
        <el-form-item label="公告内容"><el-input v-model="form.content" type="textarea" :rows="8" /></el-form-item>
        <div class="admin-checkbox-row">
          <el-checkbox v-model="form.pinned">置顶（显示在列表顶部）</el-checkbox>
          <el-checkbox v-model="form.hidden">隐藏（不展示在前台）</el-checkbox>
        </div>
      </el-form>
      <template #footer><el-button @click="dialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存</el-button></template>
    </el-dialog>
  </div>
</template>

<style scoped>
.admin-announcements { display: flex; flex-direction: column; gap: 8px; }
.admin-announcement { padding: 14px 15px; display: flex; gap: 11px; background: var(--art-gray-50); border: 1px solid var(--art-card-border); border-radius: 10px; }
.admin-announcement__icon { width: 30px; height: 30px; display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; color: var(--theme-color-deep); background: var(--theme-color-soft); border-radius: 8px; }
.admin-announcement__body { min-width: 0; flex: 1; }
.admin-announcement__title { display: flex; align-items: center; gap: 7px; }
.admin-announcement__title strong { overflow: hidden; color: var(--art-gray-800); font-size: 13px; text-overflow: ellipsis; white-space: nowrap; }
.admin-announcement__title time { margin-left: auto; flex-shrink: 0; color: var(--art-gray-400); font-size: 10px; }
.admin-announcement__pin { padding: 2px 6px; flex-shrink: 0; color: var(--el-color-danger); font-size: 9px; background: var(--el-color-danger-light-9); border-radius: 4px; }
.admin-announcement__hidden { padding: 2px 6px; flex-shrink: 0; color: var(--art-gray-500); font-size: 9px; background: var(--art-gray-100); border-radius: 4px; }
.admin-announcement__cat { padding: 2px 6px; flex-shrink: 0; color: var(--theme-color); font-size: 9px; background: var(--theme-color-soft); border-radius: 4px; }
.admin-announcement__reads { flex-shrink: 0; color: var(--art-gray-400); font-size: 10px; }
.admin-announcement__body p { margin: 7px 0 0; color: var(--art-gray-500); font-size: 11px; line-height: 1.7; white-space: pre-wrap; }
.admin-announcement__ops { display: flex; align-items: center; gap: 2px; }
.admin-checkbox-row { display: flex; gap: 20px; }
@media (max-width: 640px) { .admin-announcement__title { flex-wrap: wrap; } .admin-announcement__title time { width: 100%; margin-left: 0; } .admin-announcement__ops { flex-direction: column; } }
</style>