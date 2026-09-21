export function buildPromotionBuyPath(productID: number, promotionID: number, promotionProductID: number): string {
  const params = new URLSearchParams({
    promotion_id: String(promotionID),
    promotion_product_id: String(promotionProductID),
  })
  return `/buy/${productID}?${params.toString()}`
}

export function canUsePromotion(status: string): boolean {
  return status === 'ongoing'
}
