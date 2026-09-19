import { describe, expect, it } from 'vitest'
import { hasCaptchaResult } from './captcha-registry'

// 判据必须与 provider 无关：极验不产出 captcha_token，
// 曾经用固定字段名判断，导致启用极验后所有人机验证都被判成「未完成」，登录注册全被挡死。
describe('hasCaptchaResult', () => {
  it('极验回填的字段（无 captcha_token）也算已完成', () => {
    expect(
      hasCaptchaResult({ lot_number: 'a', captcha_output: 'b', pass_token: 'c', gen_time: '1' }),
    ).toBe(true)
  })

  it('vaptcha / corptcha 用 captcha_token', () => {
    expect(hasCaptchaResult({ captcha_token: 'tk', knock: 'kn' })).toBe(true)
  })

  it('未完成验证（字段被清空或全为空值）不算完成', () => {
    expect(hasCaptchaResult({})).toBe(false)
    expect(hasCaptchaResult({ captcha_token: '' })).toBe(false)
  })
})
