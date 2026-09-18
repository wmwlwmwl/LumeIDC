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

// 添加商品
function addProduct() {
  boundProducts.value.push({ product_id: 0, rules: {} })
}

// 移除商品
function removeProduct(idx: number) {
  boundProducts.value.splice(idx, 1)
}

// 加载商品列表
async function loadProducts() {
  products.value = await fetchAdminProducts()
}

// 加载活动详情（编辑模式）
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
    // 统计
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
  <div class="art-page">
    <div class="art-page-header">
      <h2 class="art-page-title">{{ isEdit ? '编辑活动' : '新增活动' }}</h2>
      <div class="flex gap-2">
        <el-button @click="router.push('/promotions')">返回</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </div>
    </div>

    <div class="art-form max-w-3xl space-y-6">
      <!-- 基本信息 -->
      <el-card shadow="never">
        <template #header><b>基本信息</b></template>
        <el-form label-width="100px">
          <el-form-item label="活动名称" required>
            <el-input v-model="form.name" placeholder="如：中秋服务器特惠" />
          </el-form-item>
          <el-form-item label="活动简介">
            <el-input v-model="form.description" type="textarea" :rows="2" placeholder="一句话描述活动" />
          </el-form-item>
          <el-form-item label="活动类型" required>
            <el-select v-model="form.type" style="width: 200px">
              <el-option v-for="o in typeOptions" :key="o.value" :label="o.label" :value="o.value" />
            </el-select>
          </el-form-item>
          <el-form-item label="开始时间" required>
            <el-date-picker v-model="form.starts_at" type="datetime" value-format="YYYY-MM-DDTHH:mm" style="width: 100%" />
          </el-form-item>
          <el-form-item label="结束时间" required>
            <el-date-picker v-model="form.ends_at" type="datetime" value-format="YYYY-MM-DDTHH:mm" style="width: 100%" />
          </el-form-item>
          <el-form-item label="限购数量">
            <el-input-number v-model="form.limit_per_user" :min="0" />
            <span class="ml-2 text-slate-400 text-sm">0 表示不限</span>
          </el-form-item>
          <el-form-item label="启用">
            <el-switch v-model="form.enabled" />
          </el-form-item>
        </el-form>
      </el-card>

      <!-- 展示配置 -->
      <el-card shadow="never">
        <template #header><b>展示配置</b></template>
        <el-form label-width="100px">
          <el-form-item label="横幅图URL">
            <el-input v-model="form.banner" placeholder="活动横幅大图地址（可选）" />
          </el-form-item>
          <el-form-item label="滚动公告">
            <el-input v-model="form.notice" placeholder="顶部滚动公告文字（可选）" />
          </el-form-item>
          <el-form-item label="活动规则">
            <el-input v-model="form.rules_text" type="textarea" :rows="5"
              placeholder="活动时间、限购说明、优惠券使用范围、交付周期、售后说明等" />
          </el-form-item>
        </el-form>
      </el-card>

      <!-- 商品绑定 -->
      <el-card shadow="never">
        <template #header>
          <div class="flex justify-between items-center">
            <b>绑定商品</b>
            <el-button size="small" type="primary" plain @click="addProduct">+ 添加商品</el-button>
          </div>
        </template>
        <div v-if="boundProducts.length === 0" class="text-slate-400 text-center py-8">暂未绑定商品</div>
        <div v-for="(item, idx) in boundProducts" :key="idx" class="border border-slate-200 rounded-lg p-4 mb-3">
          <div class="flex justify-between items-center mb-3">
            <span class="font-medium">商品 {{ idx + 1 }}</span>
            <el-button size="small" type="danger" link @click="removeProduct(idx)">移除</el-button>
          </div>
          <el-form label-width="90px">
            <el-form-item label="选择商品" required>
              <el-select v-model="item.product_id" filterable style="width: 100%">
                <el-option v-for="p in products" :key="p.id" :label="p.name" :value="p.id" />
              </el-select>
            </el-form-item>

            <!-- 限时折扣 / 限量抢购 / 新客专享：活动价 -->
            <template v-if="['discount', 'flash_sale', 'new_user'].includes(form.type)">
              <el-form-item label="活动价(月)">
                <el-input-number v-model="item.rules.price" :min="0" :precision="2" />
                <span class="ml-2 text-slate-400 text-sm">元/月</span>
              </el-form-item>
            </template>

            <!-- 限量抢购：库存 -->
            <template v-if="form.type === 'flash_sale'">
              <el-form-item label="活动库存">
                <el-input-number v-model="item.rules.stock" :min="1" />
                <span class="ml-2 text-slate-400 text-sm">台</span>
              </el-form-item>
            </template>

            <!-- 满减 -->
            <template v-if="form.type === 'full_reduction'">
              <el-form-item label="满减门槛">
                <el-input-number v-model="item.rules.threshold" :min="0" :precision="2" />
                <span class="ml-2 text-slate-400 text-sm">元</span>
              </el-form-item>
              <el-form-item label="减免金额">
                <el-input-number v-model="item.rules.reduce" :min="0" :precision="2" />
                <span class="ml-2 text-slate-400 text-sm">元</span>
              </el-form-item>
            </template>

            <!-- 优惠券发放 -->
            <template v-if="form.type === 'coupon_giveaway'">
              <el-form-item label="优惠券ID">
                <el-input-number v-model="item.rules.coupon_id" :min="1" />
                <span class="ml-2 text-slate-400 text-sm">请填写优惠折扣中已创建的优惠券 ID</span>
              </el-form-item>
            </template>
          </el-form>
        </div>
      </el-card>

      <!-- 数据统计（编辑时显示） -->
      <el-card v-if="isEdit" shadow="never">
        <template #header><b>数据统计</b></template>
        <el-row :gutter="16">
          <el-col :span="6">
            <div class="text-center p-4 bg-slate-50 rounded-lg">
              <div class="text-2xl font-bold text-slate-700">{{ stats.views }}</div>
              <div class="text-sm text-slate-400 mt-1">访问量</div>
            </div>
          </el-col>
          <el-col :span="6">
            <div class="text-center p-4 bg-slate-50 rounded-lg">
              <div class="text-2xl font-bold text-slate-700">{{ stats.claimed }}</div>
              <div class="text-sm text-slate-400 mt-1">领券数</div>
            </div>
          </el-col>
          <el-col :span="6">
            <div class="text-center p-4 bg-slate-50 rounded-lg">
              <div class="text-2xl font-bold text-slate-700">{{ stats.orders }}</div>
              <div class="text-sm text-slate-400 mt-1">下单量</div>
            </div>
          </el-col>
          <el-col :span="6">
            <div class="text-center p-4 bg-rose-50 rounded-lg">
              <div class="text-2xl font-bold text-rose-500">¥{{ Number(stats.paid_amount).toFixed(2) }}</div>
              <div class="text-sm text-slate-400 mt-1">成交金额</div>
            </div>
          </el-col>
        </el-row>
      </el-card>
    </div>
  </div>
</template>
