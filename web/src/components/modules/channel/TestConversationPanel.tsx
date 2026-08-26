'use client';

import { useMemo, useState } from 'react';
import { Bot, Clock3, LoaderCircle, MessageCircle, Send, UserRound } from 'lucide-react';
import { type Channel, ChannelType, useTestChannelConversation } from '@/api/endpoints/channel';
import { Button } from '@/components/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    DialogTrigger,
} from '@/components/ui/dialog';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

const DEFAULT_GREETING = '请简洁回答：17 × 23 等于多少？给出计算过程。';

export function ChannelTestConversationPanel({ channel }: { channel: Channel }) {
    const testConversation = useTestChannelConversation();
    const models = useMemo(() => Array.from(new Set(
        `${channel.model},${channel.custom_model}`.split(',').map((item) => item.trim()).filter(Boolean),
    )), [channel.custom_model, channel.model]);
    const [model, setModel] = useState(models[0] ?? '');
    const [greeting, setGreeting] = useState(DEFAULT_GREETING);
    const [open, setOpen] = useState(false);
    const effectiveModel = models.includes(model) ? model : models[0] ?? '';
    const canTest = channel.type !== ChannelType.OpenAIEmbedding && models.length > 0;

    const send = () => {
        if (!effectiveModel || !greeting.trim()) return;
        testConversation.mutate({ channel_id: channel.id, model: effectiveModel, greeting: greeting.trim() });
    };

    if (!canTest) return null;

    return (
        <Dialog open={open} onOpenChange={(next) => {
            setOpen(next);
            if (next) testConversation.reset();
        }}>
            <DialogTrigger asChild>
                <Button type="button" size="sm" variant="outline" className="h-8 rounded-xl">
                    <MessageCircle className="size-4" />
                    测试对话
                </Button>
            </DialogTrigger>
            <DialogContent className="max-h-[calc(100dvh-1.5rem)] overflow-y-auto sm:max-w-2xl">
                <DialogHeader>
                    <DialogTitle>测试普通渠道</DialogTitle>
                    <DialogDescription className="sr-only">使用普通渠道当前配置测试模型对话。</DialogDescription>
                </DialogHeader>

                <div className="grid gap-4">
                    <label className="grid gap-1.5 text-sm">
                        <span className="text-muted-foreground">模型</span>
                        <Select value={effectiveModel} onValueChange={setModel} disabled={testConversation.isPending}>
                            <SelectTrigger className="h-9 w-full rounded-xl"><SelectValue /></SelectTrigger>
                            <SelectContent>
                                {models.map((item) => <SelectItem key={item} value={item}>{item}</SelectItem>)}
                            </SelectContent>
                        </Select>
                    </label>

                    <label className="grid gap-1.5 text-sm">
                        <span className="text-muted-foreground">消息</span>
                        <textarea
                            value={greeting}
                            onChange={(event) => setGreeting(event.target.value)}
                            rows={3}
                            disabled={testConversation.isPending}
                            className="min-h-20 w-full resize-y rounded-xl border border-input bg-background px-3 py-2 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50"
                        />
                    </label>

                    <div className="rounded-xl border border-border/70 bg-muted/20">
                        <div className="flex items-center justify-between border-b border-border/60 px-4 py-3">
                            <span className="text-sm font-medium">对话结果</span>
                            {testConversation.data ? (
                                <span className="flex items-center gap-1 text-xs text-muted-foreground">
                                    <Clock3 className="size-3.5" />{testConversation.data.duration_ms}ms
                                </span>
                            ) : null}
                        </div>
                        <div className="min-h-44 space-y-3 p-4">
                            {testConversation.isError ? (
                                <div className="rounded-xl border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">
                                    {testConversation.error.message}
                                </div>
                            ) : null}
                            {testConversation.data ? (
                                <>
                                    <div className="flex justify-end gap-2">
                                        <div className="max-w-[85%] rounded-2xl rounded-tr-sm bg-primary px-3 py-2 text-sm text-primary-foreground">{testConversation.data.greeting}</div>
                                        <UserRound className="mt-2 size-4 shrink-0 text-primary" />
                                    </div>
                                    <div className="flex gap-2">
                                        <Bot className="mt-2 size-4 shrink-0 text-muted-foreground" />
                                        <div className="max-w-[85%] whitespace-pre-wrap rounded-2xl rounded-tl-sm border bg-background px-3 py-2 text-sm">{testConversation.data.reply}</div>
                                    </div>
                                </>
                            ) : testConversation.isPending ? (
                                <div className="flex h-32 items-center justify-center gap-2 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />正在请求上游</div>
                            ) : (
                                <div className="flex h-32 items-center justify-center text-sm text-muted-foreground">等待测试结果</div>
                            )}
                        </div>
                    </div>
                </div>

                <DialogFooter>
                    <Button type="button" variant="outline" onClick={() => setOpen(false)}>关闭</Button>
                    <Button type="button" onClick={send} disabled={testConversation.isPending || !effectiveModel || !greeting.trim()}>
                        {testConversation.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <Send className="size-4" />}
                        {testConversation.isError ? '重新测试' : '发送测试'}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
