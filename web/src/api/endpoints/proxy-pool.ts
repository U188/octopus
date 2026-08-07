import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'next-intl';
import { apiClient } from '../client';
import { logger } from '@/lib/logger';

export type ProxyMode = 'direct' | 'system' | 'pool' | 'inherit';
export type ProxyConfigurationType = 'single' | 'subscription';
export type ProxySubscriptionSyncStatus = 'idle' | 'success' | 'failed';

export type ProxyConfiguration = {
    id: number;
    name: string;
    url: string;
    type: ProxyConfigurationType;
    enabled: boolean;
    remark: string;
    refresh_interval_minutes: number;
    last_sync_at?: string;
    last_sync_status: ProxySubscriptionSyncStatus;
    last_sync_message: string;
    reference_count: number;
    node_count: number;
    healthy_node_count: number;
    available_node_count: number;
    quarantined_node_count: number;
    created_at: string;
    updated_at: string;
};

export type ProxySubscriptionNode = {
    id: number;
    proxy_configuration_id: number;
    url: string;
    active: boolean;
    health_status: ProxyTestHealthStatus;
    latency_ms: number;
    last_checked_at?: string;
    last_error: string;
    runtime_failure_count: number;
    quarantined_until?: string;
    last_runtime_failure_at?: string;
    last_runtime_error: string;
};

export type ProxySubscriptionSyncResult = {
    proxy_configuration_id: number;
    fetched_count: number;
    healthy_count: number;
    degraded_count: number;
    failed_count: number;
    synced_at: string;
};

export type ProxyConfigurationReferenceType = 'site' | 'site_account' | 'channel' | 'managed_channel';

export type ProxyConfigurationReference = {
    type: ProxyConfigurationReferenceType;
    site_id?: number;
    site_name?: string;
    site_archived?: boolean;
    site_account_id?: number;
    site_account_name?: string;
    channel_id?: number;
    channel_name?: string;
    managed?: boolean;
};

export type ProxyTestRequest = {
    proxy_config_id?: number | null;
    proxy_url?: string;
    use_system_proxy?: boolean;
    url?: string;
};

export type ProxyTestHealthStatus = 'healthy' | 'degraded' | 'failed';

export type ProxyTestAttemptResult = {
    attempt: number;
    success: boolean;
    status_code: number;
    duration_ms: number;
    message: string;
};

export type ProxyTestResult = {
    success: boolean;
    health_status: ProxyTestHealthStatus;
    status_code: number;
    duration_ms: number;
    average_duration_ms: number;
    attempt_count: number;
    success_count: number;
    attempts: ProxyTestAttemptResult[];
    message: string;
};

function invalidateProxyPool(queryClient: ReturnType<typeof useQueryClient>) {
    queryClient.invalidateQueries({ queryKey: ['proxy-pool'] });
    queryClient.invalidateQueries({ queryKey: ['sites', 'list'] });
    queryClient.invalidateQueries({ queryKey: ['channels', 'list'] });
}

export function useProxyConfigurationList() {
    return useQuery({
        queryKey: ['proxy-pool', 'list'],
        queryFn: async () => apiClient.get<ProxyConfiguration[]>('/api/v1/proxy-pool/list'),
        select: (data) => data ?? [],
        refetchInterval: 30000,
    });
}

export function useProxyConfigurationReferences(id: number | null, enabled = true) {
    return useQuery({
        queryKey: ['proxy-pool', 'references', id],
        queryFn: async () => apiClient.get<ProxyConfigurationReference[]>(`/api/v1/proxy-pool/references/${id}`),
        select: (data) => data ?? [],
        enabled: enabled && typeof id === 'number' && id > 0,
    });
}

export function useProxySubscriptionNodes(id: number | null, enabled = true) {
    return useQuery({
        queryKey: ['proxy-pool', 'nodes', id],
        queryFn: async () => apiClient.get<ProxySubscriptionNode[]>(`/api/v1/proxy-pool/nodes/${id}`),
        select: (data) => data ?? [],
        enabled: enabled && typeof id === 'number' && id > 0,
    });
}

export function useCreateProxyConfiguration() {
    const queryClient = useQueryClient();
    const t = useTranslations('proxyPool');
    return useMutation({
        mutationFn: async (data: Pick<ProxyConfiguration, 'name' | 'url' | 'type' | 'enabled' | 'remark' | 'refresh_interval_minutes'>) =>
            apiClient.post<ProxyConfiguration>('/api/v1/proxy-pool/create', data),
        onSuccess: () => invalidateProxyPool(queryClient),
        onError: (error) => logger.error(t('createFailed'), error),
    });
}

export function useUpdateProxyConfiguration() {
    const queryClient = useQueryClient();
    const t = useTranslations('proxyPool');
    return useMutation({
        mutationFn: async (data: Partial<Pick<ProxyConfiguration, 'name' | 'url' | 'enabled' | 'remark' | 'refresh_interval_minutes'>> & { id: number }) =>
            apiClient.post<ProxyConfiguration>('/api/v1/proxy-pool/update', data),
        onSuccess: () => invalidateProxyPool(queryClient),
        onError: (error) => logger.error(t('updateFailed'), error),
    });
}

export function useSyncProxySubscription() {
    const queryClient = useQueryClient();
    const t = useTranslations('proxyPool');
    return useMutation({
        mutationFn: async (id: number) => apiClient.post<ProxySubscriptionSyncResult>(`/api/v1/proxy-pool/sync/${id}`),
        onSettled: (_, __, id) => {
            invalidateProxyPool(queryClient);
            queryClient.invalidateQueries({ queryKey: ['proxy-pool', 'nodes', id] });
            queryClient.invalidateQueries({ queryKey: ['audit', 'logs'] });
        },
        onError: (error) => logger.error(t('syncFailed'), error),
    });
}

export function useDeleteProxyConfiguration() {
    const queryClient = useQueryClient();
    const t = useTranslations('proxyPool');
    return useMutation({
        mutationFn: async (id: number) => apiClient.delete<null>(`/api/v1/proxy-pool/delete/${id}`),
        onSuccess: () => invalidateProxyPool(queryClient),
        onError: (error) => logger.error(t('deleteFailed'), error),
    });
}

export function useTestProxyConfiguration() {
    const queryClient = useQueryClient();
    const t = useTranslations('proxyPool');
    return useMutation({
        mutationFn: async (data: ProxyTestRequest) => apiClient.post<ProxyTestResult>('/api/v1/proxy-pool/test', data),
        onError: (error) => logger.error(t('testFailed'), error),
        onSettled: () => queryClient.invalidateQueries({ queryKey: ['audit', 'logs'] }),
    });
}
