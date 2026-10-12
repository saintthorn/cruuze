import { ref } from 'vue'

import { api, errorMessage, unwrap } from '@/api/client'

/** Signing in by emailed link: the address, and asking the server to send to it. */
export function useMagicLink(initialError = '') {
  const email = ref('')
  /** The address a link was sent to, once one has been. */
  const sentTo = ref('')
  const sending = ref(false)
  const error = ref(initialError)

  async function submit() {
    error.value = ''
    sending.value = true
    try {
      const address = email.value.trim()
      await unwrap(api.POST('/auth/magic-link', { body: { email: address } }))
      sentTo.value = address
    } catch (err) {
      error.value = errorMessage(err)
    } finally {
      sending.value = false
    }
  }

  return { email, sentTo, sending, error, submit }
}
