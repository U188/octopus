import { useQuery } from '@tanstack/react-query';
import { apiClient } from '../client';

export interface AuditLog {
    id: number;
    action: string;
    status: 'success' | 'failed' | string;
    actor: string;
    ip: string;
    user_agent: string;
    method: string;
    path: string;
    detail: string;
    error: string;
    created_at: string;
}

export function useAuditLogs(limit = 50) {
    return useQuery({
        queryKey: ['audit', 'logs', limit],
        queryFn: () => apiClient.get<AuditLog[]>('/api/v1/audit/logs', { limit }),
        refetchInterval: 30000,
    });
}
