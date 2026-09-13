import { watch } from 'vue'
import AppConfig, { applySiteInfo } from '@/config'
import { useSession } from '@/http/session'

/** 本地默认主色（当前 LumeIDC 品牌蓝）。 */
export const DEFAULT_THEME_COLOR = '#5d87ff'

/**
 * 将 LumeIDC /session 的站点信息同步到 Art 全局配置。
 * 站点名使用完整文字显示，不生成任何单字占位 Logo。
 */
export function installSiteConfig(): void {
  const session = useSession()
  const apply = () => applySiteInfo(session.site)
  apply()
  watch(
    () => session.site.name,
    () => apply(),
  )
}

/** 获取当前站点名称（响应式读取）。 */
export function siteName(): string {
  return AppConfig.systemInfo.name || 'LumeIDC'
}
