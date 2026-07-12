'use client';

import { useTranslations } from 'next-intl';
import { ArrowRight, BadgeDollarSign } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useNavStore } from '@/components/modules/navbar';
import { SettingCard } from './shared';

export function SettingPrice() {
    const t = useTranslations('setting.price');
    const setActiveItem = useNavStore((state) => state.setActiveItem);

    return (
        <SettingCard icon={BadgeDollarSign} title={t('title')}>
            <p className="text-sm text-muted-foreground">{t('description')}</p>
            <Button
                type="button"
                variant="outline"
                className="w-full justify-between rounded-xl"
                onClick={() => setActiveItem('model')}
            >
                {t('open')}
                <ArrowRight className="size-4" />
            </Button>
        </SettingCard>
    );
}
