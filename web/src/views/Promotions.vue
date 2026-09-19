<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowRight, Tickets } from '@element-plus/icons-vue'
import PublicContainer from '@/components/public/PublicContainer.vue'
import { http } from '@/http'
import { formatDate } from '@/utils/format'

defineOptions({ name: 'Promotions' })

const router = useRouter()

interface PromotionLite {
  id: number
  name: string
  description: string
  type: string
  banner: string
  starts_at: string
  ends_at: string
  status: string
}

const list = ref<PromotionLite[]>([])
const loading = ref(true)
const failed = ref(false)

const typeMap: Record<string, { label: string; cls: string }> = {
  discount: { label: '限时折扣', cls: 't-discount' },
  flash_sale: { label: '限量抢购', cls: 't-flash' },
  new_user: { label: '新客专享', cls: 't-new' },
  full_reduction: { label: '满减', cls: 't-full' },
  coupon_giveaway: { label: '领券活动', cls: 't-coupon' },
}

function typeOf(t: string) {
  return typeMap[t] || { label: '活动', cls: 't-default' }
}

async function load() {
  loading.value = true
  failed.value = false
  try {
    const res = await http.get<{ list: PromotionLite[] }>('/promotions')
    list.value = res.list || []
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}

function open(p: PromotionLite) {
  router.push(`/promotion/${p.id}`)
}

onMounted(load)
</script>

<template>
  <div class="promotions-page">
    <PublicContainer class="promotions-inner">
      <div class="art-card head-card">
        <div class="head-copy">
          <h1>营销活动</h1>
          <p>限时折扣、限量抢购、新客福利与专属优惠券，关注活动别错过优惠。</p>
        </div>
      </div>

      <div v-if="loading" class="state">
        <el-skeleton :rows="6" animated />
      </div>

      <div v-else-if="failed" class="state state--error">
        <span>活动列表加载失败。</span>
        <button type="button" class="retry" @click="load">重新加载</button>
      </div>

      <div v-else-if="!list.length" class="state state--empty">
        <el-icon class="empty-icon"><Tickets /></el-icon>
        <p>当前没有进行中的活动，敬请期待。</p>
      </div>

      <div v-else class="grid">
        <article
          v-for="p in list"
          :key="p.id"
          class="art-card promo-card"
          :class="`promo-card--${p.status}`"
          tabindex="0"
          @click="open(p)"
          @keydown.enter="open(p)"
        >
          <div class="promo-card__media" :style="p.banner ? { backgroundImage: `url(${p.banner})` } : {}">
            <span class="promo-tag" :class="typeOf(p.type).cls">{{ typeOf(p.type).label }}</span>
            <span v-if="p.status === 'upcoming'" class="promo-badge promo-badge--soon">即将开始</span>
            <span v-else class="promo-badge promo-badge--on">进行中</span>
          </div>
          <div class="promo-card__body">
            <h2 class="promo-card__title">{{ p.name }}</h2>
            <p v-if="p.description" class="promo-card__desc">{{ p.description }}</p>
            <div class="promo-card__meta">
              <time>{{ formatDate(p.starts_at) }}</time>
              <span class="dash">至</span>
              <time>{{ formatDate(p.ends_at) }}</time>
            </div>
            <div class="promo-card__foot">
              <span class="promo-card__cta">
                查看活动 <el-icon><ArrowRight /></el-icon>
              </span>
            </div>
          </div>
        </article>
      </div>
    </PublicContainer>
  </div>
</template>

<style scoped>
.promotions-page {
  padding: 28px 0 48px;
  min-height: calc(100vh - 64px);
  background: var(--art-gray-50);
}

.promotions-inner {
  max-width: 1200px;
}

.head-card {
  padding: 22px 26px;
  margin-bottom: 20px;
}

.head-copy h1 {
  margin: 0;
  color: var(--art-gray-900);
  font-size: 22px;
  font-weight: 700;
}

.head-copy p {
  margin: 6px 0 0;
  color: var(--art-gray-500);
  font-size: 13px;
}

.state {
  padding: 20px;
}

.state--empty,
.state--error {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 80px 20px;
  color: var(--art-gray-400);
  font-size: 14px;
}

.empty-icon {
  font-size: 40px;
}

.retry {
  padding: 6px 16px;
  color: var(--theme-color);
  font-size: 13px;
  font-weight: 600;
  background: transparent;
  border: 1px solid currentColor;
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 18px;
}

.promo-card {
  display: flex;
  flex-direction: column;
  padding: 0;
  overflow: hidden;
  cursor: pointer;
  transition: transform 0.2s ease, box-shadow 0.2s ease, border-color 0.2s ease;
}

.promo-card:hover,
.promo-card:focus-visible {
  outline: none;
  transform: translateY(-3px);
  border-color: color-mix(in srgb, var(--theme-color) 32%, var(--art-card-border));
  box-shadow: 0 16px 34px color-mix(in srgb, var(--art-gray-900) 10%, transparent);
}

.promo-card__media {
  position: relative;
  height: 132px;
  background:
    linear-gradient(135deg, var(--theme-color) 0%, color-mix(in srgb, var(--theme-color) 55%, #8b5cf6) 100%);
  background-size: cover;
  background-position: center;
}

.promo-tag {
  position: absolute;
  top: 12px;
  left: 12px;
  padding: 3px 10px;
  font-size: 12px;
  font-weight: 600;
  color: #fff;
  background: rgba(15, 23, 42, 0.55);
  border-radius: 999px;
  backdrop-filter: blur(4px);
}

.promo-badge {
  position: absolute;
  top: 12px;
  right: 12px;
  padding: 3px 10px;
  font-size: 12px;
  font-weight: 600;
  border-radius: 999px;
}

.promo-badge--on {
  color: #fff;
  background: var(--el-color-danger);
}

.promo-badge--soon {
  color: #92400e;
  background: #fef3c7;
}

.t-discount { background: rgba(225, 29, 72, 0.9); }
.t-flash { background: rgba(234, 88, 12, 0.9); }
.t-new { background: rgba(22, 163, 74, 0.9); }
.t-full { background: rgba(217, 119, 6, 0.9); }
.t-coupon { background: rgba(37, 99, 235, 0.9); }
.t-default { background: rgba(15, 23, 42, 0.55); }

.promo-card--upcoming .promo-card__media {
  filter: saturate(0.75);
}

.promo-card__body {
  display: flex;
  flex-direction: column;
  flex: 1;
  padding: 16px 18px 18px;
}

.promo-card__title {
  margin: 0;
  color: var(--art-gray-900);
  font-size: 16px;
  font-weight: 600;
}

.promo-card__desc {
  margin: 8px 0 0;
  display: -webkit-box;
  overflow: hidden;
  color: var(--art-gray-500);
  font-size: 13px;
  line-height: 1.7;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
}

.promo-card__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-top: 12px;
  color: var(--art-gray-400);
  font-size: 12px;
}

.promo-card__meta .dash {
  opacity: 0.7;
}

.promo-card__foot {
  display: flex;
  justify-content: flex-end;
  margin-top: auto;
  padding-top: 14px;
}

.promo-card__cta {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--theme-color);
  font-size: 13px;
  font-weight: 600;
}

.promo-card:hover .promo-card__cta .el-icon {
  transform: translateX(3px);
}

.promo-card__cta .el-icon {
  transition: transform 0.16s ease;
}

@media (max-width: 960px) {
  .grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 600px) {
  .promotions-page {
    padding: 16px 0 32px;
  }

  .grid {
    grid-template-columns: 1fr;
  }
}
</style>
