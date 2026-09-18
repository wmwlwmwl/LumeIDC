<script setup lang="ts">
import { ArrowRight } from '@element-plus/icons-vue'
import type { PromotionLite } from '@/api/store'
import PublicContainer from '@/components/public/PublicContainer.vue'
import PublicSectionHeader from '@/components/public/PublicSectionHeader.vue'

defineOptions({ name: 'HomePromotions' })

const props = defineProps<{ promotions: PromotionLite[] }>()

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
</script>

<template>
  <section class="promos" v-if="props.promotions.length">
    <PublicContainer>
      <PublicSectionHeader title="营销活动" subtitle="折扣、抢购与专属优惠券，限时开放。">
        <template #extra>
          <RouterLink to="/promotions" class="promos__more">
            查看全部活动 <el-icon><ArrowRight /></el-icon>
          </RouterLink>
        </template>
      </PublicSectionHeader>

      <div class="promos__grid">
        <RouterLink
          v-for="p in props.promotions"
          :key="p.id"
          :to="`/promotion/${p.id}`"
          class="art-card promo-card"
        >
          <div
            class="promo-card__media"
            :style="p.banner ? { backgroundImage: `url(${p.banner})` } : {}"
          >
            <span class="promo-tag" :class="typeOf(p.type).cls">{{ typeOf(p.type).label }}</span>
            <span v-if="p.status === 'upcoming'" class="promo-badge promo-badge--soon">即将开始</span>
            <span v-else class="promo-badge promo-badge--on">进行中</span>
          </div>
          <div class="promo-card__body">
            <strong class="promo-card__title">{{ p.name }}</strong>
            <p v-if="p.description" class="promo-card__desc">{{ p.description }}</p>
            <span class="promo-card__cta">
              立即参与 <el-icon><ArrowRight /></el-icon>
            </span>
          </div>
        </RouterLink>
      </div>
    </PublicContainer>
  </section>
</template>

<style scoped>
.promos {
  padding: 40px 0 12px;
}

.promos__more {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--theme-color);
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
  border-radius: var(--radius-sm);
}

.promos__more .el-icon,
.promo-card__cta .el-icon {
  transition: transform 0.16s ease;
}

.promos__more:hover .el-icon {
  transform: translateX(3px);
}

.promos__grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
}

.promo-card {
  display: flex;
  flex-direction: column;
  padding: 0;
  overflow: hidden;
  transition: transform 0.2s ease, box-shadow 0.2s ease, border-color 0.2s ease;
}

.promo-card:hover {
  transform: translateY(-2px);
  border-color: color-mix(in srgb, var(--theme-color) 32%, var(--art-card-border));
  box-shadow: 0 14px 30px color-mix(in srgb, var(--art-gray-900) 8%, transparent);
}

.promo-card__media {
  position: relative;
  height: 104px;
  background:
    linear-gradient(135deg, var(--theme-color) 0%, color-mix(in srgb, var(--theme-color) 55%, #8b5cf6) 100%);
  background-size: cover;
  background-position: center;
}

.promo-tag {
  position: absolute;
  top: 10px;
  left: 10px;
  padding: 2px 9px;
  font-size: 11px;
  font-weight: 600;
  color: #fff;
  background: rgba(15, 23, 42, 0.55);
  border-radius: 999px;
  backdrop-filter: blur(4px);
}

.promo-badge {
  position: absolute;
  top: 10px;
  right: 10px;
  padding: 2px 9px;
  font-size: 11px;
  font-weight: 600;
  border-radius: 999px;
}

.t-discount { background: rgba(225, 29, 72, 0.9); }
.t-flash { background: rgba(234, 88, 12, 0.9); }
.t-new { background: rgba(22, 163, 74, 0.9); }
.t-full { background: rgba(217, 119, 6, 0.9); }
.t-coupon { background: rgba(37, 99, 235, 0.9); }
.t-default { background: rgba(15, 23, 42, 0.55); }

.promo-badge--on {
  color: #fff;
  background: var(--el-color-danger);
}

.promo-badge--soon {
  color: #92400e;
  background: #fef3c7;
}

.promo-card__body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
  padding: 14px 16px 16px;
}

.promo-card__title {
  overflow: hidden;
  color: var(--art-gray-900);
  font-size: 15px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.promo-card:hover .promo-card__title {
  color: var(--theme-color);
}

.promo-card__desc {
  margin: 0;
  display: -webkit-box;
  overflow: hidden;
  color: var(--art-gray-500);
  font-size: 12.5px;
  line-height: 1.6;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
}

.promo-card__cta {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-top: auto;
  padding-top: 4px;
  color: var(--theme-color);
  font-size: 13px;
  font-weight: 600;
}

.promo-card:hover .promo-card__cta .el-icon {
  transform: translateX(3px);
}

@media (max-width: 960px) {
  .promos__grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 520px) {
  .promos__grid {
    grid-template-columns: 1fr;
  }
}
</style>
