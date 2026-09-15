// 国际/地区电话区号（E.164 前缀）→ 名称，用于手机号输入的国家选择。
// +86 置顶（本站主运营商/默认），其余按字母序；列表为首批常见国家/地区，可扩充。
export interface PhoneCode {
  code: string
  label: string
}

export const PHONE_CODES: PhoneCode[] = [
  { code: '+86', label: '中国' },
  { code: '+852', label: '中国香港' },
  { code: '+853', label: '中国澳门' },
  { code: '+886', label: '中国台湾' },
  { code: '+1', label: '美国/加拿大' },
  { code: '+7', label: '俄罗斯' },
  { code: '+20', label: '埃及' },
  { code: '+27', label: '南非' },
  { code: '+30', label: '希腊' },
  { code: '+31', label: '荷兰' },
  { code: '+32', label: '比利时' },
  { code: '+33', label: '法国' },
  { code: '+34', label: '西班牙' },
  { code: '+36', label: '匈牙利' },
  { code: '+39', label: '意大利' },
  { code: '+41', label: '瑞士' },
  { code: '+43', label: '奥地利' },
  { code: '+44', label: '英国' },
  { code: '+45', label: '丹麦' },
  { code: '+46', label: '瑞典' },
  { code: '+47', label: '挪威' },
  { code: '+48', label: '波兰' },
  { code: '+49', label: '德国' },
  { code: '+52', label: '墨西哥' },
  { code: '+54', label: '阿根廷' },
  { code: '+55', label: '巴西' },
  { code: '+56', label: '智利' },
  { code: '+57', label: '哥伦比亚' },
  { code: '+60', label: '马来西亚' },
  { code: '+61', label: '澳大利亚' },
  { code: '+62', label: '印度尼西亚' },
  { code: '+63', label: '菲律宾' },
  { code: '+64', label: '新西兰' },
  { code: '+65', label: '新加坡' },
  { code: '+66', label: '泰国' },
  { code: '+81', label: '日本' },
  { code: '+82', label: '韩国' },
  { code: '+84', label: '越南' },
  { code: '+90', label: '土耳其' },
  { code: '+91', label: '印度' },
  { code: '+92', label: '巴基斯坦' },
  { code: '+94', label: '斯里兰卡' },
  { code: '+95', label: '缅甸' },
  { code: '+234', label: '尼日利亚' },
  { code: '+351', label: '葡萄牙' },
  { code: '+353', label: '爱尔兰' },
  { code: '+358', label: '芬兰' },
  { code: '+380', label: '乌克兰' },
  { code: '+420', label: '捷克' },
  { code: '+855', label: '柬埔寨' },
  { code: '+856', label: '老挝' },
  { code: '+880', label: '孟加拉国' },
  { code: '+962', label: '约旦' },
  { code: '+965', label: '科威特' },
  { code: '+966', label: '沙特阿拉伯' },
  { code: '+968', label: '阿曼' },
  { code: '+971', label: '阿联酋' },
  { code: '+973', label: '巴林' },
  { code: '+974', label: '卡塔尔' },
]

// 中国大陆手机号段（与后端 mainlandPhone 一致，用于前端预校验）
export const MAINLAND_RE = /^1[3-9][0-9]{9}$/

// 按已知区号做最长前缀匹配拆分完整 E.164（正则 \d{1,3} 贪婪会把 +861… 拆成 +861，
// 故用区号表排序匹配）；未知前缀回退默认 +86（号码原样保留），便于历史数据回显。
export function splitE164(phone: string): { code: string; number: string } {
  const s = (phone || '').trim()
  if (!s) return { code: '+86', number: '' }
  const matched = PHONE_CODES.filter((c) => s.startsWith(c.code)).sort((a, b) => b.code.length - a.code.length)[0]
  if (matched) return { code: matched.code, number: s.slice(matched.code.length) }
  return { code: '+86', number: s }
}