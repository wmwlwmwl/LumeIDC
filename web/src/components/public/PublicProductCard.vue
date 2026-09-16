<script setup lang="ts">
  import { computed } from 'vue'
  import { ArrowRight } from '@element-plus/icons-vue'
  import { formatRichText } from '@/utils/format'

  defineOptions({ name: 'PublicProductCard' })

  interface ProductCardData {
    id: number
    name: string
    desc?: string
    monthly: string | number
    billing_cycle?: 'monthly' | 'quarterly' | 'yearly'
    cycle_label?: string
    stock: number
  }

  const props = defineProps<{
    product: ProductCardData
    to?: string
  }>()

  const target = computed(() => props.to || `/buy/${props.product.id}`)

  const soldOut = computed(() => props.product.stock === 0)
  const lowStock = computed(() => props.product.stock > 0 && props.product.stock <= 5)
  const descHtml = computed(
    () => formatRichText(props.product.desc) || '灵活可靠的云基础设施配置方案。',
  )

  const unlimited = computed(() => props.product.stock < 0)
  const stockText = computed(() => {
    if (soldOut.value) return '暂时售罄'
    if (unlimited.value) return '库存充足'
    return `库存${props.product.stock}`
  })
</script>

<template>
  <article
    class="art-card public-product-card"
    :class="{ 'is-sold': soldOut, 'is-low': lowStock }"
    :aria-disabled="soldOut || undefined"
  >
    <div class="public-product-card__head">
      <h3>{{ product.name }}</h3>
      <span
        class="public-product-card__stock"
        :class="{ 'is-unlimited': unlimited, 'is-sold': soldOut, 'is-low': lowStock }"
      >
        {{ stockText }}
      </span>
    </div>
    <div class="public-product-card__desc" v-html="descHtml" />
    <div class="public-product-card__bottom">
      <div class="public-product-card__price">
        <strong>￥{{ product.monthly }}</strong><small>/ {{ product.cycle_label || '月' }}起</small>
      </div>
      <RouterLink v-if="!soldOut" :to="target" class="public-product-card__action">
        开始配置 <el-icon><ArrowRight /></el-icon>
      </RouterLink>
      <span v-else class="public-product-card__sold">暂时不可用</span>
    </div>
  </article>
</template>

<style scoped>
  .public-product-card {
    display: flex;
    flex-direction: column;
    min-height: 215px;
    padding: 20px;
    transition: border-color 0.2s, box-shadow 0.2s, transform 0.2s;
  }

  .public-product-card:hover {
    border-color: var(--theme-color);
    box-shadow: 0 12px 28px color-mix(in srgb, var(--art-gray-900) 8%, transparent);
    transform: translateY(-2px);
  }

  .public-product-card.is-sold {
    opacity: 0.72;
  }

  .public-product-card.is-sold:hover {
    border-color: var(--art-card-border);
    box-shadow: none;
    transform: none;
  }

  .public-product-card__head {
    display: flex;
    align-items: flex-start;
    gap: 10px;
  }

  .public-product-card__head h3 {
    flex: 1;
    min-width: 0;
    margin: 0;
    overflow: hidden;
    color: var(--art-gray-900);
    font-size: 15px;
    font-weight: 600;
    line-height: 1.4;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .public-product-card__stock {
    flex-shrink: 0;
    color: var(--el-color-danger);
    font-size: 12px;
    font-weight: 600;
    line-height: 1.4;
  }

  .public-product-card__stock.is-unlimited,
  .public-product-card__stock.is-sold {
    color: var(--art-gray-500);
    font-weight: 500;
  }

  .public-product-card__desc {
    flex: 1;
    margin: 12px 0 0;
    color: var(--art-gray-500);
    font-size: 12px;
    line-height: 1.75;
    word-break: break-word;
  }

  .public-product-card__desc :deep(b),
  .public-product-card__desc :deep(strong) {
    color: var(--art-gray-700);
    font-weight: 600;
  }

  /* 上游描述里内嵌的结构化规格列表 */
  .public-product-card__desc :deep(ul),
  .public-product-card__desc :deep(ol) {
    margin: 0;
    padding: 0;
    list-style: none;
    display: grid;
    gap: 2px;
  }

  .public-product-card__desc :deep(li) {
    display: flex;
    gap: 6px;
  }

  .public-product-card__desc :deep(span) {
    display: inline-block;
    margin: 2px 4px 0 0;
    padding: 1px 7px;
    color: var(--theme-color);
    font-size: 11px;
    background: var(--theme-color-soft);
    border-radius: var(--radius-sm);
  }

  .public-product-card__bottom {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 8px 12px;
    padding-top: 14px;
    margin-top: 14px;
    border-top: 1px solid var(--art-card-border);
  }

  .public-product-card__price {
    display: flex;
    align-items: baseline;
    flex-shrink: 0;
    white-space: nowrap;
  }

  .public-product-card__price strong {
    color: var(--art-gray-900);
    font-size: 19px;
    line-height: 1;
    letter-spacing: -0.03em;
  }

  .public-product-card__price small {
    margin-left: 4px;
    color: var(--art-gray-500);
    font-size: 11px;
  }

  .public-product-card__action {
    flex-shrink: 0;
    /* 窄卡片换行后仍靠右（Catalog 三列网格卡片很窄） */
    margin-left: auto;
    display: inline-flex;
    align-items: center;
    gap: 3px;
    padding: 5px 11px;
    color: var(--theme-color);
    font-size: 12px;
    font-weight: 600;
    white-space: nowrap;
    background: var(--theme-color-soft);
    border-radius: var(--radius-sm);
    transition: color 0.16s ease, background 0.16s ease;
  }

  .public-product-card__action:hover {
    color: #fff;
    background: var(--theme-color);
  }

  .public-product-card__action .el-icon {
    transition: transform 0.16s ease;
  }

  .public-product-card__action:hover .el-icon {
    transform: translateX(3px);
  }

  .public-product-card__sold {
    flex-shrink: 0;
    margin-left: auto;
    color: var(--art-gray-400);
    font-size: 12px;
    white-space: nowrap;
  }
</style>
