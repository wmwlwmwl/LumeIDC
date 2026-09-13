-- 058 清掉分类描述里的占位文案「上游导入」
-- 上游导入自动建的本地分类不再写描述（留空，前台分类页不显示），这里清掉历史占位值。
-- 只匹配我们自己的占位文案，管理员手写的分类说明不受影响。
UPDATE product_types SET description = '' WHERE description = '上游导入';
