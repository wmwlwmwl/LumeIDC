import { initializeTheme, useTheme } from '@/hooks/core/useTheme'
import { setElementThemeColor } from '@/utils/ui'
import { useSettingStore } from '@/store/modules/setting'
import { SystemThemeEnum } from '@/enums/appEnum'

/**
 * 前台主色：与后台统一为 Art 默认 #5D87FF。
 *
 * 前后台共用 setting store 的 systemThemeColor，这里仅在前台入口锁定同一主色，
 * 不修改 store，避免污染后台。
 */
export const PUBLIC_PRIMARY = '#5D87FF'

/**
 * 锁定前台根类与主色。
 *
 * Art 的 initializeTheme / setSystemTheme 会用 setAttribute('class', ...) 重置
 * <html> 的 class（会抹掉 public-app），且会用 store 主色重设 --el-color-primary*，
 * 所以每次主题变化后都要重新调用本函数。
 */
export function lockPublicTheme(): void {
  document.documentElement.classList.add('public-app')
  setElementThemeColor(PUBLIC_PRIMARY)
}

/** 前台主题初始化：应用明暗/盒模型后，再锁定 public-app 与前台主色。 */
export function installPublicTheme(): void {
  initializeTheme()
  const setting = useSettingStore()
  lockPublicTheme()
  document.documentElement.setAttribute(
    'data-box-mode',
    setting.boxBorderMode ? 'border-mode' : 'shadow-mode',
  )
}

/** 前台明暗切换：切换后重新锁定根类与主色。 */
export function togglePublicTheme(): void {
  const setting = useSettingStore()
  const { switchThemeStyles } = useTheme()
  const next = setting.systemThemeType === SystemThemeEnum.DARK ? SystemThemeEnum.LIGHT : SystemThemeEnum.DARK
  switchThemeStyles(next)
  lockPublicTheme()
}

/** 当前是否暗色。 */
export function isPublicDark(): boolean {
  return useSettingStore().systemThemeType === SystemThemeEnum.DARK
}
