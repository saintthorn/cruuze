import { expect, test } from '@playwright/test'

import { createIssue, createWorkspace, signUp, unique } from './helpers'

test('an admin can delete a team, and everything in it goes', async ({ page }) => {
  const id = unique()
  const urlKey = `acme-${id}`
  await signUp(page, 'Ada Lovelace', `ada-${id}@example.com`)
  await createWorkspace(page, 'Acme', urlKey)
  await createIssue(page, 'Goes with the team')
  await expect(page.getByText('ACM-1', { exact: true })).toBeVisible()

  await page.goto(`/${urlKey}/settings/teams/ACM`)
  page.once('dialog', (dialog) => {
    expect(dialog.message()).toContain('Its 1 issue, statuses and cycles are deleted.')
    void dialog.accept()
  })
  await page.getByRole('button', { name: 'Delete Acme' }).click()

  await expect(page).toHaveURL(new RegExp(`/${urlKey}/settings/teams$`))
  await expect(page.getByRole('link', { name: 'Issues', exact: true })).toHaveCount(0)
  await expect(page.getByText('No teams')).toBeVisible()
  await page.goto(`/${urlKey}/search`)
  await page.getByLabel('Search issues').fill('Goes with the team')
  await expect(page.getByText('No matches for')).toBeVisible()
})
