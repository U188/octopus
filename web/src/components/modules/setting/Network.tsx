'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Activity, Download, FlaskConical, Globe, Link, Network, Radio, Shield, ShieldCheck, X } from 'lucide-react';
import { useTestProxyConfiguration } from '@/api/endpoints/proxy-pool';
import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { Switch } from '@/components/ui/switch';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useSettingList, useSetSetting, SettingKey } from '@/api/endpoints/setting';
import { toast } from '@/components/common/Toast';
import type { ApiError } from '@/api/types';
import { SettingCard, SettingRow, useSettingField, useSettingToggle } from './shared';

// SSE 心跳间隔与流建立前首次心跳延迟统一为一个值（通常配置相同），回显以心跳间隔为准
const SSE_MIRROR_KEYS = [SettingKey.SSEPreStreamHeartbeatDelay] as const;

// enabled + defaultMode 两个 key 收敛为四个有效状态：
// enabled=false 时 mode 无意义；enabled=true 时 mode 为渠道未覆盖时的默认值
const WS_MODES = ['disabled', 'defaultOff', 'passthrough', 'transform'] as const;
type WSMode = (typeof WS_MODES)[number];

function toWSMode(enabled: boolean, rawMode: string): WSMode {
    if (!enabled) return 'disabled';
    if (rawMode === 'off') return 'defaultOff';
    if (rawMode === 'transform') return 'transform';
    return 'passthrough';
}

// 逗号/换行/中文逗号分隔的输入 → 去重后的 origin 列表
function parseCorsOrigins(raw: string): string[] {
    return Array.from(new Set(
        raw
            .split(/[,\n，]/)
            .map(item => item.trim())
            .filter(Boolean)
    ));
}

function useResponsesWSMode() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [mode, setMode] = useState<WSMode>('disabled');
    const initial = useRef<WSMode>('disabled');
    const initialized = useRef(false);

    useEffect(() => {
        if (!settings || initialized.current) return;
        const enabled = settings.find((s) => s.key === SettingKey.ResponsesWSEnabled)?.value === 'true';
        const rawMode = settings.find((s) => s.key === SettingKey.ResponsesWSDefaultMode)?.value || 'passthrough';
        const v = toWSMode(enabled, rawMode);
        queueMicrotask(() => setMode(v));
        initial.current = v;
        initialized.current = true;
    }, [settings]);

    const change = useCallback(async (v: WSMode) => {
        setMode(v);
        try {
            await setSetting.mutateAsync({
                key: SettingKey.ResponsesWSEnabled,
                value: v === 'disabled' ? 'false' : 'true',
            });
            if (v !== 'disabled') {
                await setSetting.mutateAsync({
                    key: SettingKey.ResponsesWSDefaultMode,
                    value: v === 'defaultOff' ? 'off' : v,
                });
            }
            toast.success(t('saved'));
            initial.current = v;
        } catch (error) {
            setMode(initial.current);
            toast.error(t('saveFailed'), { description: (error as unknown as ApiError)?.message });
        }
    }, [setSetting, t]);

    return { mode, change };
}

export function SettingNetwork() {
    const t = useTranslations('setting');

    const proxyUrl = useSettingField(SettingKey.ProxyURL);
    const testSystemProxy = useTestProxyConfiguration();
    const apiBaseUrl = useSettingField(SettingKey.ApiBaseUrl);
    const updateDownloadURL = useSettingField(SettingKey.UpdateDownloadURL);
    const cors = useSettingField(SettingKey.CORSAllowOrigins);
    const ipWhitelistEnabled = useSettingToggle(SettingKey.IPWhitelistEnabled);
    const ipWhitelist = useSettingField(SettingKey.IPWhitelist);
    const sseHeartbeat = useSettingField(SettingKey.SSEHeartbeatInterval, SSE_MIRROR_KEYS);
    const responsesWS = useResponsesWSMode();

    const [corsInputValue, setCorsInputValue] = useState('');

    const handleTestSystemProxy = () => {
        const currentProxyURL = proxyUrl.value.trim();
        if (!proxyUrl.isMasked && !currentProxyURL) {
            toast.error(t('proxyUrl.testRequired'));
            return;
        }
        testSystemProxy.mutate(
            proxyUrl.isMasked
                ? { use_system_proxy: true }
                : { use_system_proxy: true, proxy_url: currentProxyURL },
            {
                onSuccess: (result) => {
                    if (result.success) {
                        toast.success(t('proxyUrl.testSuccess', { statusCode: result.status_code, durationMs: result.duration_ms }));
                        return;
                    }
                    toast.error(t('proxyUrl.testFailed'), { description: result.message });
                },
                onError: (error) => toast.error(t('proxyUrl.testFailed'), { description: (error as unknown as ApiError)?.message }),
            },
        );
    };

    const corsAllowOriginsList = useMemo(() => {
        const value = cors.value.trim();
        if (!value) return [];
        if (value === '*') return ['*'];
        return parseCorsOrigins(value);
    }, [cors.value]);

    const corsAllowOriginsDisplay = useMemo(
        () => (corsAllowOriginsList.length > 0 ? corsAllowOriginsList.join(', ') : t('corsAllowOrigins.hint')),
        [corsAllowOriginsList, t]
    );

    const saveCorsAllowOrigins = (origins: string[]) => {
        const normalizedOrigins = Array.from(new Set(
            origins
                .map(origin => origin.trim())
                .filter(Boolean)
        ));
        const normalizedValue = normalizedOrigins.includes('*') ? '*' : normalizedOrigins.join(',');
        cors.commit(normalizedValue);
    };

    const handleAddCorsOrigin = () => {
        const newOrigins = parseCorsOrigins(corsInputValue);
        if (newOrigins.length === 0) return;

        if (newOrigins.includes('*')) {
            saveCorsAllowOrigins(['*']);
            setCorsInputValue('');
            return;
        }

        const base = corsAllowOriginsList.includes('*') ? [] : corsAllowOriginsList;
        const merged = Array.from(new Set([...base, ...newOrigins]));
        saveCorsAllowOrigins(merged);
        setCorsInputValue('');
    };

    const handleRemoveCorsOrigin = (originToRemove: string) => {
        const nextOrigins = corsAllowOriginsList.filter(origin => origin !== originToRemove);
        saveCorsAllowOrigins(nextOrigins);
    };

    return (
        <SettingCard icon={Network} title={t('network.title')}>
            {/* 代理地址 */}
            <SettingRow icon={Globe} label={t('proxyUrl.label')} tooltip={t('proxyUrl.description')}>
                <div className="flex w-64 max-w-full items-center gap-2">
                    <Input
                        value={proxyUrl.value}
                        onChange={(e) => proxyUrl.setValue(e.target.value)}
                        onBlur={proxyUrl.save}
                        placeholder={t('proxyUrl.placeholder')}
                        className="min-w-0 flex-1 rounded-xl"
                    />
                    <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        className="size-9 shrink-0 rounded-xl"
                        onClick={handleTestSystemProxy}
                        disabled={testSystemProxy.isPending || (!proxyUrl.isMasked && !proxyUrl.value.trim())}
                        title={testSystemProxy.isPending ? t('proxyUrl.testing') : t('proxyUrl.test')}
                        aria-label={testSystemProxy.isPending ? t('proxyUrl.testing') : t('proxyUrl.test')}
                    >
                        <FlaskConical className={`size-4 ${testSystemProxy.isPending ? 'animate-pulse' : ''}`} />
                    </Button>
                </div>
            </SettingRow>

            {/* 对外服务基础地址 */}
            <SettingRow icon={Link} label={t('apiBaseUrl.label')} tooltip={t('apiBaseUrl.description')}>
                <Input
                    value={apiBaseUrl.value}
                    onChange={(e) => apiBaseUrl.setValue(e.target.value)}
                    onBlur={apiBaseUrl.save}
                    placeholder={t('apiBaseUrl.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>

            {/* 系统更新下载加速地址 */}
            <SettingRow icon={Download} label={t('updateDownloadUrl.label')} tooltip={t('updateDownloadUrl.description')}>
                <Input
                    value={updateDownloadURL.value}
                    onChange={(e) => updateDownloadURL.setValue(e.target.value)}
                    onBlur={updateDownloadURL.save}
                    placeholder={t('updateDownloadUrl.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>

            {/* CORS 跨域白名单 */}
            <SettingRow
                icon={Shield}
                label={t('corsAllowOrigins.label')}
                tooltip={<>{t('corsAllowOrigins.hint')}<br />{t('corsAllowOrigins.example')}</>}
            >
                <Popover>
                    <PopoverTrigger asChild>
                        <button
                            type="button"
                            className="border-input focus-visible:border-ring focus-visible:ring-ring/50 w-48 min-h-9 rounded-xl border bg-transparent px-3 py-2 text-left text-sm shadow-xs transition-[color,box-shadow] outline-none focus-visible:ring-[3px]"
                            title={corsAllowOriginsDisplay}
                        >
                            <span className={`block overflow-hidden text-ellipsis whitespace-nowrap ${corsAllowOriginsList.length === 0 ? 'text-muted-foreground' : ''}`}>
                                {corsAllowOriginsDisplay}
                            </span>
                        </button>
                    </PopoverTrigger>
                    <PopoverContent className="w-72 space-y-2 rounded-3xl p-3 bg-card">
                        <Input
                            value={corsInputValue}
                            onChange={(e) => setCorsInputValue(e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === 'Enter') {
                                    e.preventDefault();
                                    handleAddCorsOrigin();
                                }
                            }}
                            placeholder={t('corsAllowOrigins.example')}
                            className="h-9 rounded-xl"
                            autoFocus
                        />
                        <div className="max-h-48 space-y-1 overflow-y-auto">
                            {corsAllowOriginsList.length > 0 && (
                                corsAllowOriginsList.map((origin) => (
                                    <div key={origin} className="flex items-center justify-between gap-2 rounded-xl border border-border/60 px-2 py-1">
                                        <span className="break-all text-xs leading-5">{origin}</span>
                                        <button
                                            type="button"
                                            onClick={() => handleRemoveCorsOrigin(origin)}
                                            className="text-muted-foreground transition-colors hover:text-destructive"
                                            aria-label={`remove ${origin}`}
                                        >
                                            <X className="size-4" />
                                        </button>
                                    </div>
                                ))
                            )}
                        </div>
                    </PopoverContent>
                </Popover>
            </SettingRow>

            {/* API 请求 IP 白名单 */}
            <SettingRow
                icon={ShieldCheck}
                label={t('ipWhitelist.enabled.label')}
                tooltip={t('ipWhitelist.enabled.description')}
            >
                <Switch checked={ipWhitelistEnabled.enabled} onCheckedChange={ipWhitelistEnabled.toggle} />
            </SettingRow>
            <SettingRow
                icon={ShieldCheck}
                label={t('ipWhitelist.list.label')}
                tooltip={t('ipWhitelist.list.description')}
            >
                <textarea
                    value={ipWhitelist.value}
                    onChange={(e) => ipWhitelist.setValue(e.target.value)}
                    onBlur={ipWhitelist.save}
                    placeholder={t('ipWhitelist.list.placeholder')}
                    rows={3}
                    className="border-input placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-ring/50 w-48 resize-y rounded-xl border bg-transparent px-3 py-2 text-sm shadow-xs outline-none focus-visible:ring-[3px]"
                />
            </SettingRow>

            {/* SSE 心跳：同一个值写入流中心跳间隔与流建立前首次心跳延迟 */}
            <SettingRow
                icon={Activity}
                label={t('sseHeartbeat.label')}
                tooltip={<>{t('sseHeartbeat.description')}<br />{t('sseHeartbeat.compatibility')}</>}
            >
                <Input
                    type="number"
                    min="0"
                    value={sseHeartbeat.value}
                    onChange={(e) => sseHeartbeat.setValue(e.target.value)}
                    onBlur={sseHeartbeat.save}
                    placeholder={t('sseHeartbeat.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>

            {/* Responses WebSocket：开关 + 默认模式收敛为单个下拉 */}
            <SettingRow icon={Radio} label={t('responsesWS.label')} tooltip={t('responsesWS.description')}>
                <Select value={responsesWS.mode} onValueChange={(v) => responsesWS.change(v as WSMode)}>
                    <SelectTrigger className="w-48 rounded-xl">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent className="rounded-xl">
                        {WS_MODES.map((m) => (
                            <SelectItem key={m} value={m} className="rounded-xl">
                                {t(`responsesWS.mode.${m}`)}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </SettingRow>
        </SettingCard>
    );
}
