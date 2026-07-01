'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Bot, Clock, Globe, KeyRound, Link, Send, ShieldCheck } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { SettingKey, useSettingList, useSetSetting } from '@/api/endpoints/setting';
import { toast } from '@/components/common/Toast';
import type { ApiError } from '@/api/types';
import { SettingCard, SettingRow, useSettingField, useSettingToggle } from './shared';

const PROXY_MODES = ['direct', 'system', 'custom'] as const;
type ProxyMode = (typeof PROXY_MODES)[number];

function useTelegramProxyMode() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();
    const [mode, setMode] = useState<ProxyMode>('direct');
    const initial = useRef<ProxyMode>('direct');
    const initialized = useRef(false);

    useEffect(() => {
        if (!settings || initialized.current) return;
        const raw = settings.find((s) => s.key === SettingKey.TelegramBotProxyMode)?.value;
        const v = PROXY_MODES.includes(raw as ProxyMode) ? raw as ProxyMode : 'direct';
        queueMicrotask(() => setMode(v));
        initial.current = v;
        initialized.current = true;
    }, [settings]);

    const change = useCallback((v: ProxyMode) => {
        setMode(v);
        setSetting.mutate(
            { key: SettingKey.TelegramBotProxyMode, value: v },
            {
                onSuccess: () => {
                    toast.success(t('saved'));
                    initial.current = v;
                },
                onError: (error) => {
                    setMode(initial.current);
                    toast.error(t('saveFailed'), { description: (error as unknown as ApiError)?.message });
                },
            }
        );
    }, [setSetting, t]);

    return { mode, change };
}

export function SettingTelegram() {
    const t = useTranslations('setting');
    const enabled = useSettingToggle(SettingKey.TelegramBotEnabled);
    const token = useSettingField(SettingKey.TelegramBotToken);
    const adminIDs = useSettingField(SettingKey.TelegramBotAdminIDs);
    const apiBaseURL = useSettingField(SettingKey.TelegramBotAPIBaseURL);
    const proxyURL = useSettingField(SettingKey.TelegramBotProxyURL);
    const pollInterval = useSettingField(SettingKey.TelegramBotPollInterval);
    const proxyMode = useTelegramProxyMode();

    return (
        <SettingCard icon={Bot} title={t('telegramBot.title')} tooltip={t('telegramBot.description')}>
            <SettingRow icon={Send} label={t('telegramBot.enabled.label')} tooltip={t('telegramBot.enabled.description')}>
                <Switch checked={enabled.enabled} onCheckedChange={enabled.toggle} />
            </SettingRow>

            <SettingRow icon={KeyRound} label={t('telegramBot.token.label')} tooltip={t('telegramBot.token.description')}>
                <Input
                    type="password"
                    value={token.value}
                    onChange={(e) => token.setValue(e.target.value)}
                    onBlur={token.save}
                    placeholder={t('telegramBot.token.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>

            <SettingRow icon={ShieldCheck} label={t('telegramBot.adminIDs.label')} tooltip={t('telegramBot.adminIDs.description')}>
                <Input
                    value={adminIDs.value}
                    onChange={(e) => adminIDs.setValue(e.target.value)}
                    onBlur={adminIDs.save}
                    placeholder={t('telegramBot.adminIDs.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>

            <SettingRow icon={Link} label={t('telegramBot.apiBaseURL.label')} tooltip={t('telegramBot.apiBaseURL.description')}>
                <Input
                    value={apiBaseURL.value}
                    onChange={(e) => apiBaseURL.setValue(e.target.value)}
                    onBlur={apiBaseURL.save}
                    placeholder={t('telegramBot.apiBaseURL.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>

            <SettingRow icon={Globe} label={t('telegramBot.proxyMode.label')} tooltip={t('telegramBot.proxyMode.description')}>
                <Select value={proxyMode.mode} onValueChange={(v) => proxyMode.change(v as ProxyMode)}>
                    <SelectTrigger className="w-48 rounded-xl">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent className="rounded-xl">
                        {PROXY_MODES.map((m) => (
                            <SelectItem key={m} value={m} className="rounded-xl">
                                {t(`telegramBot.proxyMode.option.${m}`)}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </SettingRow>

            {proxyMode.mode === 'custom' && (
                <SettingRow icon={Globe} label={t('telegramBot.proxyURL.label')} tooltip={t('telegramBot.proxyURL.description')}>
                    <Input
                        value={proxyURL.value}
                        onChange={(e) => proxyURL.setValue(e.target.value)}
                        onBlur={proxyURL.save}
                        placeholder={t('telegramBot.proxyURL.placeholder')}
                        className="w-48 rounded-xl"
                    />
                </SettingRow>
            )}

            <SettingRow icon={Clock} label={t('telegramBot.pollInterval.label')} tooltip={t('telegramBot.pollInterval.description')}>
                <Input
                    type="number"
                    min="1"
                    max="60"
                    value={pollInterval.value}
                    onChange={(e) => pollInterval.setValue(e.target.value)}
                    onBlur={pollInterval.save}
                    placeholder={t('telegramBot.pollInterval.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>
        </SettingCard>
    );
}
