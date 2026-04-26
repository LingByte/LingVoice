/**
 * API配置管理模块
 * 从环境变量统一读取后端地址配置
 */

interface ApiConfig {
    apiBaseURL: string
}

/**
 * 获取API基础URL
 */
export function getApiBaseURL(): string {
    return getConfig().apiBaseURL
}


/**
 * 获取API配置
 */
function getApiConfig(): ApiConfig {
    // 与后端默认 ADDR（如 :7070）对齐；跨域开发请设置 VITE_API_BASE_URL
    let apiBaseURL = import.meta.env.VITE_API_BASE_URL || 'http://127.0.0.1:7070'
    return {
        apiBaseURL,
    }
}


// 缓存配置
let cachedConfig: ApiConfig | null = null

/**
 * 获取配置（带缓存）
 */
export function getConfig(): ApiConfig {
    if (!cachedConfig) {
        cachedConfig = getApiConfig()
    }
    return cachedConfig
}