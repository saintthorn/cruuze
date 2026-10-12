import { expect, test } from '@playwright/test'

import { createIssue, createWorkspace, signUp, unique } from './helpers'

test('with work assigned, the workspace opens on Focus and the work can be started there', async ({ page }) => {
  const id = unique()
  const urlKey = `acme-${id}`
  await signUp(page, 'Ada Lovelace', `ada-${id}@example.com`)
  // With nothing assigned yet, a workspace opens on its team's issues.
  await createWorkspace(page, 'Acme', urlKey)

  await createIssue(page, 'Ship the outbox')
  await expect(page.getByText('ACM-1', { exact: true })).toBeVisible()
  await page.getByRole('link', { name: 'Ship the outbox' }).click()
  const properties = page.getByRole('complementary', { name: 'Properties' })
  await properties.getByRole('button', { name: 'Unassigned. Assign' }).click()
  await page.getByRole('menuitem', { name: 'Ada Lovelace' }).click()
  await expect(properties.getByRole('button', { name: /Assigned to Ada Lovelace/ })).toBeVisible()

  await page.goto(`/${urlKey}`)
  await expect(page).toHaveURL(new RegExp(`/${urlKey}/focus$`))
  await expect(page.getByRole('heading', { level: 1, name: 'Focus' })).toBeVisible()

  // The one thing to do is the card at the top; the same issue is queued below.
  const upNext = page.getByRole('region', { name: 'Up next', exact: true })
  await expect(upNext.getByRole('link', { name: 'Ship the outbox' })).toBeVisible()
  await expect(page.getByRole('region', { name: /^Up next 1$/ }).getByRole('link', { name: 'Ship the outbox' })).toBeVisible()

  await upNext.getByRole('button', { name: 'Start Ship the outbox' }).click()
  const now = page.getByRole('region', { name: 'Now', exact: true })
  await expect(now.getByRole('link', { name: 'Ship the outbox' })).toBeVisible()
  await expect(now.getByText('In Progress')).toBeVisible()
  await expect(page.getByRole('region', { name: /^In progress 1$/ }).getByRole('link', { name: 'Ship the outbox' })).toBeVisible()

  await now.getByRole('button', { name: 'Start timer' }).click()
  await expect(now.getByRole('button', { name: 'Stop timer' })).toBeVisible()
  await now.getByRole('button', { name: 'Stop timer' }).click()
  await expect(now.getByRole('button', { name: 'Start timer' })).toBeVisible()
})
