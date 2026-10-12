import { expect, test } from '@playwright/test'

import { comment, createIssue, createWorkspace, invite, signUp, unique } from './helpers'

test('sign up, create an issue and work on it', async ({ page }) => {
  const id = unique()
  await signUp(page, 'Ada Lovelace', `ada-${id}@example.com`)
  await createWorkspace(page, 'Acme', `acme-${id}`)
  await expect(page.getByText('No issues in Acme yet')).toBeVisible()

  await createIssue(page, 'Fix the login redirect', 'Happens after a password reset.')
  const row = page.getByRole('link', { name: 'Fix the login redirect' })
  await expect(row).toBeVisible()
  // It gets its number once the server has it.
  await expect(page.getByText('ACM-1', { exact: true })).toBeVisible()

  await row.click()
  await expect(page).toHaveURL(/\/issue\/ACM-1$/)
  await expect(page.getByLabel('Issue title')).toHaveValue('Fix the login redirect')

  const properties = page.getByRole('complementary', { name: 'Properties' })
  await expect(properties.getByRole('button', { name: /estimate/i })).toHaveCount(0)
  await properties.getByRole('button', { name: /Change status/ }).click()
  await page.getByRole('menuitem', { name: 'In Progress' }).click()
  await expect(properties.getByRole('button', { name: /Status: In Progress/ })).toBeVisible()

  await comment(page, 'On it.')
  const posted = page.getByRole('article', { name: 'Comment by Ada Lovelace' })
  await expect(posted).toContainText('On it.')
  await expect(page.getByText('changed status from Backlog to In Progress')).toBeVisible()

  // All of it is on the server, not just in this tab.
  await page.reload()
  await expect(posted).toContainText('On it.')
  await expect(properties.getByRole('button', { name: /Status: In Progress/ })).toBeVisible()
})

test("teammates see each other's changes as they happen", async ({ page, browser, baseURL }) => {
  const id = unique()
  const urlKey = `acme-${id}`
  const bobEmail = `bob-${id}@example.com`
  await signUp(page, 'Ada Lovelace', `ada-${id}@example.com`)
  await createWorkspace(page, 'Acme', urlKey)
  await invite(page, urlKey, bobEmail)

  // Bob accepts by signing in, in a browser of his own.
  const bobContext = await browser.newContext({ baseURL })
  const bob = await bobContext.newPage()
  await signUp(bob, 'Bob Builder', bobEmail)
  await bob.goto(`/${urlKey}/team/ACM/active`)
  await expect(bob.getByText('No issues in Acme yet')).toBeVisible()

  // What Ada creates shows up for Bob without a reload.
  await page.goto(`/${urlKey}/team/ACM/active`)
  await createIssue(page, 'Ship the beta')
  const forBob = bob.getByRole('link', { name: 'Ship the beta' })
  await expect(forBob).toBeVisible()

  // And his comment reaches her inbox.
  await forBob.click()
  await comment(bob, 'Looks good to me.')
  const sidebar = page.getByRole('navigation', { name: 'Workspace' })
  await expect(sidebar.getByLabel('1 unread')).toBeVisible()
  await sidebar.getByRole('link', { name: /Inbox/ }).click()
  await expect(page.getByText('Bob Builder commented')).toBeVisible()

  await bobContext.close()
})

test('search finds words in descriptions', async ({ page }) => {
  const id = unique()
  const urlKey = `acme-${id}`
  await signUp(page, 'Ada Lovelace', `ada-${id}@example.com`)
  await createWorkspace(page, 'Acme', urlKey)
  await createIssue(page, 'Invoice totals are wrong', 'Rounding of the quarterly tax line.')
  await createIssue(page, 'Update the logo')
  // Search runs on the server, so wait until both issues are saved there.
  await expect(page.getByText('ACM-2', { exact: true })).toBeVisible()

  await page.goto(`/${urlKey}/search`)
  await page.getByLabel('Search issues').fill('quarterly')
  const results = page.getByRole('list', { name: 'Search results' })
  await expect(results.getByRole('link')).toHaveCount(1)
  await expect(results.getByRole('link', { name: /Invoice totals are wrong/ })).toBeVisible()
  await expect(results.locator('mark')).toHaveText('quarterly')
})

test('someone removed from a workspace loses it at once', async ({ page, browser, baseURL }) => {
  const id = unique()
  const urlKey = `acme-${id}`
  const bobEmail = `bob-${id}@example.com`
  await signUp(page, 'Ada Lovelace', `ada-${id}@example.com`)
  await createWorkspace(page, 'Acme', urlKey)
  await invite(page, urlKey, bobEmail)

  const bobContext = await browser.newContext({ baseURL })
  const bob = await bobContext.newPage()
  await signUp(bob, 'Bob Builder', bobEmail)
  await bob.goto(`/${urlKey}/team/ACM/active`)
  await expect(bob.getByRole('heading', { name: 'Acme' })).toBeVisible()

  // Ada removes him; the app asks her to confirm.
  page.once('dialog', (dialog) => void dialog.accept())
  await page.getByRole('button', { name: 'Remove Bob Builder' }).click()
  await expect(page.getByRole('button', { name: 'Remove Bob Builder' })).toBeHidden()

  // Bob is told, and since this was his only workspace, offered to make one.
  await expect(bob.getByText('You no longer have access to Acme.')).toBeVisible()
  await expect(bob.getByRole('heading', { name: 'Create a workspace' })).toBeVisible()

  await bobContext.close()
})
