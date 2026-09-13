// noVNC 未随包提供类型声明（package.json exports 直接指向 core/rfb.js），
// 这里只声明本项目用到的 RFB 接口，字段与 core/rfb.js 对齐。
declare module '@novnc/novnc' {
  export interface RFBCredentials {
    username?: string
    password?: string
    target?: string
  }

  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, urlOrChannel: string, options?: Record<string, unknown>)

    /** 画面随容器缩放显示 */
    scaleViewport: boolean
    /** 请求远端按容器尺寸调整分辨率 */
    resizeSession: boolean
    /** 只读模式（不发送键鼠事件） */
    viewOnly: boolean

    focus(options?: FocusOptions): void
    disconnect(): void
    sendCredentials(creds: RFBCredentials): void
    sendCtrlAltDel(): void
    /** keysym 为目标键位，code 为 XT scancode 键名；down 省略时按下并抬起 */
    sendKey(keysym: number, code: string | null, down?: boolean): void
    clipboardPasteFrom(text: string): void
  }
}
