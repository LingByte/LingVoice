import axios, {
  type AxiosInstance,
  type AxiosResponse,
  type InternalAxiosRequestConfig,
} from 'axios'
import { getApiBaseURL } from '@/config/apiConfig'
import { useAuthStore } from '@/stores/authStore'

const axiosInstance: AxiosInstance = axios.create({
  baseURL: getApiBaseURL(),
  timeout: 120_000,
  headers: {
    'Content-Type': 'application/json',
  },
  withCredentials: true,
})

axiosInstance.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    let token: string | null = useAuthStore.getState().token
    if (!token) {
      try {
        token = localStorage.getItem('auth_token')
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
  (response: AxiosResponse) => response,
  (error) => {
    if (import.meta.env.DEV) {
      console.error('[axios] response error', error)
    }

    if (error.response) {
      const status = error.response.status as number
      switch (status) {
        case 401:
          useAuthStore.getState().clearUser()
          break
        default:
          break
      }
    }

    return Promise.reject(error)
  },
)

export default axiosInstance
