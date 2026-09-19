<script setup lang="ts">
import { ref, reactive, onMounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { http } from '@/http'
import { fetchAdminProducts, type AdminProduct } from '@/admin/api'

const route = useRoute()
const router = useRouter()
const isEdit = computed(() => !!route.params.id)
const saving = ref(false)
const products = ref<AdminProduct[]>([])
const stats = ref({ views: 0, claimed: 0, orders: 0, paid_amount: 0 })

const form = reactive({
  name: '',
  description: '',
  type: 'discount',
  banner: '',
  notice: '',
  rules_text: '',
  starts_at: '',
  ends_at: '',
  enabled: true,
  limit_per_user: 0,
})

// 绑定的商品列表（每个商品带规则配置）
const boundProducts = ref<Array<{
  product_id: number
  priceset_id?: number
  cycle?: string
  rules: Record<string, any>
}>>([])

const typeOptions = [
  { label: '限时折扣', value: 'discount' },
  { label: '限量抢购', value: 'flash_sale' },
  { label: '新客专享', value: 'new_user' },
  { label: '满减', value: 'full_reduction' },
  { label: '优惠券发放', value: 'coupon_giveaway' },
]

function addProduct() {
  boundProducts.value.push({ product_id: 0, rules: {} })
}

function removeProduct(idx: number) {
  boundProducts.value.splice(idx, 1)
}

async function loadProducts() {
  products.value = await fetchAdminProducts()
}

async function loadDetail() {
  const id = route.params.id
  const res = await http.get<any>(`/promotions/${id}`)
  if (res.ok) {
    const p = res.promotion
    form.name = p.name
    form.description = p.description
    form.type = p.type
    form.banner = p.banner
    form.notice = p.notice
    form.rules_text = p.rules_text
    form.starts_at = p.starts_at
    form.ends_at = p.ends_at
    form.enabled = p.enabled
    form.limit_per_user = p.limit_per_user
    boundProducts.value = (res.products || []).map((pp: any) => ({
      product_id: pp.product_id,
      priceset_id: pp.priceset_id,
      cycle: pp.cycle,
      rules: pp.rules || {},
    }))
    const s = await http.get<any>(`/promotions/${id}/stats`)
    if (s.ok) stats.value = s.stats
  }
}

async function save() {
  if (!form.name) { ElMessage.error('请输入活动名称'); return }
  if (!form.starts_at || !form.ends_at) { ElMessage.error('请选择活动时间'); return }
  if (boundProducts.value.length === 0) { ElMessage.error('请至少绑定一个商品'); return }

  saving.value = true
  try {
    const body = {
      name: form.name,
      description: form.description,
      type: form.type,
      banner: form.banner,
      notice: form.notice,
      rules_text: form.rules_text,
      starts_at: form.starts_at,
      ends_at: form.ends_at,
      enabled: form.enabled ? '1' : '0',
      limit_per_user: String(form.limit_per_user),
      products: JSON.stringify(boundProducts.value),
    }
    const url = isEdit.value ? `/promotions/${route.params.id}/save` : '/promotions/save'
    const res = await http.post<any>(url, body)
    if (res.ok) {
      ElMessage.success('保存成功')
      router.push('/promotions')
    } else {
      ElMessage.error(res.msg || '保存失败')
    }
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  loadProducts()
  if (isEdit.value) loadDetail()
})
</script>

<template>
  <div class="art-full-height" v-loading="false">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <span>{{ isEdit ? '编辑活动' : '新增活动' }}</span>
          <div class="flex gap-2">
            <el-button @click="router.push('/promotions')">返回</el-button>
            <el-button type="primary" :loading="saving" @click="save">保存</el-button>
          </div>
        </div>
      </template>

      <el-form label-width="100px">
        <div class="admin-form-grid">
          <el-form-item label="活动名称" required>
            <el-input v-model="form.name" placeholder="如：中秋服务器特惠" />
          </el-form-item>
          <el-form-item label="活动类型" required>
            <el-select v-model="form.type" class="w-full">
              <el-option v-for="o in typeOptions" :key="o.value" :label="o.label" :value="o.value" />
            </el-select>
          </el-form-item>
        </div>

        <div class="admin-form-grid">
          <el-form-item label="开始时间" required>
            <el-date-picker v-model="form.starts_at" type="datetime" value-format="YYYY-MM-DDTHH:mm" class="w-full" />
          </el-form-item>
          <el-form-item label="结束时间" required>
            <el-date-picker v-model="form.ends_at" type="datetime" value-format="YYYY-MM-DDTHH:mm" class="w-full" />
          </el-form-item>
        </div>

        <div class="admin-form-grid">
          <el-form-item label="限购数量">
            <el-input-number v-model="form.limit_per_user" :min="0" />
            <span class="ml-2 text-xs text-g-500">0 表示不限</span>
          </el-form-item>
          <el-form-item label="启用">
            <el-switch v-model="form.enabled" />
          </el-form-item>
        </div>

        <el-form-item label="活动简介">
          <el-input v-model="form.description" type="textarea" :rows="2" placeholder="一句话描述活动" />
        </el-form-item>

        <el-form-item label="活动规则">
          <el-input v-model="form.rules_text" type="textarea" :rows="5"
            placeholder="活动时间、限购说明、优惠券使用范围、交付周期、售后说明等" />
        </el-form-item>

        <div class="admin-form-grid">
          <el-form-item label="横幅图URL">
            <el-input v-model="form.banner" placeholder="活动横幅大图地址（可选）" />
          </el-form-item>
          <el-form-item label="滚动公告">
            <el-input v-model="form.notice" placeholder="顶部滚动公告文字（可选）" />
          </el-form-item>
        </div>

        <el-divider content-position="left">绑定商品</el-divider>

        <div v-if="boundProducts.length === 0" class="text-center text-g-500 py-4">暂未绑定商品，点击下方按钮添加</div>

        <div v-for="(item, idx) in boundProducts" :key="idx" class="admin-product-bound">
          <div class="flex items-center justify-between mb-3">
            <span class="font-medium">商品 {{ idx + 1 }}</span>
            <el-button size="small" type="danger" link @click="removeProduct(idx)">移除</el-button>
          </div>
          <div class="admin-form-grid">
            <el-form-item label="选择商品" required>
              <el-select v-model="item.product_id" filterable class="w-full">
                <el-option v-for="p in products" :key="p.id" :label="p.name" :value="p.id" />
              </el-select>
            </el-form-item>

            <template v-if="['discount', 'flash_sale', 'new_user'].includes(form.type)">
              <el-form-item label="活动价(月)">
                <el-input-number v-model="item.rules.price" :min="0" :precision="2" />
                <span class="ml-2 text-xs text-g-500">元/月</span>
              </el-form-item>
            </template>

            <template v-if="form.type === 'flash_sale'">
              <el-form-item label="活动库存">
                <el-input-number v-model="item.rules.stock" :min="1" />
                <span class="ml-2 text-xs text-g-500">台</span>
              </el-form-item>
            </template>

            <template v-if="form.type === 'full_reduction'">
              <el-form-item label="满减门槛">
                <el-input-number v-model="item.rules.threshold" :min="0" :precision="2" />
                <span class="ml-2 text-xs text-g-500">元</span>
              </el-form-item>
              <el-form-item label="减免金额">
                <el-input-number v-model="item.rules.reduce" :min="0" :precision="2" />
                <span class="ml-2 text-xs text-g-500">元</span>
              </el-form-item>
            </template>

            <template v-if="form.type === 'coupon_giveaway'">
              <el-form-item label="优惠券ID">
                <el-input-number v-model="item.rules.coupon_id" :min="1" />
                <span class="ml-2 text-xs text-g-500">优惠折扣中已创建的优惠券 ID</span>
              </el-form-item>
            </template>
          </div>
        </div>

        <el-button type="primary" plain @click="addProduct">+ 添加商品</el-button>
      </el-form>

      <template v-if="isEdit && stats.orders > 0">
        <el-divider content-position="left">数据统计</el-divider>
        <div class="admin-form-grid">
          <div class="stat-card">
            <div class="stat-value">{{ stats.views }}</div>
            <div class="stat-label">访问量</div>
          </div>
          <div class="stat-card">
            <div class="stat-value">{{ stats.claimed }}</div>
            <div class="stat-label">领券数</div>
          </div>
          <div class="stat-card">
            <div class="stat-value">{{ stats.orders }}</div>
            <div class="stat-label">下单量</div>
          </div>
          <div class="stat-card stat-card--highlight">
            <div class="stat-value">¥{{ Number(stats.paid_amount).toFixed(2) }}</div>
            <div class="stat-label">成交金额</div>
          </div>
        </div>
      </template>
    </ElCard>
  </div>
</template>

<style scoped>
.admin-form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}
@media (max-width: 640px) {
  .admin-form-grid {
    grid-template-columns: 1fr;
  }
}

.admin-product-bound {
  padding: 12px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 6px;
  margin-bottom: 10px;
}

.stat-card {
  padding: 16px;
  border-radius: 6px;
  text-align: center;
  background: var(--el-fill-color-light);
}
.stat-card--highlight {
  background: var(--el-color-primary-light-9);
}
.stat-value {
  font-size: 22px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}
.stat-card--highlight .stat-value {
  color: var(--el-color-primary);
}
.stat-label {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-top: 2px;
}
</style>
