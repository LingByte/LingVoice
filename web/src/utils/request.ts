import type { InternalAxiosRequestConfig, AxiosResponse } from 'axios'
import axiosInstance from '@/utils/axios'

export interface ApiResponse<T = unknown> {
  code: number
  msg: string
  data: T
}

export async function request<T = unknown>(
  url: string,
  options: Partial<InternalAxiosRequestConfig> = {},
): Promise<ApiResponse<T>> {
  try {
    const response: AxiosResponse<ApiResponse<T>> = await axiosInstance({
      url,
      ...options,
    })
    return response.data
  } catch (error: unknown) {
    const err = error as {
      response?: { data?: unknown; status?: number }
      code?: string
      message?: string
    }

    if (err.response?.data) {
      const errorData = err.response.data as Record<string, unknown>
      if (typeof errorData.error === 'string') {
        throw {
          code: err.response.status ?? 500,
          msg: errorData.error,
          data: null,
        }
      }
      if (typeof errorData.code === 'number') {
        throw errorData
      }
      throw {
        code: err.response.status ?? 500,
        msg:
          (errorData.message as string) ||
          (errorData.msg as string) ||
          (errorData.error as string) ||
          '请求失败',
        data: null,
      }
    }

    let errorMessage = '网络请求失败'
    if (err.code === 'ERR_CONNECTION_REFUSED') {
      errorMessage = '无法连接到服务器，请检查后端是否已启动'
    } else if (err.code === 'ECONNABORTED') {
      errorMessage = '请求超时，请稍后重试'
    } else if (err.message) {
      errorMessage = err.message
    }

    throw {
      code: -1,
      msg: errorMessage,
      data: null,
    }
  }
}

export const get = <T = unknown>(
  url: string,
  config?: Partial<InternalAxiosRequestConfig>,
): Promise<ApiResponse<T>> => request<T>(url, { ...config, method: 'GET' })

export const post = <T = unknown>(
  url: string,
  data?: unknown,
  config?: Partial<InternalAxiosRequestConfig>,
): Promise<ApiResponse<T>> =>
  request<T>(url, { ...config, method: 'POST', data })

export const put = <T = unknown>(
  url: string,
  data?: unknown,
  config?: Partial<InternalAxiosRequestConfig>,
): Promise<ApiResponse<T>> =>
  request<T>(url, { ...config, method: 'PUT', data })

export const del = <T = unknown>(
  url: string,
  config?: Partial<InternalAxiosRequestConfig>,
): Promise<ApiResponse<T>> => request<T>(url, { ...config, method: 'DELETE' })

export const patch = <T = unknown>(
  url: string,
  data?: unknown,
  config?: Partial<InternalAxiosRequestConfig>,
): Promise<ApiResponse<T>> =>
  request<T>(url, { ...config, method: 'PATCH', data })

export default request
