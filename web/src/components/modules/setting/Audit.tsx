'use client';

import { useMemo, useState } from 'react';
import { ChevronRight, ShieldCheck } from 'lucide-react';
import { useTranslations } from 'next-intl';
import { type AuditLog, useAuditLogs } from '@/api/endpoints/audit';
import { Badge } from '@/components/ui/badge';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import { SettingCard } from './shared';

export function SettingAudit() {
    const t = useTranslations('setting');
    const { data: logs, isLoading } = useAuditLogs(50);
    const [selectedLog, setSelectedLog] = useState<AuditLog | null>(null);

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
                    <button
                        key={item.id}
                        type="button"
                        className="w-full rounded-lg border border-border px-3 py-2 text-left text-sm transition-colors hover:bg-accent/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        onClick={() => setSelectedLog(item)}
                    >
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
                            <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
                        </div>
                        {(item.detail || item.error) && (
                            <div className="mt-2 truncate text-xs text-muted-foreground">
                                {item.error || formatDetail(item.detail)}
                            </div>
                        )}
                    </button>
                ))}
            </div>
            <AuditLogDetailDialog log={selectedLog} onOpenChange={(open) => !open && setSelectedLog(null)} />
        </SettingCard>
    );
}

function AuditLogDetailDialog({
    log,
    onOpenChange,
}: {
    log: AuditLog | null;
    onOpenChange: (open: boolean) => void;
}) {
    const t = useTranslations('setting');
    const detailText = useMemo(() => formatDetailBlock(log?.detail ?? ''), [log?.detail]);

    return (
        <Dialog open={!!log} onOpenChange={onOpenChange}>
            <DialogContent className="max-h-[88vh] overflow-hidden p-0 sm:max-w-3xl">
                {log && (
                    <div className="flex max-h-[88vh] flex-col">
                        <DialogHeader className="shrink-0 border-b border-border px-6 py-5">
                            <div className="flex min-w-0 items-start justify-between gap-4 pr-8">
                                <div className="min-w-0 space-y-2">
                                    <DialogTitle className="truncate">{formatAction(log.action)}</DialogTitle>
                                    <DialogDescription>
                                        {formatDate(log.created_at)} · {log.actor || t('audit.unknown')}
                                    </DialogDescription>
                                </div>
                                <Badge variant={log.status === 'success' ? 'outline' : 'destructive'} className="shrink-0">
                                    {log.status === 'success' ? t('audit.success') : t('audit.failed')}
                                </Badge>
                            </div>
                        </DialogHeader>
                        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">
                            <div className="grid gap-3 sm:grid-cols-2">
                                <AuditDetailField label={t('audit.fields.id')} value={String(log.id)} />
                                <AuditDetailField label={t('audit.fields.status')} value={log.status || t('audit.unknown')} />
                                <AuditDetailField label={t('audit.fields.actor')} value={log.actor || t('audit.unknown')} />
                                <AuditDetailField label={t('audit.fields.ip')} value={log.ip || t('audit.unknown')} />
                                <AuditDetailField label={t('audit.fields.method')} value={log.method || t('audit.unknown')} />
                                <AuditDetailField label={t('audit.fields.path')} value={log.path || t('audit.unknown')} />
                                <AuditDetailField
                                    className="sm:col-span-2"
                                    label={t('audit.fields.userAgent')}
                                    value={log.user_agent || t('audit.unknown')}
                                />
                            </div>

                            {log.error && (
                                <section className="mt-5 rounded-lg border border-destructive/30 bg-destructive/5 p-3">
                                    <div className="text-xs font-medium text-destructive">{t('audit.fields.error')}</div>
                                    <pre className="mt-2 whitespace-pre-wrap break-words text-xs text-destructive">{log.error}</pre>
                                </section>
                            )}

                            <section className="mt-5 rounded-lg border border-border p-3">
                                <div className="text-xs font-medium text-muted-foreground">{t('audit.fields.detail')}</div>
                                {detailText ? (
                                    <pre className="mt-2 max-h-[42vh] overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/60 p-3 text-xs leading-relaxed text-foreground">
                                        {detailText}
                                    </pre>
                                ) : (
                                    <div className="mt-2 text-sm text-muted-foreground">{t('audit.noDetail')}</div>
                                )}
                            </section>
                        </div>
                    </div>
                )}
            </DialogContent>
        </Dialog>
    );
}

function AuditDetailField({
    label,
    value,
    className = '',
}: {
    label: string;
    value: string;
    className?: string;
}) {
    return (
        <div className={`min-w-0 rounded-lg border border-border p-3 ${className}`}>
            <div className="text-xs font-medium text-muted-foreground">{label}</div>
            <div className="mt-1 break-words text-sm text-card-foreground">{value}</div>
        </div>
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
        const parsed = redactAuditValue(JSON.parse(value));
        if (!isRecord(parsed)) return formatDetailValue(parsed);
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

function formatDetailBlock(value: string) {
    if (!value) return '';
    try {
        const parsed = redactAuditValue(JSON.parse(value));
        return typeof parsed === 'string' ? parsed : JSON.stringify(parsed, null, 2);
    } catch {
        return value;
    }
}

function redactAuditValue(value: unknown): unknown {
    if (Array.isArray(value)) {
        return value.map((item) => redactAuditValue(item));
    }
    if (!isRecord(value)) {
        return value;
    }
    return Object.fromEntries(
        Object.entries(value).map(([key, item]) => [
            key,
            isSensitiveDetailKey(key) ? '<redacted>' : redactAuditValue(item),
        ]),
    );
}

function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isSensitiveDetailKey(key: string) {
    const normalized = key.trim().toLowerCase().replaceAll('-', '_').replaceAll(' ', '_');
    if (!normalized) return false;
    if (normalized === 'key' || normalized.endsWith('_key')) return true;
    return [
        'api_key',
        'apikey',
        'access_token',
        'refresh_token',
        'authorization',
        'bearer',
        'cookie',
        'credential',
        'password',
        'secret',
        'token',
    ].some((marker) => normalized.includes(marker));
}
