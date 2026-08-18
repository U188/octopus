"use client"

import * as React from "react"

import { cn } from "@/lib/utils"

type CheckboxProps = React.ComponentProps<"input"> & {
  onCheckedChange?: (checked: boolean) => void
}

function Checkbox({ className, onChange, onCheckedChange, ...props }: CheckboxProps) {
  return (
    <input
      {...props}
      type="checkbox"
      data-slot="checkbox"
      className={cn(
        "size-4 shrink-0 rounded border border-input accent-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50",
        className
      )}
      onChange={(event) => {
        onChange?.(event)
        onCheckedChange?.(event.currentTarget.checked)
      }}
    />
  )
}

export { Checkbox }
