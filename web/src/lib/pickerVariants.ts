/**
 * How a property picker's trigger looks:
 * - `icon`: just the icon, for list rows
 * - `field`: icon and value on one full-width line, for the issue sidebar
 * - `chip`: a bordered pill, for the new-issue dialog
 */
export type PickerVariant = 'icon' | 'field' | 'chip'

export const TRIGGER_CLASS: Record<PickerVariant, string> = {
  icon: 'flex size-6 shrink-0 items-center justify-center rounded hover:bg-active',
  field: 'flex h-8 w-full min-w-0 items-center gap-2 rounded px-2 text-left text-[13px] hover:bg-hover',
  chip: 'flex h-7 max-w-48 shrink-0 items-center gap-1.5 rounded-md border border-border-strong px-2 text-xs hover:bg-hover',
}
