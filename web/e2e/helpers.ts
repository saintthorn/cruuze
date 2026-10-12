import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'

import { expect, type Page } from '@playwright/test'

import { MAIL_DIR } from '../playwright.config'

/** A short string unique to this call, for emails and workspace URLs that must not collide. */
export function unique(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 6)
}

/**
 * The sign-in link most recently emailed to an address. The API under test
 * writes email to files instead of sending it.
 */
export async function signInLink(email: string): Promise<string> {
  const suffix = `-${email.toLowerCase()}.txt`
  for (let attempt = 0; attempt < 100; attempt++) {
    const files = (await readdir(MAIL_DIR).catch(() => [] as string[])).filter((f) => f.endsWith(suffix))
    // File names start with a timestamp: newest last.
    for (const file of files.sort().reverse()) {
      const text = await readFile(path.join(MAIL_DIR, file), 'utf8')
      const link = text.match(/https?:\/\/\S+\/auth\/verify#token=[\w-]+/)
      if (link) return link[0]
    }
    await new Promise((resolve) => setTimeout(resolve, 100))
  }
  throw new Error(`No sign-in email arrived for ${email}`)
}

/** Signs in through the magic-link flow. Use each address once per test: links are single-use. */
export async function signIn(page: Page, email: string) {
  await page.goto('/login')
  await page.getByLabel('Email').fill(email)
  await page.getByRole('button', { name: 'Continue with email' }).click()
  await expect(page.getByRole('heading', { name: 'Check your email' })).toBeVisible()
  await page.goto(await signInLink(email))
}

/** Signs in as someone new and gives them a name. */
export async function signUp(page: Page, name: string, email: string) {
  await signIn(page, email)
  await page.getByLabel('Full name').fill(name)
  await page.getByRole('button', { name: 'Continue', exact: true }).click()
}

/**
 * Creates a workspace from the page a new user lands on. It comes with one
 * team, named after the workspace and keyed by its first three letters.
 */
export async function createWorkspace(page: Page, name: string, urlKey: string) {
  await page.getByLabel('Workspace name').fill(name)
  await page.getByLabel('Workspace URL').fill(urlKey)
  await page.getByRole('button', { name: 'Create workspace' }).click()
  await page.waitForURL(`**/${urlKey}/team/*/active`)
}

export async function createIssue(page: Page, title: string, description = '') {
  await page.getByRole('navigation', { name: 'Workspace' }).getByRole('button', { name: 'New issue' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('button', { name: /estimate/i })).toHaveCount(0)
  await dialog.getByLabel('Issue title').fill(title)
  if (description) await dialog.getByLabel('Description').fill(description)
  await dialog.getByRole('button', { name: 'Create issue' }).click()
  await expect(dialog).toBeHidden()
}

export async function comment(page: Page, text: string) {
  await page.getByRole('textbox', { name: 'Leave a comment' }).click()
  await page.keyboard.type(text)
  await page.getByRole('button', { name: 'Comment', exact: true }).click()
}

/** Invites someone from the members page and waits for the invite to be listed. */
export async function invite(page: Page, urlKey: string, email: string) {
  await page.goto(`/${urlKey}/settings/members`)
  await page.getByLabel('Email address').fill(email)
  await page.getByRole('button', { name: 'Send invite' }).click()
  await expect(page.getByText(email)).toBeVisible()
}
