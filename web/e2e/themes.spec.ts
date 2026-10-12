import { expect, test } from '@playwright/test'

import { createWorkspace, signUp, unique } from './helpers'

test('a chosen theme applies at once and is remembered', async ({ page }) => {
  const id = unique()
  const urlKey = `acme-${id}`
  await signUp(page, 'Ada Lovelace', `ada-${id}@example.com`)
  await createWorkspace(page, 'Acme', urlKey)

  const html = page.locator('html')
  await expect(html).toHaveAttribute('data-theme', 'midnight')
  // The editor-style status line belongs to the terminal theme alone.
  const statusLine = page.getByRole('contentinfo', { name: 'Status' })
  await expect(statusLine).toHaveCount(0)

  await page.goto(`/${urlKey}/settings/themes`)
  const themes = page.getByRole('radiogroup', { name: 'Theme' })
  await expect(themes.getByRole('radio')).toHaveCount(5)
  await expect(themes.getByRole('radio', { name: 'mode=midnight' })).toBeChecked()

  await themes.getByRole('radio', { name: 'mode=hacker' }).click()
  await expect(html).toHaveAttribute('data-theme', 'hacker')
  await expect(themes.getByRole('radio', { name: 'mode=hacker' })).toBeChecked()
  await expect(themes.getByRole('radio', { name: 'mode=midnight' })).not.toBeChecked()
  await expect(statusLine).toBeVisible()

  await page.reload()
  await expect(html).toHaveAttribute('data-theme', 'hacker')
  await expect(themes.getByRole('radio', { name: 'mode=hacker' })).toBeChecked()
  await expect(statusLine).toBeVisible()
})
