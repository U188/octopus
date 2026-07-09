'use client';

import { ExternalLink, Info, Power, RefreshCw } from 'lucide-react';
import type { ReactNode } from 'react';
import { useTranslations } from 'next-intl';
import { useLatestVersion, useRestartCore, useUpdateCore, useVersionInfo } from '@/api/endpoints/update';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { toast } from '@/components/common/Toast';
import { SettingCard } from './shared';
import { ConfirmActionButton } from './ConfirmActionButton';
import type { ApiError } from '@/api/types';

const frontendVersion = process.env.NEXT_PUBLIC_APP_VERSION || '';

export function SettingVersion() {
    const t = useTranslations('setting');
    const { data: versionInfo } = useVersionInfo();
    const { data: latestInfo, isLoading: latestLoading, refetch } = useLatestVersion();
    const updateCore = useUpdateCore();
    const restartCore = useRestartCore();

    const backendVersion = versionInfo?.version ?? t('info.unknown');
    const latestVersion = latestInfo?.tag_name ?? (latestLoading ? '...' : t('info.unknown'));
    const versionMismatch = Boolean(frontendVersion && versionInfo?.version && versionInfo.version !== frontendVersion);
    const updateAvailable = Boolean(versionInfo?.version && latestInfo?.tag_name && compareVersions(latestInfo.tag_name, versionInfo.version) > 0);

    const onUpdate = () => {
        updateCore.mutate(undefined, {
            onSuccess: () => toast.success(t('info.updateSuccess')),
            onError: (error) => toast.error(t('info.updateFailed'), { description: (error as unknown as ApiError)?.message }),
        });
    };

    const onRestart = () => {
        restartCore.mutate(undefined, {
            onSuccess: () => toast.success(t('info.restartSuccess')),
            onError: () => toast.error(t('info.restartFailed')),
        });
    };

    return (
        <SettingCard icon={Info} title={t('info.title')}>
            <div className="space-y-3 text-sm">
                <InfoRow label={t('info.currentVersion')}>
                    <div className="flex flex-wrap items-center justify-end gap-2">
                        <Badge variant="outline">{backendVersion}</Badge>
                        {versionMismatch && <Badge variant="destructive">{t('info.versionMismatch')}</Badge>}
                    </div>
                </InfoRow>
                <InfoRow label={t('info.frontendVersion')}>
                    <Badge variant="outline">{frontendVersion || t('info.unknown')}</Badge>
                </InfoRow>
                <InfoRow label={t('info.latestVersion')}>
                    <div className="flex items-center justify-end gap-2">
                        <Badge variant={updateAvailable ? 'default' : 'outline'}>{latestVersion}</Badge>
                        <Button
                            type="button"
                            variant="outline"
                            size="icon"
                            className="size-8 rounded-xl"
                            onClick={() => refetch()}
                            disabled={latestLoading}
                            title={t('info.latestVersion')}
                        >
                            <RefreshCw className="size-4" />
                        </Button>
                    </div>
                </InfoRow>
                <InfoRow label={t('info.buildTime')}>
                    <span className="truncate text-right text-muted-foreground">{formatInfo(versionInfo?.build_time, t('info.unknown'))}</span>
                </InfoRow>
                <InfoRow label={t('info.github')}>
                    {versionInfo?.repo ? (
                        <a
                            href={versionInfo.repo}
                            target="_blank"
                            rel="noreferrer"
                            className="flex min-w-0 items-center gap-1 text-right text-primary hover:underline"
                        >
                            <span className="truncate">{versionInfo.repo}</span>
                            <ExternalLink className="size-3.5 shrink-0" />
                        </a>
                    ) : (
                        <span className="text-muted-foreground">{t('info.unknown')}</span>
                    )}
                </InfoRow>
            </div>

            {versionMismatch && (
                <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                    {t('info.versionMismatchHint', { frontend: frontendVersion, backend: backendVersion })}
                </div>
            )}

            {updateAvailable && (
                <div className="space-y-2 rounded-xl border border-primary/30 bg-primary/10 px-3 py-3">
                    <div className="text-sm font-medium text-card-foreground">{t('info.newVersionAvailable')}</div>
                    <div className="text-xs text-muted-foreground">{t('info.newVersionAvailableHint')}</div>
                    <ConfirmActionButton
                        type="button"
                        className="w-full rounded-xl"
                        onConfirm={onUpdate}
                        disabled={updateCore.isPending}
                        title={t('danger.update.title')}
                        description={t('danger.update.description')}
                        confirmLabel={t('danger.confirm')}
                        cancelLabel={t('danger.cancel')}
                    >
                        {updateCore.isPending ? t('info.updating') : t('info.updateNow')}
                    </ConfirmActionButton>
                </div>
            )}

            <ConfirmActionButton
                type="button"
                className="w-full rounded-xl"
                onConfirm={onRestart}
                disabled={restartCore.isPending || updateCore.isPending}
                title={t('danger.restart.title')}
                description={t('danger.restart.description')}
                confirmLabel={t('danger.confirm')}
                cancelLabel={t('danger.cancel')}
            >
                <Power className="size-4" />
                {restartCore.isPending ? t('info.restarting') : t('info.restartNow')}
            </ConfirmActionButton>
        </SettingCard>
    );
}

function InfoRow({ label, children }: { label: string; children: ReactNode }) {
    return (
        <div className="flex min-w-0 items-center justify-between gap-4">
            <span className="shrink-0 text-muted-foreground">{label}</span>
            <div className="min-w-0">{children}</div>
        </div>
    );
}

function formatInfo(value: string | undefined, fallback: string) {
    if (!value || value === 'unknown') return fallback;
    return value;
}

function compareVersions(left: string, right: string) {
    const a = parseVersion(left);
    const b = parseVersion(right);
    if (a.valid !== b.valid) {
        return a.valid ? 1 : -1;
    }
    if (!a.valid && !b.valid) {
        return left.localeCompare(right);
    }
    const leftParts = a.parts;
    const rightParts = b.parts;
    const length = Math.max(leftParts.length, rightParts.length);
    for (let i = 0; i < length; i++) {
        const diff = (leftParts[i] ?? 0) - (rightParts[i] ?? 0);
        if (diff !== 0) return diff;
    }
    return 0;
}

function parseVersion(value: string) {
    const trimmed = value.trim();
    const normalized = trimmed.replace(/^v/i, '');
    const valid = /^\d+(?:[.-]\d+)*$/.test(normalized);
    return {
        valid,
        parts: normalized
            .trim()
            .split(/[.-]/)
            .map((part) => Number.parseInt(part, 10))
            .filter((part) => Number.isFinite(part)),
    };
}
