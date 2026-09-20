<template>
  <div class="promotion-page min-h-screen bg-slate-50">
    <!-- 顶部横幅 + 倒计时 -->
    <section
      class="relative overflow-hidden bg-gradient-to-br from-indigo-600 via-purple-600 to-pink-500 text-white"
    >
      <div class="absolute inset-0 opacity-20" style="background-image: radial-gradient(circle at 20% 30%, white 1px, transparent 1px); background-size: 32px 32px;"></div>
      <div class="relative max-w-6xl mx-auto px-4 py-10 sm:py-16">
        <div class="text-center">
          <h1 class="text-3xl sm:text-5xl font-bold tracking-tight drop-shadow">
            {{ promotion.name }}
          </h1>
          <p v-if="promotion.description" class="mt-3 text-white/80 text-base sm:text-lg">
            {{ promotion.description }}
          </p>
        </div>

        <!-- 倒计时 -->
        <div class="mt-8 flex justify-center">
          <div v-if="status === 'ongoing'" class="bg-white/15 backdrop-blur rounded-2xl px-6 py-4 border border-white/20">
            <div class="text-center text-white/70 text-sm mb-2">距离活动结束</div>
            <div class="flex items-center gap-2 sm:gap-3 font-mono">
              <div class="bg-white/20 rounded-lg px-3 py-2 min-w-[52px] text-center">
                <div class="text-2xl sm:text-3xl font-bold">{{ countdown.days }}</div>
                <div class="text-xs text-white/70">天</div>
              </div>
              <span class="text-2xl font-bold">:</span>
              <div class="bg-white/20 rounded-lg px-3 py-2 min-w-[52px] text-center">
                <div class="text-2xl sm:text-3xl font-bold">{{ countdown.hours }}</div>
                <div class="text-xs text-white/70">时</div>
              </div>
              <span class="text-2xl font-bold">:</span>
              <div class="bg-white/20 rounded-lg px-3 py-2 min-w-[52px] text-center">
                <div class="text-2xl sm:text-3xl font-bold">{{ countdown.minutes }}</div>
                <div class="text-xs text-white/70">分</div>
              </div>
              <span class="text-2xl font-bold">:</span>
              <div class="bg-white/20 rounded-lg px-3 py-2 min-w-[52px] text-center">
                <div class="text-2xl sm:text-3xl font-bold">{{ countdown.seconds }}</div>
                <div class="text-xs text-white/70">秒</div>
              </div>
            </div>
          </div>
          <div v-else-if="status === 'upcoming'" class="bg-white/15 backdrop-blur rounded-2xl px-8 py-6 border border-white/20 text-center">
            <div class="text-xl font-bold">活动尚未开始</div>
            <div class="text-white/70 mt-1">开始时间：{{ formatDate(promotion.starts_at) }}</div>
          </div>
          <div v-else class="bg-white/15 backdrop-blur rounded-2xl px-8 py-6 border border-white/20 text-center">
            <div class="text-xl font-bold">活动已结束</div>
            <div class="text-white/70 mt-1">感谢参与，敬请期待下次活动</div>
          </div>
        </div>
      </div>
    </section>

    <!-- 滚动公告栏 -->
    <div v-if="promotion.notice" class="bg-amber-50 border-b border-amber-200">
      <div class="max-w-6xl mx-auto px-4 py-2.5 flex items-center gap-2">
        <el-icon class="text-amber-500 flex-shrink-0"><Bell /></el-icon>
        <div class="overflow-hidden flex-1">
          <div class="animate-marquee whitespace-nowrap text-amber-700 text-sm">
            {{ promotion.notice }}
          </div>
        </div>
      </div>
    </div>

    <!-- 套餐卡片列表 -->
    <section class="max-w-6xl mx-auto px-4 py-8 sm:py-12">
      <div v-if="loading" class="text-center py-20 text-slate-400">加载中...</div>
      <div v-else-if="products.length === 0" class="text-center py-20 text-slate-400">暂无活动商品</div>
      <div v-else class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5">
        <div
          v-for="item in products"
          :key="item.product_id"
          class="bg-white rounded-2xl border border-slate-200 overflow-hidden shadow-sm hover:shadow-lg transition-shadow flex flex-col"
        >
          <!-- 标签 -->
          <div class="px-5 pt-4 flex items-center gap-2">
            <span
              v-for="tag in itemTags(item)"
              :key="tag.text"
              :class="tag.cls"
              class="text-xs px-2 py-0.5 rounded-full font-medium"
            >{{ tag.text }}</span>
          </div>
          <!-- 名称 -->
          <h3 class="px-5 pt-3 text-lg font-bold text-slate-800">{{ item.name }}</h3>
          <!-- 价格 -->
          <div class="px-5 pt-3 pb-4">
            <div class="flex items-baseline gap-2">
              <span class="text-3xl font-bold text-rose-500">¥{{ formatMoney(item.promo_price ?? item.original_price) }}</span>
              <span v-if="item.promo_price !== undefined && Number(item.original_price) > Number(item.promo_price)" class="text-slate-400 line-through text-sm">
                ¥{{ formatMoney(item.original_price) }}
              </span>
              <span class="text-slate-400 text-sm">/月起</span>
            </div>
            <div v-if="item.discount" class="text-rose-500 text-sm mt-1">立省 ¥{{ formatMoney(item.discount) }}</div>
            <div v-if="item.threshold" class="text-amber-600 text-sm mt-1">满 ¥{{ formatMoney(item.threshold) }} 减 ¥{{ formatMoney(item.reduce) }}</div>
          </div>
          <!-- 库存 -->
          <div v-if="promotion.type === 'flash_sale'" class="px-5 pb-3">
            <div class="flex items-center justify-between text-xs text-slate-500 mb-1.5">
              <span>已售 {{ item.quota_sold }} / {{ item.quota_total }}</span>
              <span :class="item.sold_out ? 'text-slate-400' : 'text-rose-500 font-medium'">
                {{ item.sold_out ? '已售罄' : `仅剩 ${item.quota_left} 台` }}
              </span>
            </div>
            <div class="h-1.5 bg-slate-100 rounded-full overflow-hidden">
              <div
                class="h-full bg-gradient-to-r from-rose-400 to-rose-500 rounded-full transition-all"
                :style="{ width: quotaPercent(item) + '%' }"
              ></div>
            </div>
          </div>
          <!-- 按钮 -->
          <div class="px-5 pb-5 mt-auto flex gap-2">
            <template v-if="promotion.type === 'coupon_giveaway'">
              <el-button
                type="warning"
                plain
                class="flex-1"
                :loading="couponClaiming"
                :disabled="couponClaimed"
                @click="claimCoupon(item)"
              >{{ couponClaimed ? '已领取' : '领取优惠券' }}</el-button>
            </template>
            <el-button
              type="primary"
              class="flex-1"
              :disabled="status !== 'ongoing' || item.sold_out"
              @click="goBuy(item)"
            >
              {{ item.sold_out ? '已售罄' : status !== 'ongoing' ? '活动未开始' : '立即购买' }}
            </el-button>
          </div>
        </div>
      </div>
    </section>

    <!-- 活动规则 -->
    <section class="max-w-6xl mx-auto px-4 pb-12">
      <div class="bg-white rounded-2xl border border-slate-200 p-6">
        <div class="flex items-center justify-between cursor-pointer" @click="rulesOpen = !rulesOpen">
          <h2 class="text-lg font-bold text-slate-800 flex items-center gap-2">
            <el-icon><Document /></el-icon>活动规则
          </h2>
          <el-icon class="text-slate-400 transition-transform" :class="{ 'rotate-180': rulesOpen }"><ArrowDown /></el-icon>
        </div>
        <div v-if="rulesOpen" class="mt-4 text-slate-600 text-sm leading-relaxed whitespace-pre-line">
          {{ promotion.rules_text || '暂无活动规则说明' }}
        </div>
      </div>
    </section>

    <!-- 底部 -->
    <footer class="border-t border-slate-200 bg-white">
      <div class="max-w-6xl mx-auto px-4 py-6 text-center text-sm text-slate-400">
        如有疑问请联系在线客服 · 本活动最终解释权归本站所有
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Bell, Document, ArrowDown } from '@element-plus/icons-vue'
import { http } from '../http'
import { formatDate as fmtDateTime, formatMoney, parseDate } from '@/utils/format'

interface PromoProduct {
  product_id: number
  name: string
  original_price: string
  promo_price?: number
  discount?: number
  threshold?: number
  reduce?: number
  quota_total?: number
  quota_sold?: number
  quota_left?: number
  sold_out?: boolean
  coupon_id?: number
}

interface Promotion {
  id: number
  name: string
  description: string
  type: string
  banner: string
  notice: string
  rules_text: string
  starts_at: string
  ends_at: string
  enabled: boolean
  limit_per_user: number
  status: string
  coupon_claimed?: boolean
}

const route = useRoute()
const router = useRouter()
const promotion = ref<Promotion>({} as Promotion)
const products = ref<PromoProduct[]>([])
const loading = ref(true)
const rulesOpen = ref(false)
const couponClaimed = ref(false)
const couponClaiming = ref(false)

const status = computed(() => promotion.value.status || 'upcoming')

// 倒计时
const countdown = ref({ days: 0, hours: 0, minutes: 0, seconds: 0 })
let timer: number | null = null

function pad(n: number) {
  return String(n).padStart(2, '0')
}

function updateCountdown() {
  const end = parseDate(promotion.value.ends_at)
  const now = Date.now()
  let diff = Math.max(0, Number.isNaN(end) ? 0 : end - now)
  countdown.value.days = Math.floor(diff / 86400000)
  diff %= 86400000
  countdown.value.hours = pad(Math.floor(diff / 3600000)) as any
  diff %= 3600000
  countdown.value.minutes = pad(Math.floor(diff / 60000)) as any
  diff %= 60000
  countdown.value.seconds = pad(Math.floor(diff / 1000)) as any
}

function formatDate(s: string) {
  return fmtDateTime(s)
}

function itemTags(item: PromoProduct) {
  const tags: { text: string; cls: string }[] = []
  const t = promotion.value.type
  if (t === 'discount') tags.push({ text: '限时折扣', cls: 'bg-rose-100 text-rose-600' })
  if (t === 'flash_sale') tags.push({ text: '限量抢购', cls: 'bg-orange-100 text-orange-600' })
  if (t === 'new_user') tags.push({ text: '新客专享', cls: 'bg-green-100 text-green-600' })
  if (t === 'full_reduction') tags.push({ text: '满减', cls: 'bg-amber-100 text-amber-600' })
  if (t === 'coupon_giveaway') tags.push({ text: '领券', cls: 'bg-blue-100 text-blue-600' })
  return tags
}

function quotaPercent(item: PromoProduct) {
  if (!item.quota_total || item.quota_total <= 0) return 0
  return Math.min(100, Math.round((item.quota_sold! / item.quota_total) * 100))
}

function goBuy(item: PromoProduct) {
  router.push(`/buy/${item.product_id}`)
}

async function claimCoupon(item: PromoProduct) {
  if (couponClaiming.value || couponClaimed.value) return
  couponClaiming.value = true
  try {
    const res = await http.post(`/promotion/${promotion.value.id}/claim`)
    if (res.ok) {
      couponClaimed.value = true
      ElMessage.success('领取成功，可在下单时使用')
    } else {
      ElMessage.error(res.msg || '领取失败')
    }
  } catch (err: unknown) {
    // 真实原因可能是未登录、网络失败或后端拒绝，统一写死「请先登录」会误导用户。
    ElMessage.error((err as Error).message || '领取失败，请先登录后重试')
  } finally {
    couponClaiming.value = false
  }
}

async function load() {
  loading.value = true
  try {
    const id = route.params.id
    const res = await http.get(`/promotion/${id}`)
    if (res.ok) {
      const promotionData = res.promotion as Promotion
      promotion.value = promotionData
      products.value = (res.products as PromoProduct[]) || []
      couponClaimed.value = Boolean(promotionData.coupon_claimed)
      updateCountdown()
    } else {
      ElMessage.error(res.msg || '加载失败')
    }
  } catch {
    ElMessage.error('活动加载失败，请检查网络后重试')
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  load()
  timer = window.setInterval(updateCountdown, 1000)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<style scoped>
@keyframes marquee {
  0% { transform: translateX(100%); }
  100% { transform: translateX(-100%); }
}
.animate-marquee {
  display: inline-block;
  animation: marquee 20s linear infinite;
}
</style>
