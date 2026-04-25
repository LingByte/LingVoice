import axios, {
  type AxiosInstance,
  type AxiosResponse,
  type InternalAxiosRequestConfig,
} from 'axios'
import { getApiBaseURL } from '@/config/apiConfig'
import { AUTH_ACCESS_TOKEN_KEY, useAuthStore } from '@/stores/authStore'
import { redirectUnauthorizedToHome } from '@/utils/authSession'

const axiosInstance: AxiosInstance = axios.create({
  baseURL: getApiBaseURL(),
  timeout: 120_000,
  headers: {
    'Content-Type': 'application/json',
  },
  withCredentials: true,
})

// 每个请求带上 access JWT：登录/注册后 `persistAuthSession` 写入 store + localStorage，此处与后端 `CurrentUser` 解析的 Header 一致（默认 Authorization）。
axiosInstance.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    let token: string | null = useAuthStore.getState().token
    if (!token) {
      try {
        token = localStorage.getItem(AUTH_ACCESS_TOKEN_KEY)
      } catch {
        token = null
      }
    }

    const fallback = import.meta.env.VITE_AUTH_BEARER_FALLBACK

    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    } else if (fallback) {
      config.headers.Authorization = `Bearer ${fallback}`
    }

    if (config.data instanceof FormData) {
      delete config.headers['Content-Type']
    }

    if (config.params) {
      config.params = { ...config.params, _t: Date.now() }
    } else {
      config.params = { _t: Date.now() }
    }

    if (import.meta.env.DEV) {
      const base = config.baseURL ?? ''
      const url = config.url ?? ''
      console.debug('[axios]', config.method?.toUpperCase(), base + url)
    }

    return config
  },
  (error) => Promise.reject(error),
)

axiosInstance.interceptors.response.use(
  (response: AxiosResponse) => {
    const d = response.data as { code?: number; msg?: string } | undefined
    if (d && typeof d === 'object' && d.code === 401) {
      redirectUnauthorizedToHome()
      return Promise.reject(Object.assign(new Error(d.msg || '暂未登录'), { code: 401 }))
    }
    return response
  },
  (error) => {
    if (import.meta.env.DEV) {
      console.error('[axios] response error', error)
    }

    if (error.response) {
      const status = error.response.status as number
      if (status === 401) {
        redirectUnauthorizedToHome()
      }
    }

    return Promise.reject(error)
  },
)

export default axiosInstance
