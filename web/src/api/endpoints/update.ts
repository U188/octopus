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
        mutationFn: () => apiClient.post<string>('/api/v1/update', {}),
        onError: (error) => {
            logger.error('系统更新失败:', error);
        },
    });
}
