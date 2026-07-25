"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Bot, Clock3, ImageIcon, LoaderCircle, MessageCircle, Send, Square, UserRound } from "lucide-react";
import {
  type Site,
  type SiteAccount,
  type SiteTestConversationClient,
  type SiteTestConversationImage,
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
  { value: "openai_image", label: "Images" },
  { value: "anthropic", label: "Messages" },
];

const CLIENT_OPTIONS: Array<{ value: SiteTestConversationClient; label: string }> = [
  { value: "default", label: "Default" },
  { value: "codex", label: "Codex" },
  { value: "claude", label: "Claude" },
];

const DEFAULT_IMAGE_PROMPT = "a clean product-style image of a small orange octopus mascot";
const TEXT_STREAM_INACTIVITY_TIMEOUT_MS = 90_000;
const IMAGE_STREAM_INACTIVITY_TIMEOUT_MS = 110_000;

function randomInteger(min: number, max: number) {
  return Math.floor(Math.random() * (max - min + 1)) + min;
}

function generateCalculusProblem() {
  const a = randomInteger(2, 6);
  const b = randomInteger(2, 8);
  const c = randomInteger(1, 5);
  const n = randomInteger(2, 5);
  const x0 = randomInteger(1, 3);
  const problems = [
    `设 f(x)=(${a}x^2+${b}x+${c})e^(${n}x)，求 f'(x)，并计算 f'(${x0})。请给出主要步骤。`,
    `求极限：lim_(x→0) [sin(${a}x)-${a}x]/x^3。请说明所用方法。`,
    `计算定积分：∫_0^${a} x^${n}·ln(x+1) dx。请给出主要步骤和最终结果。`,
    `判断级数 Σ_(n=1)^∞ (n+${a})/(n^3+${b}) 的敛散性；若收敛，请说明理由。`,
    `求解初值问题：y'+${a}y=e^(${b}x)，y(0)=${c}。`,
    `计算二重积分：∫_0^${a} ∫_0^(${a}-x) (x+${b}y) dy dx。`,
    `设 z=x^2y+${a}xy^2-${b}x，求点 (${x0}, ${c}) 处的梯度，并写出该点处的最速上升方向。`,
    `求函数 f(x)=x^${n}e^(-${a}x) 在区间 [0,+∞) 上的最大值，并说明极值判定过程。`,
  ];
  return problems[randomInteger(0, problems.length - 1)];
}

function defaultMode(site: Site): SiteTestConversationMode {
  if (site.default_route_type === "openai_response") return "openai_response";
  if (site.default_route_type === "openai_image") return "openai_image";
  if (site.default_route_type === "anthropic") return "anthropic";
  return "openai_chat";
}

function modeFromRouteType(routeType: string | null | undefined): SiteTestConversationMode | null {
  switch ((routeType ?? "").trim()) {
    case "openai_response":
      return "openai_response";
    case "openai_image":
      return "openai_image";
    case "anthropic":
      return "anthropic";
    case "openai_chat":
      return "openai_chat";
    default:
      return null;
  }
}

type ImageResultPreview = {
  key: string;
  url?: string;
  b64Json?: string;
  mimeType: string;
  revisedPrompt?: string;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value && typeof value === "object" && !Array.isArray(value));
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function imageMimeType(item: Record<string, unknown>) {
  const raw = stringValue(item.mime_type) || stringValue(item.mimeType);
  if (raw.startsWith("image/")) return raw;

  const format = (stringValue(item.output_format) || stringValue(item.format)).toLowerCase();
  switch (format) {
    case "jpg":
    case "jpeg":
      return "image/jpeg";
    case "webp":
      return "image/webp";
    case "png":
    default:
      return "image/png";
  }
}

function normalizeBase64Image(value: string) {
  const trimmed = value.trim();
  const match = trimmed.match(/^data:([^;,]+);base64,(.+)$/i);
  if (!match) return { b64Json: trimmed, mimeType: "" };
  return { b64Json: match[2].trim(), mimeType: match[1].trim() };
}

function previewKeyForB64(mimeType: string, b64Json: string) {
  return `b64-${mimeType}-${b64Json.length}-${b64Json.slice(0, 24)}`;
}

function extractImageResultPreviews(reply: string, raw: unknown, images: SiteTestConversationImage[] | undefined) {
  const previews: ImageResultPreview[] = [];
  const seen = new Set<string>();
  const addURL = (url: string, revisedPrompt = "") => {
    if (!url || seen.has(url)) return;
    seen.add(url);
    previews.push({ key: url, url, mimeType: "", revisedPrompt });
  };
  const addB64 = (b64Value: string, mimeType: string, revisedPrompt = "") => {
    const normalized = normalizeBase64Image(b64Value);
    const b64Json = normalized.b64Json;
    const resolvedMimeType = normalized.mimeType || mimeType || "image/png";
    if (!b64Json) return;
    const key = previewKeyForB64(resolvedMimeType, b64Json);
    if (seen.has(key)) return;
    seen.add(key);
    previews.push({ key, b64Json, mimeType: resolvedMimeType, revisedPrompt });
  };
  const addImage = (item: SiteTestConversationImage) => {
    const revisedPrompt = stringValue(item.revised_prompt);
    if (item.b64_json) {
      addB64(stringValue(item.b64_json), stringValue(item.mime_type), revisedPrompt);
      return;
    }
    if (item.url) {
      addURL(stringValue(item.url), revisedPrompt);
    }
  };

  images?.forEach(addImage);
  if (previews.length > 0) return previews;

  for (const line of reply.split(/\r?\n/)) {
    const match = line.match(/^Image\s+\d+:\s+(https?:\/\/\S+)/i);
    if (match?.[1]) addURL(match[1]);
  }

  const payloadData = isRecord(raw) ? raw.data : null;
  const items = Array.isArray(payloadData)
    ? payloadData
    : isRecord(payloadData) && Array.isArray(payloadData.data)
      ? payloadData.data
      : [];

  items.forEach((item) => {
    if (!isRecord(item)) return;
    const b64 = stringValue(item.b64_json);
    if (b64) {
      addB64(b64, imageMimeType(item), stringValue(item.revised_prompt));
      return;
    }
    const url = stringValue(item.url);
    if (url) {
      addURL(url, stringValue(item.revised_prompt));
    }
  });

  if (isRecord(raw)) {
    const b64 = stringValue(raw.b64_json);
    if (b64) {
      addB64(b64, imageMimeType(raw), stringValue(raw.revised_prompt));
    } else {
      const url = stringValue(raw.url);
      if (url) addURL(url, stringValue(raw.revised_prompt));
    }
  }

  return previews;
}

function base64ToBlobURL(b64Json: string, mimeType: string) {
  const binary = window.atob(b64Json);
  const chunkSize = 8192;
  const chunks: BlobPart[] = [];
  for (let offset = 0; offset < binary.length; offset += chunkSize) {
    const slice = binary.slice(offset, offset + chunkSize);
    const buffer = new ArrayBuffer(slice.length);
    const bytes = new Uint8Array(buffer);
    for (let i = 0; i < slice.length; i++) {
      bytes[i] = slice.charCodeAt(i);
    }
    chunks.push(buffer);
  }
  return URL.createObjectURL(new Blob(chunks, { type: mimeType || "image/png" }));
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

function isTestConversationTokenOption(token: SiteAccount["tokens"][number]) {
  if (!token.enabled) return false;
  return true;
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
  const [textGreeting, setTextGreeting] = useState("");
  const [imageGreeting, setImageGreeting] = useState(DEFAULT_IMAGE_PROMPT);
  const [isStreaming, setIsStreaming] = useState(false);
  const [conversationCompleted, setConversationCompleted] = useState(false);
  const [streamError, setStreamError] = useState<Error | null>(null);
  const [conversationResult, setConversationResult] = useState<SiteTestConversationResult | null>(null);
  const requestControllerRef = useRef<AbortController | null>(null);
  const abortMessageRef = useRef("");
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
      dedupeTokenOptions(latestAccount.tokens.filter(isTestConversationTokenOption))
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

  const selectedModelRouteType = useMemo(() => {
    const found = latestAccount.models.find(
      (item) => item.model_name.trim() === effectiveModel && !item.disabled,
    );
    return found?.route_type ?? null;
  }, [effectiveModel, latestAccount.models]);

  const suggestedMode = modeFromRouteType(selectedModelRouteType);

  const selectedToken = readyTokenOptions.find((token) => String(token.id) === effectiveTokenID);
  const effectiveMode = client === "codex" ? "openai_response" : client === "claude" ? "anthropic" : mode;
  const isImageMode = effectiveMode === "openai_image";
  const greeting = isImageMode ? imageGreeting : textGreeting;
  const canSend = Boolean(effectiveTokenID && effectiveModel && greeting.trim()) && !isStreaming;
  const imagePreviews = useMemo(
    () =>
      conversationResult?.mode === "openai_image"
        ? extractImageResultPreviews(conversationResult.reply, conversationResult.raw, conversationResult.images)
        : [],
    [conversationResult],
  );
  const [imageObjectURLs, setImageObjectURLs] = useState<Record<string, string>>({});
  const tokenLabel = (token: (typeof enabledTokenOptions)[number]) =>
    [token.group_name || token.group_key || "default", token.name || `Key ${token.id}`].filter(Boolean).join(" / ");

  useEffect(() => {
    const urls: Record<string, string> = {};
    for (const preview of imagePreviews) {
      if (!preview.b64Json) continue;
      try {
        urls[preview.key] = base64ToBlobURL(preview.b64Json, preview.mimeType);
      } catch {
        // Keep the failed preview visible as text instead of breaking the panel.
      }
    }
    setImageObjectURLs(urls);
    return () => {
      Object.values(urls).forEach((url) => URL.revokeObjectURL(url));
    };
  }, [imagePreviews]);

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

  useEffect(() => {
    if (suggestedMode === "openai_image" && client !== "default") {
      setClient("default");
      setMode("openai_image");
      return;
    }
    if (client !== "default" || !suggestedMode) return;
    setMode(suggestedMode);
  }, [client, suggestedMode]);

  useEffect(
    () => () => {
      abortMessageRef.current = "测试对话已停止";
      requestControllerRef.current?.abort();
    },
    [],
  );

  const resetConversation = () => {
    setConversationResult(null);
    setStreamError(null);
    setConversationCompleted(false);
    setTextGreeting(generateCalculusProblem());
    setImageGreeting(DEFAULT_IMAGE_PROMPT);
  };

  const handleSend = async () => {
    if (!canSend || requestControllerRef.current) return;
    const nextGreeting = greeting.trim();
    const controller = new AbortController();
    const inactivityTimeoutMS = isImageMode
      ? IMAGE_STREAM_INACTIVITY_TIMEOUT_MS
      : TEXT_STREAM_INACTIVITY_TIMEOUT_MS;
    let inactivityTimer: ReturnType<typeof setTimeout> | null = null;
    const resetInactivityTimer = () => {
      if (inactivityTimer) clearTimeout(inactivityTimer);
      inactivityTimer = setTimeout(() => {
        abortMessageRef.current = `测试对话超过 ${Math.round(inactivityTimeoutMS / 1000)} 秒没有返回新内容，已自动停止`;
        controller.abort();
      }, inactivityTimeoutMS);
    };
    const pendingResult: SiteTestConversationResult = {
      model: effectiveModel,
      mode: effectiveMode,
      greeting: nextGreeting,
      reply: "",
      duration_ms: 0,
    };
    requestControllerRef.current = controller;
    abortMessageRef.current = "";
    setConversationResult(pendingResult);
    setStreamError(null);
    setConversationCompleted(false);
    setIsStreaming(true);
    resetInactivityTimer();
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
          onStart: resetInactivityTimer,
          onDelta: (delta) => {
            resetInactivityTimer();
            setConversationResult((current) => ({
              ...(current ?? pendingResult),
              reply: `${current?.reply ?? ""}${delta}`,
            }));
          },
          onDone: (result) => {
            if (inactivityTimer) clearTimeout(inactivityTimer);
            setConversationResult(result);
            setConversationCompleted(true);
          },
          onError: (message) => {
            if (inactivityTimer) clearTimeout(inactivityTimer);
            setStreamError(new Error(message));
          },
        },
        controller.signal,
      );
    } catch (error) {
      const message = abortMessageRef.current;
      setConversationCompleted(false);
      setStreamError(message ? new Error(message) : error instanceof Error ? error : new Error("测试对话失败"));
    } finally {
      if (inactivityTimer) clearTimeout(inactivityTimer);
      if (requestControllerRef.current === controller) {
        requestControllerRef.current = null;
      }
      abortMessageRef.current = "";
      setIsStreaming(false);
    }
  };

  const handleStop = () => {
    if (!isStreaming || !requestControllerRef.current) return;
    abortMessageRef.current = "测试对话已手动停止，可保留当前内容后重新测试";
    requestControllerRef.current.abort();
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen) {
          if (conversationCompleted) {
            resetConversation();
          } else if (!textGreeting.trim()) {
            setTextGreeting(generateCalculusProblem());
          }
        }
        setOpen(nextOpen);
      }}
    >
      <DialogTrigger asChild>
        <Button type="button" size="sm" variant="outline" className="h-8 rounded-xl">
          {isStreaming ? <LoaderCircle className="size-4 animate-spin" /> : <MessageCircle className="size-4" />}
          {isStreaming ? "测试中" : "测试对话"}
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[calc(100dvh-1.5rem)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>测试对话</DialogTitle>
          <DialogDescription>
            使用当前账号最新同步到的 API Key 和模型发起一次测试请求，图片模式会调用 Images API。
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="grid gap-1.5 text-sm">
              <span className="text-muted-foreground">API Key</span>
              <Select value={effectiveTokenID} onValueChange={setTokenID} disabled={isStreaming}>
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
              <span className="text-muted-foreground">测试模式</span>
              <Select
                value={effectiveMode}
                onValueChange={(value) => {
                  const nextMode = value as SiteTestConversationMode;
                  setMode(nextMode);
                  if (nextMode === "openai_image") setClient("default");
                }}
                disabled={isStreaming || client === "codex" || client === "claude"}
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
                {suggestedMode ? `按模型端点建议：${MODE_OPTIONS.find((option) => option.value === suggestedMode)?.label ?? suggestedMode}` : "默认按站点路由类型选择"}
              </span>
            </label>
          </div>

          <label className="grid gap-1.5 text-sm">
            <span className="text-muted-foreground">客户端</span>
            <Select
              value={client}
              disabled={isStreaming || isImageMode}
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
              {isImageMode ? "图片测试使用 Default 客户端和 Images API" : "Codex 使用 Responses；Claude 使用 Anthropic Messages 和 claude-cli User-Agent"}
            </span>
          </label>

          <label className="grid gap-1.5 text-sm">
            <span className="text-muted-foreground">模型</span>
            <Select value={effectiveModel} onValueChange={setModel} disabled={isStreaming}>
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
            <span className="text-muted-foreground">{isImageMode ? "图片提示词" : "招呼语"}</span>
            <textarea
              value={greeting}
              onChange={(event) => {
                if (isImageMode) {
                  setImageGreeting(event.target.value);
                } else {
                  setTextGreeting(event.target.value);
                }
              }}
              rows={3}
              disabled={isStreaming}
              className="min-h-20 w-full resize-y rounded-xl border border-input bg-background px-3 py-2 text-sm shadow-xs outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50"
              placeholder={isImageMode ? DEFAULT_IMAGE_PROMPT : "随机生成一道高数题"}
            />
          </label>

          <div className="rounded-xl border border-border/70 bg-muted/20">
            <div className="flex items-center justify-between gap-3 border-b border-border/60 px-4 py-3">
              <div className="min-w-0">
                <div className="text-sm font-medium">{isImageMode ? "图片结果" : "对话结果"}</div>
                <div className="mt-0.5 truncate text-xs text-muted-foreground">
                  {effectiveModel ? `${effectiveModel} · ${effectiveMode}` : "请选择 Key 和模型后发送"}
                </div>
              </div>
              {conversationResult ? (
                <div className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
                  <Clock3 className="size-3.5" />
                  {isStreaming
                    ? "进行中"
                    : conversationCompleted
                      ? conversationResult.duration_ms > 0
                        ? `${conversationResult.duration_ms}ms`
                        : "已完成"
                      : streamError
                        ? "已停止"
                        : "等待结果"}
                </div>
              ) : null}
            </div>

            <div className="max-h-72 min-h-44 space-y-3 overflow-y-auto p-4">
              {streamError ? (
                <div className="rounded-xl border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">
                  {errorMessage(streamError)}
                </div>
              ) : null}
              {conversationResult ? (
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
                      {conversationResult.mode === "openai_image" ? <ImageIcon className="size-4" /> : <Bot className="size-4" />}
                    </div>
                    <div className="max-w-[85%] whitespace-pre-wrap rounded-2xl rounded-tl-sm border border-border/70 bg-background px-3 py-2 text-sm leading-relaxed">
                      {conversationResult.reply || (isStreaming ? "..." : "No text content returned.")}
                      {conversationResult.mode === "openai_image" && imagePreviews.length > 0 ? (
                        <div className="mt-3 grid gap-2">
                          {imagePreviews.map((preview) => {
                            const src = preview.url || imageObjectURLs[preview.key] || "";
                            return (
                              <div key={preview.key} className="grid gap-1.5">
                                {src ? (
                                  // eslint-disable-next-line @next/next/no-img-element
                                  <img
                                    src={src}
                                    alt="Generated result"
                                    className="max-h-64 w-full rounded-lg border border-border/70 object-contain"
                                  />
                                ) : (
                                  <div className="rounded-lg border border-border/70 bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
                                    图片数据正在准备。
                                  </div>
                                )}
                                {preview.revisedPrompt ? (
                                  <div className="line-clamp-2 text-xs text-muted-foreground">
                                    {preview.revisedPrompt}
                                  </div>
                                ) : null}
                              </div>
                            );
                          })}
                        </div>
                      ) : null}
                    </div>
                  </div>
                </>
              ) : (
                <div className="flex h-32 items-center justify-center rounded-xl border border-dashed border-border/70 text-sm text-muted-foreground">
                  发送后会在这里显示测试结果。
                </div>
              )}
            </div>
          </div>
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => setOpen(false)}>
            关闭
          </Button>
          {isStreaming ? (
            <Button type="button" variant="destructive" onClick={handleStop}>
              <Square className="size-4" />
              停止测试
            </Button>
          ) : (
            <Button type="button" disabled={!canSend} onClick={handleSend}>
              <Send className="size-4" />
              {streamError ? "重新测试" : isImageMode ? "生成图片" : "发送测试"}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
