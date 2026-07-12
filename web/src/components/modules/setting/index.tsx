'use client';

import { PageWrapper } from '@/components/common/PageWrapper';
import { SettingAppearance } from './Appearance';
import { SettingAPIKey } from './APIKey';
import { SettingAccount } from './Account';
import { SettingNetwork } from './Network';
import { SettingReliability } from './Reliability';
import { SettingSyncTasks } from './SyncTasks';
import { SettingData } from './Data';
import { SettingTelegram } from './Telegram';
import { SettingVersion } from './Version';
import { SettingAudit } from './Audit';
import { SettingPrice } from './Price';

export function Setting() {
    return (
        <div className="h-full min-h-0 overflow-y-auto overscroll-contain rounded-t-3xl">
            <PageWrapper className="columns-1 gap-4 pb-24 md:columns-2 md:pb-4 *:mb-4 *:min-w-0 *:break-inside-avoid">
                <SettingAPIKey key="setting-apikey" />
                <SettingAppearance key="setting-appearance" />
                <SettingPrice key="setting-price" />
                <SettingNetwork key="setting-network" />
                <SettingVersion key="setting-version" />
                <SettingTelegram key="setting-telegram" />
                <SettingAccount key="setting-account" />
                <SettingReliability key="setting-reliability" />
                <SettingSyncTasks key="setting-sync-tasks" />
                <SettingData key="setting-data" />
                <SettingAudit key="setting-audit" />
            </PageWrapper>
        </div>
    );
}
