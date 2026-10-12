import { useToastStore } from '@/stores/toasts'

/** Copies text to the clipboard and says so. `what` names it: "Link copied". */
export function useCopy() {
  const toasts = useToastStore()

  return async function copy(text: string, what: string) {
    try {
      await navigator.clipboard.writeText(text)
      toasts.show(`${what} copied`)
    } catch {
      // The browser can refuse, e.g. when the page isn't focused.
      toasts.show("Couldn't copy to the clipboard")
    }
  }
}
