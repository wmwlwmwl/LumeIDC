/**
 * API 接口类型定义模块
 *
 * LumeIDC 使用同源 HMAC Cookie 会话与 `{ ok, ... }` 响应，不使用 Art 的
 * Bearer Token / mock 接口。这里只保留表格与状态层实际引用的通用类型，
 * 以及与会话一致的认证用户类型。
 *
 * @module types/api/api
 */

declare namespace Api {
  /** 通用类型 */
  namespace Common {
    /** 分页参数 */
    interface PaginationParams {
      /** 当前页码 */
      current: number
      /** 每页条数 */
      size: number
      /** 总条数 */
      total: number
    }

    /** 通用搜索参数 */
    type CommonSearchParams = Pick<PaginationParams, 'current' | 'size'>

    /** 分页响应基础结构 */
    interface PaginatedResponse<T = any> {
      records: T[]
      current: number
      size: number
      total: number
    }
  }

  /** 认证类型（与 /session 下发的用户结构一致） */
  namespace Auth {
    /** 会话用户信息 */
    interface UserInfo {
      id: number
      isAdmin: boolean
      email?: string
      name?: string
      phone?: string
      balance?: string
    }
  }
}
