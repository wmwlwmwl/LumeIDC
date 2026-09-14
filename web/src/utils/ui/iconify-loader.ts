/**
 * 离线图标加载器
 *
 * 内网/离线部署下 @iconify/vue 默认会从 CDN 拉取图标数据，导致图标缺失。
 * 这里在应用启动时把使用到的图标集（scripts/build-icons.mjs 生成的多集合子集）
 * 预先注册到本地，避免运行时请求外网。
 *
 * @module utils/ui/iconify-loader
 */

import { addCollection, type IconifyJSON } from '@iconify/vue'
import iconSubsets from './icons-subset.json'

// resolveJsonModule 推断的字面量联合与 IconifyJSON 索引签名不兼容，双重断言收敛
for (const collection of iconSubsets as unknown as IconifyJSON[]) addCollection(collection)
