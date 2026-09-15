import { initializeTheme, useTheme } from '@/hooks/core/useTheme'
import { setElementThemeColor } from '@/utils/ui'
import { StorageConfig } from '@/utils'
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

/** 前台主题初始化：恢复上次明暗偏好，应用明暗/盒模型后，再锁定 public-app 与前台主色。 */
export function installPublicTheme(): void {
  // 前台入口用的是不带持久化插件的 pinia（见 main.ts），store 里的主题设置整页刷新即丢，
  // 所以明暗偏好从 localStorage 的 sys-theme 恢复（每次切换 setGlopTheme 都会写入）。
  // 缺了这步，退出登录（整页跳转）后深色会回落到默认的「跟随系统」。
  const savedTheme = localStorage.getItem(StorageConfig.THEME_KEY)
  if (savedTheme === SystemThemeEnum.DARK || savedTheme === SystemThemeEnum.LIGHT) {
    useTheme().setSystemTheme(savedTheme)
  }
  initializeTheme()
  const setting = useSettingStore()
  lockPublicTheme()
  document.documentElement.setAttribute(
    'data-box-mode',
    setting.boxBorderMode ? 'border-mode' : 'shadow-mode',
  )
}

/**
 * 前台明暗切换：带过渡动画，切完重新锁定根类与主色。
 *
 * 动画与 Art 登录页顶栏的 themeAnimation 同源：圆心取点击位置、半径取到视窗最远角，
 * 走同一份全局样式（assets/styles/core/theme-animation.scss 的 --x/--y/--r + clip）。
 * 区别是这里在切换后补回 public-app 与前台主色（Art 那份只切主题，会把根类重置掉）。
 *
 * @param e 点击事件，用于取扩散圆心；不传或浏览器不支持 View Transition 时直接切换
 */
export function togglePublicTheme(e?: MouseEvent): void {
  const setting = useSettingStore()
  const { switchThemeStyles } = useTheme()
  const next =
    setting.systemThemeType === SystemThemeEnum.DARK ? SystemThemeEnum.LIGHT : SystemThemeEnum.DARK

  const apply = () => {
    switchThemeStyles(next)
    lockPublicTheme()
  }

  if (!e || typeof document.startViewTransition !== 'function') {
    apply()
    return
  }

  const x = e.clientX
  const y = e.clientY
  const endRadius = Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y))
  const { style } = document.documentElement
  style.setProperty('--x', `${x}px`)
  style.setProperty('--y', `${y}px`)
  style.setProperty('--r', `${endRadius}px`)
  document.startViewTransition(apply)
}

/** 当前是否暗色。 */
export function isPublicDark(): boolean {
  return useSettingStore().systemThemeType === SystemThemeEnum.DARK
}
