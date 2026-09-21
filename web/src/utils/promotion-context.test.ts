import { describe, expect, it } from 'vitest'
import { buildPromotionBuyPath, canUsePromotion } from './promotion-context'

describe('promotion context', () => {
  it('builds a buy path with promotion context', () => {
    expect(buildPromotionBuyPath(12, 34, 56)).toBe('/buy/12?promotion_id=34&promotion_product_id=56')
  })

  it('allows promotion actions only while ongoing', () => {
    expect(canUsePromotion('ongoing')).toBe(true)
    expect(canUsePromotion('upcoming')).toBe(false)
    expect(canUsePromotion('ended')).toBe(false)
  })
})
