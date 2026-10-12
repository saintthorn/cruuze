import { rm } from 'node:fs/promises'

import { MAIL_DIR } from '../playwright.config'

/** Starts each run without the previous run's email. */
export default async function globalSetup() {
  await rm(MAIL_DIR, { recursive: true, force: true })
}
