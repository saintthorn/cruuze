import { onBeforeUnmount, onMounted, ref } from 'vue'

const INTERVAL_MS = 10_000
const HISTORY = 16

/**
 * Measures the round trip to the API every few seconds while the component
 * is mounted and the page is visible. `latest` is in milliseconds, or null
 * when the last attempt failed.
 */
export function usePing() {
  const latest = ref<number | null>(null)
  const history = ref<number[]>([])
  let timer: ReturnType<typeof setInterval> | undefined

  async function measure() {
    if (document.hidden) return
    const start = performance.now()
    try {
      // The cheapest endpoint there is: no session, no database.
      const response = await fetch('/api/auth/config', { cache: 'no-store' })
      if (!response.ok) throw new Error(String(response.status))
      latest.value = Math.round(performance.now() - start)
      history.value = [...history.value, latest.value].slice(-HISTORY)
    } catch {
      latest.value = null
    }
  }

  onMounted(() => {
    void measure()
    timer = setInterval(() => void measure(), INTERVAL_MS)
  })
  onBeforeUnmount(() => clearInterval(timer))

  return { latest, history }
}
