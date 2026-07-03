'use client';

import { ShieldCheck } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { useAuditLogs } from '@/api/endpoints/audit';
import { Badge } from '@/components/ui/badge';
import { SettingCard } from './shared';

export function SettingAudit() {
    const t = useTranslations('setting');
    const { data: logs, isLoading } = useAuditLogs(50);

    return (
        <SettingCard icon={ShieldCheck} title={t('audit.title')}>
            <div className="space-y-2">
                {isLoading && <div className="text-sm text-muted-foreground">{t('audit.loading')}</div>}
                {!isLoading && (!logs || logs.length === 0) && (
                    <div className="rounded-xl border border-dashed border-border px-3 py-3 text-sm text-muted-foreground">
                        {t('audit.empty')}
                    </div>
                )}
                {logs?.map((item) => (
                    <div key={item.id} className="rounded-xl border border-border px-3 py-2 text-sm">
                        <div className="flex min-w-0 items-center justify-between gap-2">
                            <div className="min-w-0">
                                <div className="truncate font-medium text-card-foreground">{formatAction(item.action)}</div>
                                <div className="truncate text-xs text-muted-foreground">
                                    {formatDate(item.created_at)} · {item.actor || t('audit.unknown')} · {item.ip || t('audit.unknown')}
                                </div>
                            </div>
                            <Badge variant={item.status === 'success' ? 'outline' : 'destructive'}>
                                {item.status === 'success' ? t('audit.success') : t('audit.failed')}
                            </Badge>
                        </div>
                        {(item.detail || item.error) && (
                            <div className="mt-2 truncate text-xs text-muted-foreground">
                                {item.error || formatDetail(item.detail)}
                            </div>
                        )}
                    </div>
                ))}
            </div>
        </SettingCard>
    );
}

function formatAction(action: string) {
    return action.replaceAll('.', ' / ');
}

function formatDate(value: string) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return date.toLocaleString();
}

function formatDetail(value: string) {
    if (!value) return '';
    try {
        const parsed = JSON.parse(value) as Record<string, unknown>;
        return Object.entries(parsed)
            .map(([key, item]) => `${key}: ${formatDetailValue(item)}`)
            .join(' · ');
    } catch {
        return value;
    }
}

function formatDetailValue(value: unknown) {
    if (value === null || value === undefined) return '';
    if (typeof value === 'object') return JSON.stringify(value);
    return String(value);
}
