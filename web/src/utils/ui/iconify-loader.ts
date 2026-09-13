/**
 * 离线图标加载器
 *
 * 内网/离线部署下 @iconify/vue 默认会从 CDN 拉取图标数据，导致图标缺失。
 * 这里在应用启动时把使用到的图标集预先注册到本地，避免运行时请求外网。
 *
 * @module utils/ui/iconify-loader
 */

import { addCollection } from '@iconify/vue'
import riSubset from './icons-subset.json'

addCollection(riSubset)
