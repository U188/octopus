'use client';

import { useMemo, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { AlertTriangle, Calendar, Clock, Cloud, Database, Download, FileArchive, RefreshCw, RotateCcw, ScrollText, Trash2, Upload } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { toast } from '@/components/common/Toast';
import { SettingKey, useExportDB, useImportDB, useWebDAVBackupDB, useWebDAVBackupList, useWebDAVRestoreDB } from '@/api/endpoints/setting';
import { useClearLogs } from '@/api/endpoints/log';
import { SettingCard, SettingRow, SettingSection, useSettingField, useSettingToggle } from './shared';
import { ConfirmActionButton } from './ConfirmActionButton';

export function SettingData() {
    const t = useTranslations('setting');

    // 历史日志与统计持久化
    const logEnabled = useSettingToggle(SettingKey.RelayLogKeepEnabled);
    const keepPeriod = useSettingField(SettingKey.RelayLogKeepPeriod);
    const statsInterval = useSettingField(SettingKey.StatsSaveInterval);
    const clearLogs = useClearLogs();

    // 备份导出/导入
    const exportDB = useExportDB();
    const importDB = useImportDB();
    const davList = useWebDAVBackupList();
    const davBackup = useWebDAVBackupDB();
    const davRestore = useWebDAVRestoreDB();
    const autoDAVEnabled = useSettingToggle(SettingKey.WebDAVAutoBackupEnabled);
    const autoDAVURL = useSettingField(SettingKey.WebDAVAutoBackupURL);
    const autoDAVUsername = useSettingField(SettingKey.WebDAVAutoBackupUsername);
    const autoDAVPassword = useSettingField(SettingKey.WebDAVAutoBackupPassword);
    const autoDAVInterval = useSettingField(SettingKey.WebDAVAutoBackupIntervalHours);
    const autoDAVRetention = useSettingField(SettingKey.WebDAVAutoBackupRetention);

    const [includeStats, setIncludeStats] = useState(true);
    // 常规导出固定 JSON（可导入恢复）；含日志导出为 ZIP 流式归档，单独成按钮
    const [exportingKind, setExportingKind] = useState<'json' | 'logs' | null>(null);

    const [file, setFile] = useState<File | null>(null);
    const fileInputRef = useRef<HTMLInputElement | null>(null);
    const [davURL, setDavURL] = useState('');
    const [davUsername, setDavUsername] = useState('');
    const [davPassword, setDavPassword] = useState('');
    const [davFilename, setDavFilename] = useState('');
    const [selectedDavFile, setSelectedDavFile] = useState('');

    const davCredentials = useMemo(() => ({
        url: davURL.trim(),
        username: davUsername.trim(),
        password: davPassword,
    }), [davPassword, davURL, davUsername]);

    const rowsAffected = importDB.data?.rows_affected ?? null;
    const rowsAffectedList = useMemo(() => {
        if (!rowsAffected) return [];
        return Object.entries(rowsAffected)
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([k, v]) => ({ table: k, count: v }));
    }, [rowsAffected]);

    const handleClearLogs = () => {
        clearLogs.mutate(undefined, {
            onSuccess: () => toast.success(t('log.clearSuccess')),
            onError: () => toast.error(t('log.clearFailed')),
        });
    };

    const onImport = async () => {
        if (!file) {
            toast.error(t('backup.import.noFile'));
            return;
        }
        // accept 属性只约束选择器默认过滤，仍可手动选任意文件，导入前再校验一次
        if (file.type !== 'application/json' && !file.name.toLowerCase().endsWith('.json')) {
            toast.error(t('backup.import.invalidFileType'));
            if (fileInputRef.current) fileInputRef.current.value = '';
            setFile(null);
            return;
        }
        try {
            await importDB.mutateAsync(file);
            toast.success(t('backup.import.success'));
            if (fileInputRef.current) fileInputRef.current.value = '';
            setFile(null);
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('backup.import.failed'));
        }
    };

    const onExport = async (kind: 'json' | 'logs') => {
        setExportingKind(kind);
        try {
            await exportDB.mutateAsync(kind === 'logs'
                ? { include_logs: true, include_stats: includeStats, format: 'zip' }
                : { include_logs: false, include_stats: includeStats, format: 'json' });
            toast.success(t('backup.export.success'));
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('backup.export.failed'));
        } finally {
            setExportingKind(null);
        }
    };

    const refreshDAVBackups = async () => {
        try {
            const files = await davList.mutateAsync(davCredentials);
            if (!selectedDavFile && files[0]) {
                setSelectedDavFile(files[0].name);
            }
            toast.success(t('backup.dav.listSuccess'));
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('backup.dav.listFailed'));
        }
    };

    const backupToDAV = async () => {
        try {
            const result = await davBackup.mutateAsync({
                ...davCredentials,
                filename: davFilename.trim() || undefined,
            });
            setSelectedDavFile(result.filename);
            toast.success(t('backup.dav.backupSuccess'));
            try {
                await davList.mutateAsync(davCredentials);
            } catch {
                // Backup already succeeded; the next manual refresh can recover the list.
            }
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('backup.dav.backupFailed'));
        }
    };

    const restoreFromDAV = async () => {
        if (!selectedDavFile) {
            toast.error(t('backup.dav.noFile'));
            return;
        }
        try {
            await davRestore.mutateAsync({
                ...davCredentials,
                filename: selectedDavFile,
            });
            toast.success(t('backup.dav.restoreSuccess'));
        } catch (e) {
            toast.error(e instanceof Error ? e.message : t('backup.dav.restoreFailed'));
        }
    };

    return (
        <SettingCard icon={Database} title={t('data.title')}>
            {/* 统计保存周期 */}
            <SettingRow icon={Clock} label={t('statsSaveInterval.label')}>
                <Input
                    type="number"
                    value={statsInterval.value}
                    onChange={(e) => statsInterval.setValue(e.target.value)}
                    onBlur={statsInterval.save}
                    placeholder={t('statsSaveInterval.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>

            {/* 历史日志 */}
            <SettingSection title={t('log.title')} />
            <SettingRow icon={ScrollText} label={t('log.enabled.label')}>
                <Switch checked={logEnabled.enabled} onCheckedChange={logEnabled.toggle} />
            </SettingRow>
            <SettingRow icon={Calendar} label={t('log.keepPeriod.label')}>
                <Input
                    type="number"
                    value={keepPeriod.value}
                    onChange={(e) => keepPeriod.setValue(e.target.value)}
                    onBlur={keepPeriod.save}
                    placeholder={t('log.keepPeriod.placeholder')}
                    className="w-48 rounded-xl"
                    disabled={!logEnabled.enabled}
                />
            </SettingRow>
            <SettingRow icon={Trash2} label={t('log.clear.label')}>
                <ConfirmActionButton
                    variant="destructive"
                    size="sm"
                    onConfirm={handleClearLogs}
                    disabled={clearLogs.isPending}
                    className="rounded-xl"
                    title={t('danger.clearLogs.title')}
                    description={t('danger.clearLogs.description')}
                    confirmLabel={t('danger.confirm')}
                    cancelLabel={t('danger.cancel')}
                >
                    {clearLogs.isPending ? t('log.clear.clearing') : t('log.clear.button')}
                </ConfirmActionButton>
            </SettingRow>

            {/* 备份导出 */}
            <SettingSection title={t('backup.export.title')} />
            <div className="space-y-3">
                <div className="flex items-center justify-between gap-4">
                    <div className="text-sm text-muted-foreground">{t('backup.export.includeStats')}</div>
                    <Switch checked={includeStats} onCheckedChange={setIncludeStats} />
                </div>

                <Button
                    type="button"
                    variant="outline"
                    className="w-full rounded-xl"
                    onClick={() => onExport('json')}
                    disabled={exportDB.isPending}
                >
                    <Download className="size-4" />
                    {exportingKind === 'json' ? t('backup.export.exporting') : t('backup.export.button')}
                </Button>

                {/* 含日志归档：数据量大，ZIP 流式写入，仅供留存，无法导入恢复 */}
                <Button
                    type="button"
                    variant="outline"
                    className="w-full rounded-xl"
                    onClick={() => onExport('logs')}
                    disabled={exportDB.isPending}
                >
                    <FileArchive className="size-4" />
                    {exportingKind === 'logs' ? t('backup.export.exporting') : t('backup.export.withLogsButton')}
                </Button>
                <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
                    <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-destructive" />
                    {t('backup.export.withLogsWarning')}
                </p>
            </div>

            {/* 备份导入 */}
            <SettingSection title={t('backup.import.title')} />
            <div className="space-y-3">
                <Input
                    ref={fileInputRef}
                    type="file"
                    accept="application/json,.json"
                    onChange={(e) => setFile(e.target.files?.[0] ?? null)}
                    className="rounded-xl"
                />

                <ConfirmActionButton
                    type="button"
                    variant="destructive"
                    className="w-full rounded-xl"
                    onConfirm={onImport}
                    disabled={importDB.isPending}
                    title={t('danger.import.title')}
                    description={t('danger.import.description')}
                    confirmLabel={t('danger.confirm')}
                    cancelLabel={t('danger.cancel')}
                >
                    <Upload className="size-4" />
                    {importDB.isPending ? t('backup.import.importing') : t('backup.import.button')}
                </ConfirmActionButton>

                {rowsAffectedList.length > 0 && (
                    <div className="mt-2 space-y-1">
                        <div className="text-xs font-semibold text-card-foreground">{t('backup.import.result')}</div>
                        <div className="grid grid-cols-2 gap-1 text-xs text-muted-foreground">
                            {rowsAffectedList.map((it) => (
                                <div key={it.table} className="flex justify-between gap-2">
                                    <span className="truncate">{it.table}</span>
                                    <span className="tabular-nums">{it.count}</span>
                                </div>
                            ))}
                        </div>
                    </div>
                )}
            </div>

            {/* WebDAV 轻量备份 */}
            <SettingSection title={t('backup.dav.title')} />
            <div className="space-y-3">
                <div className="grid gap-3 sm:grid-cols-3">
                    <Input
                        value={davURL}
                        onChange={(e) => setDavURL(e.target.value)}
                        placeholder={t('backup.dav.url')}
                        className="rounded-xl sm:col-span-3"
                    />
                    <Input
                        value={davUsername}
                        onChange={(e) => setDavUsername(e.target.value)}
                        placeholder={t('backup.dav.username')}
                        className="rounded-xl"
                    />
                    <Input
                        type="password"
                        value={davPassword}
                        onChange={(e) => setDavPassword(e.target.value)}
                        placeholder={t('backup.dav.password')}
                        className="rounded-xl"
                    />
                    <Input
                        value={davFilename}
                        onChange={(e) => setDavFilename(e.target.value)}
                        placeholder={t('backup.dav.filename')}
                        className="rounded-xl"
                    />
                </div>

                <div className="grid gap-2 sm:grid-cols-2">
                    <Button
                        type="button"
                        variant="outline"
                        className="rounded-xl"
                        onClick={refreshDAVBackups}
                        disabled={davList.isPending}
                    >
                        <RefreshCw className="size-4" />
                        {davList.isPending ? t('backup.dav.listing') : t('backup.dav.list')}
                    </Button>
                    <Button
                        type="button"
                        variant="outline"
                        className="rounded-xl"
                        onClick={backupToDAV}
                        disabled={davBackup.isPending || davList.isPending}
                    >
                        <Cloud className="size-4" />
                        {davBackup.isPending ? t('backup.dav.backingUp') : t('backup.dav.backup')}
                    </Button>
                </div>

                {(davList.data?.length ?? 0) > 0 && (
                    <div className="space-y-2">
                        <select
                            value={selectedDavFile}
                            onChange={(e) => setSelectedDavFile(e.target.value)}
                            className="h-10 w-full rounded-xl border border-input bg-background px-3 text-sm"
                        >
                            {davList.data?.map((item) => (
                                <option key={item.name} value={item.name}>
                                    {item.name} · {formatBytes(item.size)}{item.modified_at ? ` · ${formatDate(item.modified_at)}` : ''}
                                </option>
                            ))}
                        </select>
                        <ConfirmActionButton
                            type="button"
                            variant="destructive"
                            className="w-full rounded-xl"
                            onConfirm={restoreFromDAV}
                            disabled={davRestore.isPending}
                            title={t('danger.davRestore.title')}
                            description={t('danger.davRestore.description')}
                            confirmLabel={t('danger.confirm')}
                            cancelLabel={t('danger.cancel')}
                        >
                            <RotateCcw className="size-4" />
                            {davRestore.isPending ? t('backup.dav.restoring') : t('backup.dav.restore')}
                        </ConfirmActionButton>
                    </div>
                )}

                {davList.data && davList.data.length === 0 && (
                    <div className="rounded-xl border border-dashed border-border px-3 py-2 text-sm text-muted-foreground">
                        {t('backup.dav.empty')}
                    </div>
                )}

                <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
                    <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-destructive" />
                    {t('backup.dav.restoreWarning')}
                </p>
            </div>

            <SettingSection title={t('backup.autoDav.title')} />
            <div className="space-y-3">
                <SettingRow label={t('backup.autoDav.enabled')}>
                    <Switch checked={autoDAVEnabled.enabled} onCheckedChange={autoDAVEnabled.toggle} />
                </SettingRow>
                <div className="grid gap-3 sm:grid-cols-2">
                    <Input
                        value={autoDAVURL.value}
                        onChange={(e) => autoDAVURL.setValue(e.target.value)}
                        onBlur={autoDAVURL.save}
                        placeholder={t('backup.autoDav.url')}
                        className="rounded-xl sm:col-span-2"
                    />
                    <Input
                        value={autoDAVUsername.value}
                        onChange={(e) => autoDAVUsername.setValue(e.target.value)}
                        onBlur={autoDAVUsername.save}
                        placeholder={t('backup.autoDav.username')}
                        className="rounded-xl"
                    />
                    <Input
                        type="password"
                        value={autoDAVPassword.value}
                        onChange={(e) => autoDAVPassword.setValue(e.target.value)}
                        onBlur={autoDAVPassword.save}
                        placeholder={t('backup.autoDav.password')}
                        className="rounded-xl"
                    />
                    <Input
                        type="number"
                        min={1}
                        value={autoDAVInterval.value}
                        onChange={(e) => autoDAVInterval.setValue(e.target.value)}
                        onBlur={autoDAVInterval.save}
                        placeholder={t('backup.autoDav.interval')}
                        className="rounded-xl"
                    />
                    <Input
                        type="number"
                        min={1}
                        max={100}
                        value={autoDAVRetention.value}
                        onChange={(e) => autoDAVRetention.setValue(e.target.value)}
                        onBlur={autoDAVRetention.save}
                        placeholder={t('backup.autoDav.retention')}
                        className="rounded-xl"
                    />
                </div>
                <p className="text-xs text-muted-foreground">{t('backup.autoDav.hint')}</p>
            </div>
        </SettingCard>
    );
}

function formatBytes(size: number) {
    if (!Number.isFinite(size) || size <= 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB'];
    let value = size;
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
        value /= 1024;
        unit++;
    }
    return `${value.toFixed(value >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function formatDate(value: string) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return date.toLocaleString();
}
