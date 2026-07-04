'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslations } from 'next-intl';
import { Bell, Bot, Clock, Gauge, Globe, KeyRound, Link, Send, ShieldCheck, Wallet } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { SettingKey, useSettingList, useSetSetting } from '@/api/endpoints/setting';
import { toast } from '@/components/common/Toast';
import type { ApiError } from '@/api/types';
import { SettingCard, SettingRow, SettingSection, useSettingField, useSettingToggle } from './shared';

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
    const reportEnabled = useSettingToggle(SettingKey.TelegramReportEnabled);
    const reportTime = useSettingField(SettingKey.TelegramReportTime);
    const alertEnabled = useSettingToggle(SettingKey.TelegramAlertEnabled);
    const balanceThreshold = useSettingField(SettingKey.TelegramAlertBalanceThreshold);
    const failureRatePct = useSettingField(SettingKey.TelegramAlertFailureRatePct);
    const failureWindow = useSettingField(SettingKey.TelegramAlertFailureWindow);
    const minRequests = useSettingField(SettingKey.TelegramAlertMinRequests);
    const cooldownMinutes = useSettingField(SettingKey.TelegramAlertCooldownMinutes);
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

            <SettingSection title={t('telegramBot.report.section')} tooltip={t('telegramBot.report.description')} />

            <SettingRow icon={Gauge} label={t('telegramBot.report.enabled.label')} tooltip={t('telegramBot.report.enabled.description')}>
                <Switch checked={reportEnabled.enabled} onCheckedChange={reportEnabled.toggle} />
            </SettingRow>

            <SettingRow icon={Clock} label={t('telegramBot.report.time.label')} tooltip={t('telegramBot.report.time.description')}>
                <Input
                    value={reportTime.value}
                    onChange={(e) => reportTime.setValue(e.target.value)}
                    onBlur={reportTime.save}
                    placeholder={t('telegramBot.report.time.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>

            <SettingSection title={t('telegramBot.alert.section')} tooltip={t('telegramBot.alert.description')} />

            <SettingRow icon={Bell} label={t('telegramBot.alert.enabled.label')} tooltip={t('telegramBot.alert.enabled.description')}>
                <Switch checked={alertEnabled.enabled} onCheckedChange={alertEnabled.toggle} />
            </SettingRow>

            <SettingRow icon={Wallet} label={t('telegramBot.alert.balanceThreshold.label')} tooltip={t('telegramBot.alert.balanceThreshold.description')}>
                <Input
                    type="number"
                    min="0"
                    step="0.01"
                    value={balanceThreshold.value}
                    onChange={(e) => balanceThreshold.setValue(e.target.value)}
                    onBlur={balanceThreshold.save}
                    placeholder={t('telegramBot.alert.balanceThreshold.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>

            <SettingRow icon={Gauge} label={t('telegramBot.alert.failureRate.label')} tooltip={t('telegramBot.alert.failureRate.description')}>
                <Input
                    type="number"
                    min="1"
                    max="100"
                    value={failureRatePct.value}
                    onChange={(e) => failureRatePct.setValue(e.target.value)}
                    onBlur={failureRatePct.save}
                    placeholder={t('telegramBot.alert.failureRate.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>

            <SettingRow icon={Clock} label={t('telegramBot.alert.failureWindow.label')} tooltip={t('telegramBot.alert.failureWindow.description')}>
                <Input
                    type="number"
                    min="1"
                    value={failureWindow.value}
                    onChange={(e) => failureWindow.setValue(e.target.value)}
                    onBlur={failureWindow.save}
                    placeholder={t('telegramBot.alert.failureWindow.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>

            <SettingRow icon={Gauge} label={t('telegramBot.alert.minRequests.label')} tooltip={t('telegramBot.alert.minRequests.description')}>
                <Input
                    type="number"
                    min="1"
                    value={minRequests.value}
                    onChange={(e) => minRequests.setValue(e.target.value)}
                    onBlur={minRequests.save}
                    placeholder={t('telegramBot.alert.minRequests.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>

            <SettingRow icon={Clock} label={t('telegramBot.alert.cooldown.label')} tooltip={t('telegramBot.alert.cooldown.description')}>
                <Input
                    type="number"
                    min="1"
                    value={cooldownMinutes.value}
                    onChange={(e) => cooldownMinutes.setValue(e.target.value)}
                    onBlur={cooldownMinutes.save}
                    placeholder={t('telegramBot.alert.cooldown.placeholder')}
                    className="w-48 rounded-xl"
                />
            </SettingRow>
        </SettingCard>
    );
}
