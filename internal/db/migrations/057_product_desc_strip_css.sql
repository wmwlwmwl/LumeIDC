-- 057 清洗上游导入产品描述里泄漏的 CSS 文本
-- 上游商品描述内嵌 <style>（魔方财务商品详情即「<div><style>.config-row{…}</style><div class="config-row">…」），
-- 历史导入只去标签、不去内容，CSS 规则被当正文写进了 products.description，列表卡片直接显示一堆样式代码。
-- 描述过滤器已改为丢弃 <style>/<script> 内容（handler.safeDescriptionHTML），这里清理存量。
UPDATE products
SET description = regexp_replace(description, '^\s*(?:\.[A-Za-z0-9_-]+\s*\{[^{}]*\}\s*)+', '')
WHERE description ~ '^\s*\.[A-Za-z0-9_-]+\s*\{';
