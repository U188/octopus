/**
 * 统一的日志工具
 * 在生产环境中过滤敏感的日志输出
 */

const isDevelopment = process.env.NODE_ENV !== 'production';

export const logger = {
    /**
     * 普通日志 - 仅在开发环境输出
     */
    log: (...args: unknown[]) => {
        if (isDevelopment) {
            console.log(...args);
        }
    },

    /**
     * 错误日志 - 在所有环境输出
     */
    error: (...args: unknown[]) => {
        console.error(...args);
    },

    /**
     * 警告日志 - 在所有环境输出
     */
    warn: (...args: unknown[]) => {
        console.warn(...args);
    },

    /**
     * 调试日志 - 仅在开发环境输出
     */
    debug: (...args: unknown[]) => {
        if (isDevelopment) {
            console.debug(...args);
        }
    },

    /**
     * 信息日志 - 仅在开发环境输出
     */
    info: (...args: unknown[]) => {
        if (isDevelopment) {
            console.info(...args);
        }
    },
};
