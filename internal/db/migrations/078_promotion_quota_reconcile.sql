-- 一次性对账：重算限量抢购活动的已售名额。
-- 旧实现每轮 cron 都按 invoices.status=3 全量重算并回退 sold，历史过期订单被反复扣减，
-- 使 sold 低于真实占用（活动名额被放大、可超卖）。升级时按真实占用拉回一次。
-- 占用口径与运行时一致：订单存在「已支付」账单，或存在「未支付且未过期」账单。
UPDATE promotion_quota q
   SET sold = s.cnt, updated_at = now()
  FROM (
    SELECT pp.id AS ppid, count(o.id) AS cnt
      FROM promotion_products pp
      LEFT JOIN orders o
             ON o.promo_product_id = pp.id
            AND EXISTS (
                  SELECT 1 FROM invoices i
                   WHERE i.order_id = o.id
                     AND (i.status = 1 OR (i.status = 0 AND (i.due_at IS NULL OR i.due_at > now())))
                )
     GROUP BY pp.id
  ) s
 WHERE q.promotion_product_id = s.ppid
   AND q.sold <> s.cnt;
