import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export async function copyText(text: string): Promise<void> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return
    }
  } catch {
    // Fall back for denied clipboard permissions and non-secure contexts.
  }

  const textarea = document.createElement("textarea")
  textarea.value = text
  textarea.readOnly = true
  textarea.style.position = "fixed"
  textarea.style.opacity = "0"
  document.body.appendChild(textarea)
  textarea.select()
  textarea.setSelectionRange(0, text.length)
  const copied = document.execCommand("copy")
  textarea.remove()
  if (!copied) throw new Error("Clipboard access is unavailable")
}


function formatNumber(num: number | undefined, compare: number[], units: string[]): { value: string, unit: string } {
  if (num === undefined) return { value: "0.00", unit: units[0] };
  else if (num >= compare[0]) return { value: (num / compare[0]).toFixed(2), unit: units[1] };
  else if (num >= compare[1]) return { value: (num / compare[1]).toFixed(2), unit: units[2] };
  else if (num >= compare[2]) return { value: (num / compare[2]).toFixed(2), unit: units[3] };
  else if (num >= compare[3]) return { value: (num / compare[3]).toFixed(2), unit: units[4] };
  else return { value: (num).toFixed(2), unit: units[5] };
}

export function formatCount(num: number | undefined): { raw: number, formatted: { value: string, unit: string } } {
  return {
    raw: num ?? 0,
    formatted: formatNumber(num, [1000000000, 1000000, 1000, 1], ['', 'B', 'M', 'K', '', '']),
  };
}
export function formatMoney(num: number | undefined): { raw: number, formatted: { value: string, unit: string } } {
  return {
    raw: num ?? 0,
    formatted: formatNumber(num, [1000000000, 1000000, 1000, 1], ['$', 'B$', 'M$', 'K$', '$', '$']),
  };
}

export function formatTime(ms: number | undefined): { raw: number, formatted: { value: string, unit: string } } {
  return {
    raw: ms ?? 0,
    formatted: formatNumber(ms, [86400000, 3600000, 60000, 1000], ['', 'd', 'h', 'm', 's', 'ms']),
  };
}
