"use client";

import { useEffect, useMemo, useState } from "react";
import { Bot, Clock3, LoaderCircle, MessageCircle, Send, UserRound } from "lucide-react";
import {
  type Site,
  type SiteAccount,
  type SiteTestConversationClient,
  type SiteTestConversationResult,
  type SiteTestConversationMode,
  streamTestSiteConversation,
  useSiteList,
} from "@/api/endpoints/site";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

const MODE_OPTIONS: Array<{ value: SiteTestConversationMode; label: string }> = [
  { value: "openai_chat", label: "Chat" },
  { value: "openai_response", label: "Responses" },
  { value: "anthropic", label: "Messages" },
];

const CLIENT_OPTIONS: Array<{ value: SiteTestConversationClient; label: string }> = [
  { value: "default", label: "Default" },
  { value: "codex", label: "Codex" },
  { value: "claude", label: "Claude" },
];

function defaultMode(site: Site): SiteTestConversationMode {
  if (site.default_route_type === "openai_response") return "openai_response";
  if (site.default_route_type === "anthropic") return "anthropic";
  return "openai_chat";
}

function errorMessage(error: unknown) {
  if (error instanceof Error) return error.message;
  if (error && typeof error === "object" && "message" in error) {
    const message = (error as { message?: unknown }).message;
    if (typeof message === "string") return message;
  }
  return "测试对话失败";
}

function tokenSourceRank(source: string) {
  switch (source.trim()) {
    case "manual":
      return 3;
    case "sync":
      return 2;
    case "account":
      return 1;
    default:
      return 0;
  }
}

function preferTokenOption<T extends { token: string; value_status?: string; is_default: boolean; source: string }>(current: T, candidate: T) {
  const currentReady = current.value_status !== "masked_pending";
  const candidateReady = candidate.value_status !== "masked_pending";
  if (candidateReady !== currentReady) return candidateReady;

  const sourceDelta = tokenSourceRank(candidate.source) - tokenSourceRank(current.source);
  if (sourceDelta !== 0) return sourceDelta > 0;

  if (candidate.is_default !== current.is_default) return candidate.is_default;
  return false;
}

function dedupeTokenOptions<T extends { token: string; value_status?: string; is_default: boolean; source: string }>(tokens: T[]) {
  const byValue = new Map<string, T>();
  const withoutValue: T[] = [];

  for (const token of tokens) {
    const value = token.token.trim();
    if (!value) {
      withoutValue.push(token);
      continue;
    }

    const existing = byValue.get(value);
    if (!existing || preferTokenOption(existing, token)) {
      byValue.set(value, token);
    }
  }

  return [...byValue.values(), ...withoutValue];
}

export function TestConversationPanel({
  site,
  account,
}: {
  site: Site;
  account: SiteAccount;
}) {
  const [open, setOpen] = useState(false);
  const [tokenID, setTokenID] = useState("");
  const [model, setModel] = useState("");
  const [mode, setMode] = useState<SiteTestConversationMode>(() => defaultMode(site));
  const [client, setClient] = useState<SiteTestConversationClient>("default");
  const [greeting, setGreeting] = useState("hi");
  const [isStreaming, setIsStreaming] = useState(false);
  const [streamError, setStreamError] = useState<Error | null>(null);
  const [conversationResult, setConversationResult] = useState<SiteTestConversationResult | null>(null);
  const { data: latestSites } = useSiteList();

  const latestAccount = useMemo(() => {
    if (!latestSites) return account;
    for (const item of latestSites) {
      const found = item.accounts.find((candidate) => candidate.id === account.id);
      if (found) return found;
    }
    return account;
  }, [account, latestSites]);

  const enabledTokenOptions = useMemo(
    () =>
      dedupeTokenOptions(latestAccount.tokens.filter((token) => token.enabled))
        .sort((a, b) => {
          const groupCompare = (a.group_name || a.group_key).localeCompare(b.group_name || b.group_key);
          return Number(b.is_default) - Number(a.is_default) || groupCompare || a.name.localeCompare(b.name);
        }),
    [latestAccount.tokens],
  );
  const readyTokenOptions = useMemo(
    () => enabledTokenOptions.filter((token) => token.value_status !== "masked_pending"),
    [enabledTokenOptions],
  );
  const maskedPendingTokenCount = enabledTokenOptions.length - readyTokenOptions.length;

  const modelOptions = useMemo(() => {
    const seen = new Set<string>();
    const names: string[] = [];
    for (const item of latestAccount.models) {
      const name = item.model_name.trim();
      if (!name || item.disabled || seen.has(name)) continue;
      seen.add(name);
      names.push(name);
    }
    return names.sort((a, b) => a.localeCompare(b));
  }, [latestAccount.models]);

  const effectiveTokenID = useMemo(() => {
    if (tokenID && readyTokenOptions.some((token) => String(token.id) === tokenID)) return tokenID;
    const preferred = readyTokenOptions.find((token) => token.is_default) ?? readyTokenOptions[0];
    return preferred ? String(preferred.id) : "";
  }, [tokenID, readyTokenOptions]);

  const effectiveModel = useMemo(() => {
    if (model && modelOptions.includes(model)) return model;
    return modelOptions[0] ?? "";
  }, [model, modelOptions]);

  const canSend = Boolean(effectiveTokenID && effectiveModel && greeting.trim()) && !isStreaming;
  const selectedToken = readyTokenOptions.find((token) => String(token.id) === effectiveTokenID);
  const effectiveMode = client === "codex" ? "openai_response" : client === "claude" ? "anthropic" : mode;
  const tokenLabel = (token: (typeof enabledTokenOptions)[number]) =>
    [token.group_name || token.group_key || "default", token.name || `Key ${token.id}`].filter(Boolean).join(" / ");

  useEffect(() => {
    if (!tokenID) return;
    if (!readyTokenOptions.some((token) => String(token.id) === tokenID)) {
      setTokenID("");
    }
  }, [readyTokenOptions, tokenID]);

  useEffect(() => {
    if (!model) return;
    if (!modelOptions.includes(model)) {
      setModel("");
    }
  }, [model, modelOptions]);

  const handleSend = async () => {
    if (!canSend) return;
    const nextGreeting = greeting.trim() || "hi";
    const pendingResult: SiteTestConversationResult = {
      model: effectiveModel,
      mode: effectiveMode,
      greeting: nextGreeting,
      reply: "",
      duration_ms: 0,
    };
    setConversationResult(pendingResult);
    setStreamError(null);
    setIsStreaming(true);
    try {
      await streamTestSiteConversation(
        {
          account_id: account.id,
          token_id: Number(effectiveTokenID),
          model: effectiveModel,
          mode: effectiveMode,
          greeting: nextGreeting,
          client,
        },
        {
          onDelta: (delta) => {
            setConversationResult((current) => ({
              ...(current ?? pendingResult),
              reply: `${current?.reply ?? ""}${delta}`,
            }));
          },
          onDone: (result) => {
            setConversationResult(result);
          },
          onError: (message) => {
            setStreamError(new Error(message));
          },
        },
      );
    } catch (error) {
      setStreamError(error instanceof Error ? error : new Error("测试对话失败"));
    } finally {
      setIsStreaming(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button type="button" size="sm" variant="outline" className="h-8 rounded-xl">
          <MessageCircle className="size-4" />
          测试对话
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[calc(100dvh-1.5rem)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>测试对话</DialogTitle>
          <DialogDescription>
            使用当前账号最新同步到的 API Key 和模型发起一次测试请求，招呼语默认 hi。
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="grid gap-1.5 text-sm">
              <span className="text-muted-foreground">API Key</span>
              <Select value={effectiveTokenID} onValueChange={setTokenID}>
                <SelectTrigger className="h-9 w-full rounded-xl">
                  <SelectValue placeholder="选择 Key" />
                </SelectTrigger>
                <SelectContent>
                  {enabledTokenOptions.length > 0 ? (
                    enabledTokenOptions.map((token) => (
                      <SelectItem
                        key={token.id}
                        value={String(token.id)}
                        disabled={token.value_status === "masked_pending"}
                      >
                        {tokenLabel(token)}
                        {token.value_status === "masked_pending" ? " · 待补全" : ""}
                      </SelectItem>
                    ))
                  ) : (
                    <SelectItem value="__empty" disabled>
                      当前账号没有同步到 Key
                    </SelectItem>
                  )}
                </SelectContent>
              </Select>
              <span className="min-h-4 truncate text-xs text-muted-foreground">
                {selectedToken
                  ? `分组：${selectedToken.group_name || selectedToken.group_key || "default"}`
                  : maskedPendingTokenCount > 0
                    ? "当前只有待补全 Key，请先在站点渠道里补全真实 Key"
                    : "同步账号后会显示可用于测试的 Key"}
              </span>
            </label>

            <label className="grid gap-1.5 text-sm">
              <span className="text-muted-foreground">对话模式</span>
              <Select
                value={effectiveMode}
                onValueChange={(value) => setMode(value as SiteTestConversationMode)}
                disabled={client === "codex" || client === "claude"}
              >
                <SelectTrigger className="h-9 w-full rounded-xl">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {MODE_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <span className="min-h-4 text-xs text-muted-foreground">
                默认按站点路由类型选择
              </span>
            </label>
          </div>

          <label className="grid gap-1.5 text-sm">
            <span className="text-muted-foreground">客户端</span>
            <Select
              value={client}
              onValueChange={(value) => {
                const nextClient = value as SiteTestConversationClient;
                setClient(nextClient);
                if (nextClient === "codex") setMode("openai_response");
                if (nextClient === "claude") setMode("anthropic");
              }}
            >
              <SelectTrigger className="h-9 w-full rounded-xl">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CLIENT_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <span className="min-h-4 text-xs text-muted-foreground">
              Codex 使用 Responses；Claude 使用 Anthropic Messages 和 claude-cli User-Agent
            </span>
          </label>

          <label className="grid gap-1.5 text-sm">
            <span className="text-muted-foreground">模型</span>
            <Select value={effectiveModel} onValueChange={setModel}>
              <SelectTrigger className="h-9 w-full rounded-xl">
                <SelectValue placeholder="选择模型" />
              </SelectTrigger>
              <SelectContent>
                {modelOptions.map((name) => (
                  <SelectItem key={name} value={name}>
                    {name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </label>

          <label className="grid gap-1.5 text-sm">
            <span className="text-muted-foreground">招呼语</span>
            <textarea
              value={greeting}
              onChange={(event) => setGreeting(event.target.value)}
              rows={3}
              className="min-h-20 w-full resize-y rounded-xl border border-input bg-background px-3 py-2 text-sm shadow-xs outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50"
              placeholder="hi"
            />
          </label>

          <div className="rounded-xl border border-border/70 bg-muted/20">
            <div className="flex items-center justify-between gap-3 border-b border-border/60 px-4 py-3">
              <div className="min-w-0">
                <div className="text-sm font-medium">对话结果</div>
                <div className="mt-0.5 truncate text-xs text-muted-foreground">
                  {effectiveModel ? `${effectiveModel} · ${effectiveMode}` : "请选择 Key 和模型后发送"}
                </div>
              </div>
              {conversationResult ? (
                <div className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
                  <Clock3 className="size-3.5" />
                  {conversationResult.duration_ms > 0 ? `${conversationResult.duration_ms}ms` : "streaming"}
                </div>
              ) : null}
            </div>

            <div className="max-h-72 min-h-44 space-y-3 overflow-y-auto p-4">
              {streamError ? (
                <div className="rounded-xl border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">
                  {errorMessage(streamError)}
                </div>
              ) : conversationResult ? (
                <>
                  <div className="flex justify-end gap-2">
                    <div className="max-w-[85%] rounded-2xl rounded-tr-sm bg-primary px-3 py-2 text-sm leading-relaxed text-primary-foreground">
                      {conversationResult.greeting}
                    </div>
                    <div className="mt-1 flex size-7 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary">
                      <UserRound className="size-4" />
                    </div>
                  </div>
                  <div className="flex gap-2">
                    <div className="mt-1 flex size-7 shrink-0 items-center justify-center rounded-full bg-background text-muted-foreground ring-1 ring-border">
                      <Bot className="size-4" />
                    </div>
                    <div className="max-w-[85%] whitespace-pre-wrap rounded-2xl rounded-tl-sm border border-border/70 bg-background px-3 py-2 text-sm leading-relaxed">
                      {conversationResult.reply || (isStreaming ? "..." : "No text content returned.")}
                    </div>
                  </div>
                </>
              ) : (
                <div className="flex h-32 items-center justify-center rounded-xl border border-dashed border-border/70 text-sm text-muted-foreground">
                  发送后会在这里显示用户消息和模型回复。
                </div>
              )}
            </div>
          </div>
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => setOpen(false)}>
            关闭
          </Button>
          <Button type="button" disabled={!canSend} onClick={handleSend}>
            {isStreaming ? (
              <LoaderCircle className="size-4 animate-spin" />
            ) : (
              <Send className="size-4" />
            )}
            发送测试
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
