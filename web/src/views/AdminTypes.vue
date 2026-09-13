<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { FolderOpened, Plus } from '@element-plus/icons-vue'
import { fetchAdminTypes, saveAdminType, deleteAdminType, moveTypeProducts, type AdminType } from '../admin/api'

const list = ref<AdminType[]>([])
const loading = ref(false)
const saving = ref(false)

const dialog = ref(false)
const editing = ref<AdminType | null>(null)
const form = ref({ name: '', description: '', sort: 0, parent_id: 0, hidden: false })

// 移动产品：目标只能是二级分类（与后端约束一致）
const moveDialog = ref(false)
const moveFrom = ref<AdminType | null>(null)
const moveTarget = ref(0)
const moving = ref(false)
const secondLevelTargets = computed(() =>
  list.value
    .flatMap((f) => (f.children || []).map((c) => ({ ...c, parentName: f.name })))
    .filter((c) => c.id !== moveFrom.value?.id),
)

function openMove(t: AdminType) {
  moveFrom.value = t
  moveTarget.value = secondLevelTargets.value[0]?.id || 0
  moveDialog.value = true
}

async function doMove() {
  if (!moveFrom.value || !moveTarget.value) {
    ElMessage.warning('请选择目标分类')
    return
  }
  moving.value = true
  try {
    const res = await moveTypeProducts(moveFrom.value.id, moveTarget.value)
    ElMessage.success(res.msg || `已移动 ${res.moved} 个产品`)
    moveDialog.value = false
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '移动失败')
  } finally {
    moving.value = false
  }
}

async function load() {
  loading.value = true
  try {
    list.value = await fetchAdminTypes()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '查询失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

function openNew(parent_id = 0) {
  editing.value = null
  form.value = { name: '', description: '', sort: 0, parent_id, hidden: false }
  dialog.value = true
}
function openEdit(t: AdminType) {
  editing.value = t
  form.value = { name: t.name, description: t.description || '', sort: t.sort, parent_id: t.parent_id ?? 0, hidden: t.hidden }
  dialog.value = true
}

async function save() {
  if (!form.value.name.trim()) {
    ElMessage.warning('请输入分类名称')
    return
  }
  saving.value = true
  try {
    await saveAdminType({ id: editing.value?.id, ...form.value, name: form.value.name.trim() })
    ElMessage.success('已保存')
    dialog.value = false
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function del(t: AdminType) {
  const hasProducts = t.product_count > 0 || (t.children || []).some((c) => c.product_count > 0)
  const hint = hasProducts
    ? '该分类下仍有产品，删除前需先在分类管理中把产品移动到其他分类。'
    : ''
  const ok = await ElMessageBox.confirm(`确认删除分类「${t.name}」？${hint}`, '删除分类', {
    type: 'warning',
    confirmButtonText: '删除',
    cancelButtonText: '取消',
  }).catch(() => null)
  if (!ok) return
  try {
    await deleteAdminType(t.id)
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
            <h4>分类管理</h4>
            <p>一级 / 二级分类（最多两级）。</p>
          </div>
          <el-button type="primary" @click="openNew(0)"><el-icon><Plus /></el-icon>新增一级分类</el-button>
        </div>
      </template>

      <div v-loading="loading" class="admin-types">
      <div v-for="first in list" :key="first.id" class="admin-type-group">
        <div class="admin-type-group__head">
          <span class="admin-type-group__icon"><el-icon><FolderOpened /></el-icon></span>
          <strong>{{ first.name }}</strong>
          <el-tag v-if="first.hidden" size="small" type="info">隐藏</el-tag>
          <span class="admin-type-group__count">{{ first.product_count }} 个直挂产品</span>
          <span class="admin-type-group__ops">
            <el-button size="small" text type="primary" @click="openNew(first.id)">加子分类</el-button>
            <el-button v-if="first.product_count > 0" size="small" text @click="openMove(first)">移动产品</el-button>
            <el-button size="small" text type="primary" @click="openEdit(first)">编辑</el-button>
            <el-button size="small" text type="danger" @click="del(first)">删除</el-button>
          </span>
        </div>
        <p v-if="first.description" class="admin-type-group__desc">{{ first.description }}</p>
        <div v-if="first.children?.length" class="admin-type-children">
          <div v-for="c in first.children" :key="c.id" class="admin-type-child">
            <span class="admin-type-child__name">{{ c.name }}</span>
            <el-tag v-if="c.hidden" size="small" type="info">隐藏</el-tag>
            <span class="admin-type-child__count">{{ c.product_count }}</span>
            <span class="admin-type-child__ops">
              <el-button v-if="c.product_count > 0" size="small" text @click="openMove(c)">移动</el-button>
              <el-button size="small" text type="primary" @click="openEdit(c)">编辑</el-button>
              <el-button size="small" text type="danger" @click="del(c)">删</el-button>
            </span>
          </div>
        </div>
      </div>
      <el-empty v-if="!list.length && !loading" description="暂无分类" />
      </div>
    </ElCard>

    <el-dialog v-model="dialog" :title="editing ? '编辑分类' : form.parent_id ? '新增二级分类' : '新增一级分类'" width="480px">
      <el-form label-position="top">
        <el-form-item label="分类名称" required><el-input v-model="form.name" maxlength="50" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="form.description" type="textarea" :rows="2" /></el-form-item>
        <el-form-item label="排序（小在前）"><el-input-number v-model="form.sort" :min="0" /></el-form-item>
        <el-checkbox v-model="form.hidden">隐藏（前台不展示）</el-checkbox>
      </el-form>
      <template #footer><el-button @click="dialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="moveDialog" title="移动产品" width="460px">
      <p class="admin-move-hint">将「{{ moveFrom?.name }}」下的 <strong>{{ moveFrom?.product_count }}</strong> 个产品整体移动到：</p>
      <el-select v-model="moveTarget" placeholder="选择目标二级分类" class="w-full"><el-option v-for="t in secondLevelTargets" :key="t.id" :value="t.id" :label="`${t.parentName} / ${t.name}`" /></el-select>
      <el-empty v-if="!secondLevelTargets.length" description="没有可选的二级分类（需先创建）" :image-size="60" />
      <template #footer><el-button @click="moveDialog = false">取消</el-button><el-button type="primary" :loading="moving" :disabled="!secondLevelTargets.length" @click="doMove">确认移动</el-button></template>
    </el-dialog>
  </div>
</template>

<style scoped>
.admin-types { display: flex; flex-direction: column; gap: 8px; }
.admin-type-group { padding: 14px 15px; background: var(--art-gray-50); border: 1px solid var(--art-card-border); border-radius: 10px; }
.admin-type-group__head { display: flex; align-items: center; gap: 9px; }
.admin-type-group__icon { width: 28px; height: 28px; display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; color: var(--theme-color-deep); background: var(--theme-color-soft); border-radius: 7px; }
.admin-type-group__head strong { color: var(--art-gray-800); font-size: 14px; }
.admin-type-group__count { color: var(--art-gray-400); font-size: 11px; }
.admin-type-group__ops { margin-left: auto; display: flex; gap: 2px; }
.admin-type-group__desc { margin: 7px 0 0; padding-left: 37px; color: var(--art-gray-500); font-size: 11px; }
.admin-type-children { margin: 10px 0 0; padding: 8px 10px; display: flex; flex-wrap: wrap; gap: 8px; background: var(--art-gray-50); border-radius: 8px; }
.admin-type-child { padding: 5px 9px; display: inline-flex; align-items: center; gap: 7px; background: var(--default-box-color); border: 1px solid var(--art-card-border); border-radius: 7px; }
.admin-type-child__name { color: var(--art-gray-700); font-size: 11px; font-weight: 550; }
.admin-type-child__count { color: var(--art-gray-400); font-size: 10px; }
.admin-type-child__ops { display: flex; gap: 2px; }
.admin-move-hint { margin: 0 0 11px; color: var(--art-gray-600); font-size: 12px; line-height: 1.7; }
@media (max-width: 640px) { .admin-type-group__head { flex-wrap: wrap; } .admin-type-group__ops { margin-left: 0; width: 100%; flex-wrap: wrap; } }
</style>