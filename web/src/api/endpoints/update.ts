import { useMutation, useQuery } from '@tanstack/react-query';
import { apiClient } from '../client';
import { logger } from '@/lib/logger';

export interface LatestVersionInfo {
    tag_name: string;
    published_at: string;
    body: string;
    message?: string;
}

export interface VersionInfo {
    version: string;
    commit: string;
    build_time: string;
    author: string;
    repo: string;
}

export interface UpdateStatus {
    state: 'idle' | 'running' | 'success' | 'failed';
    message?: string;
    started_at?: number;
    updated_at?: number;
}

export interface UpdatePollResult {
    outcome: 'success' | 'failed' | 'timeout';
    message?: string;
    version?: string;
}

export function useLatestVersion() {
    return useQuery({
        queryKey: ['update', 'latest'],
        queryFn: () => apiClient.get<LatestVersionInfo>('/api/v1/update'),
        refetchInterval: 5 * 60 * 1000,
        retry: 1,
    });
}

export function useVersionInfo() {
    return useQuery({
        queryKey: ['update', 'info'],
        queryFn: () => apiClient.get<VersionInfo>('/api/v1/update/info'),
        refetchInterval: 60 * 1000,
    });
}

export function useUpdateCore() {
    return useMutation({
        mutationFn: () => apiClient.post<UpdateStatus>('/api/v1/update', {}),
        onError: (error) => {
            logger.error('系统更新失败:', error);
        },
    });
}

/**
 * 触发更新后，后端异步下载/安装/重启。前端轮询判断结果：
 * - 版本号变化（重启后）或状态为 success ⇒ 更新成功（版本号变化能跨越重启，
 *   因为进程内状态会在重启后重置为 idle）。
 * - 状态为 failed ⇒ 更新失败（失败发生在重启前，进程内状态仍然有效）。
 * - 超时未决 ⇒ pending，提示用户稍后刷新确认。
 * 重启期间服务短暂不可达，fetch 抛错时继续轮询而非直接判失败。
 */
export async function pollUpdateOutcome(
    fromVersion: string,
    options?: { timeoutMs?: number; intervalMs?: number },
): Promise<UpdatePollResult> {
    const timeoutMs = options?.timeoutMs ?? 180_000;
    const intervalMs = options?.intervalMs ?? 2_500;
    const deadline = Date.now() + timeoutMs;

    while (Date.now() < deadline) {
        await new Promise((resolve) => setTimeout(resolve, intervalMs));

        // 失败发生在重启前，状态可靠；成功后进程会重启并把状态重置为 idle。
        try {
            const status = await apiClient.get<UpdateStatus>('/api/v1/update/status');
            if (status?.state === 'failed') {
                return { outcome: 'failed', message: status.message };
            }
            if (status?.state === 'success') {
                return { outcome: 'success' };
            }
        } catch {
            // 重启期间服务不可达，忽略并继续轮询
        }

        // 版本号变化是跨越重启的确定性成功信号。
        try {
            const info = await apiClient.get<VersionInfo>('/api/v1/update/info');
            if (info?.version && fromVersion && info.version !== fromVersion) {
                return { outcome: 'success', version: info.version };
            }
        } catch {
            // 忽略瞬时错误
        }
    }
    return { outcome: 'timeout' };
}

export function useRestartCore() {
    return useMutation({
        mutationFn: () => apiClient.post<string>('/api/v1/update/restart', {}),
        onError: (error) => {
            logger.error('系统重启失败:', error);
        },
    });
}
