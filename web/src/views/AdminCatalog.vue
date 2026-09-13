<script setup lang="ts">
import { ref, computed, onMounted, watch, h } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElTag } from 'element-plus'
import { ArrowLeft, ArrowRight } from '@element-plus/icons-vue'
import type { ColumnOption } from '@/types'
import { fetchServerCatalog, importCatalogProducts, type CatalogRow } from '../admin/api'

const route = useRoute()
const router = useRouter()
const serverId = computed(() => Number(route.params.id))

const loading = ref(false)
const importing = ref(false)
const serverName = ref('')
const rows = ref<CatalogRow[]>([])
const types = ref<{ id: number; name: string }[]>([])
const errMsg = ref('')

const parentID = ref('')
const profitType = ref('0')
const profitValue = ref('0')
const requiresIdentity = ref(false)
const desc = ref('')
const selected = ref<number[]>([])

const selectable = computed(() => rows.value.filter((r) => !r.linked))

// 月付价/库存为字符串，字典序会 9>10，需按数值比较；"不限"库存视为 +∞（升序排最后）
const toNum = (s: string): number => (Number.isNaN(Number(s)) ? Infinity : Number(s))
const byNumber =
  (key: 'monthly' | 'stock') =>
  (a: CatalogRow, b: CatalogRow): number =>
    toNum(a[key]) - toNum(b[key])

const columns = ref<ColumnOption[]>([
  { type: 'selection', width: 46, selectable: (row: CatalogRow) => !row.linked },
  { prop: 'pid', label: '上游 PID', width: 110, sortable: true },
  { prop: 'name', label: '名称', minWidth: 200, sortable: true },
  { prop: 'group', label: '分组', minWidth: 140, sortable: true },
  { prop: 'monthly', label: '月付价', width: 100, sortable: true, sortMethod: byNumber('monthly'), formatter: (row: CatalogRow) => `￥${row.monthly}` },
  { prop: 'stock', label: '库存', width: 90, sortable: true, sortMethod: byNumber('stock') },
  {
    prop: 'linked',
    label: '状态',
    width: 100,
    sortable: true,
    formatter: (row: CatalogRow) =>
      h(
        ElTag,
        { type: row.linked ? 'success' : 'info', size: 'small' },
        () => (row.linked ? '已对接' : '未对接'),
      ),
  },
])

// 请求序号：丢弃慢到的过期响应，防止切换服务器/并发刷新时旧数据覆盖新数据
let reqSeq = 0

async function load(fresh = false) {
  // 路由离开时 params.id 变 undefined → NaN，直接跳过，避免发出 /servers/NaN 请求
  if (!Number.isFinite(serverId.value)) return
  const reqId = ++reqSeq
  loading.value = true
  errMsg.value = ''
  try {
    const d = await fetchServerCatalog(serverId.value, fresh)
    if (reqId !== reqSeq) return
    serverName.value = d.server.name
    rows.value = d.rows
    types.value = d.types
    errMsg.value = d.error || ''
    selected.value = []
  } catch (err: unknown) {
    if (reqId !== reqSeq) return
    ElMessage.error((err as Error).message || '拉取目录失败')
  } finally {
    if (reqId === reqSeq) loading.value = false
  }
}
onMounted(() => load())
watch(serverId, () => load())

function onSelectionChange(selection: CatalogRow[]) {
  selected.value = selection.map((r) => r.pid)
}

async function doImport() {
  if (!parentID.value) {
    ElMessage.warning('请选择导入目标一级分类')
    return
  }
  if (!selected.value.length) {
    ElMessage.warning('请勾选要导入的商品')
    return
  }
  importing.value = true
  try {
    const res = await importCatalogProducts(serverId.value, {
      parent_id: parentID.value,
      profit_type: profitType.value,
      profit_value: profitValue.value,
      requires_identity: requiresIdentity.value,
      desc: desc.value,
      pids: selected.value,
    })
    ElMessage.success(res.msg || `已导入 ${res.imported} 个产品`)
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '导入失败')
  } finally {
    importing.value = false
  }
}
</script>

<template>
  <div class="art-full-height">
    <ElCard class="art-card mb-3">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>上游商品目录</h4>
            <p>{{ serverName }} · 选择商品导入本地目录。</p>
          </div>
          <div class="flex gap-2">
            <el-button :disabled="importing" @click="load(true)">刷新目录</el-button>
            <el-button @click="router.push('/servers')">
              <el-icon><ArrowLeft /></el-icon>返回服务器
            </el-button>
          </div>
        </div>
      </template>

      <el-alert v-if="errMsg" type="error" :closable="false" show-icon :title="errMsg" class="mb-3" />

      <div class="admin-import-bar">
        <div class="admin-import-field">
          <span class="admin-import-label">导入分类</span>
          <el-select v-model="parentID" placeholder="请选择目标一级分类" style="width: 240px">
            <el-option v-for="t in types" :key="t.id" :value="String(t.id)" :label="`上游分组建为「${t.name}」下的二级分类`" />
          </el-select>
          <span class="admin-import-hint">建议先在分类管理建好一级分类</span>
        </div>

        <div class="admin-import-field">
          <span class="admin-import-label">导入利润</span>
          <el-select v-model="profitType" style="width: 120px">
            <el-option label="百分比（%）" value="0" />
            <el-option label="固定金额" value="1" />
          </el-select>
          <el-input v-model="profitValue" type="number" placeholder="如 30 或 5" style="width: 100px" />
          <span class="admin-import-hint">已有产品不覆盖</span>
        </div>

        <div class="admin-import-field">
          <span class="admin-import-label">实名认证</span>
          <el-checkbox v-model="requiresIdentity">购买需实名</el-checkbox>
          <span class="admin-import-hint">导入的产品需通过实名才能购买/续费</span>
        </div>

        <div class="admin-import-field admin-import-field-grow">
          <span class="admin-import-label">分类描述</span>
          <el-input
            v-model="desc"
            class="admin-import-desc"
            placeholder="选填，写入上游分组对应的二级分类（前台分类页展示）"
            clearable
          />
          <span class="admin-import-hint">填了则覆盖该分组描述，留空不写；产品描述仍搬上游</span>
        </div>
      </div>
    </ElCard>

    <ElCard class="art-table-card" style="margin-top: 0">
      <ArtTableHeader v-model:columns="columns" :loading="loading" @refresh="() => !importing && load(true)">
        <template #left>
          <span class="admin-import-count">已选 {{ selected.length }} 个（共 {{ selectable.length }} 个未对接）</span>
        </template>
        <template #right>
          <el-button type="primary" :loading="importing" :disabled="!selected.length || !!errMsg" @click="doImport">
            导入勾选商品 <el-icon><ArrowRight /></el-icon>
          </el-button>
        </template>
      </ArtTableHeader>

      <ArtTable
        :loading="loading"
        :data="rows"
        :columns="columns"
        empty-text="上游目录为空，请检查服务器连接或稍后重试"
        @selection-change="onSelectionChange"
      />
    </ElCard>
  </div>
</template>

<style scoped>
.admin-import-bar {
  padding: 13px 15px;
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px 24px;
  background: var(--art-gray-50);
  border: 1px solid var(--art-card-border);
  border-radius: 11px;
}
.admin-import-field {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
/* 描述输入占满所在格子剩余宽度 */
.admin-import-field-grow .admin-import-desc {
  flex: 1;
  min-width: 200px;
}
.admin-import-label {
  flex-shrink: 0;
  color: var(--art-gray-600);
  font-size: 12px;
}
.admin-import-hint {
  color: var(--art-gray-500);
  font-size: 12px;
  /* hint 允许截断换行，避免把格子撑爆 */
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.admin-import-count {
  color: var(--art-gray-500);
  font-size: 12px;
}
@media (max-width: 900px) {
  .admin-import-bar {
    grid-template-columns: 1fr;
  }
}
</style>
