// SPDX-License-Identifier: Apache-2.0

import { expect, test as setup } from '@playwright/test'

import { authorAuthFile, editorAuthFile, memberAuthFile, seededRoles } from '../env'

setup('logs in as the seeded editor and stores the session', async ({ page }) => {
	await page.goto('/admin/')

	await page.getByLabel('Email').fill(seededRoles.editor.email)
	await page.getByLabel('Password').fill(seededRoles.editor.password)
	await page.getByRole('button', { name: 'Log in' }).click()

	await expect(page.getByRole('heading', { name: 'Posts', level: 2 })).toBeVisible()
	await page.context().storageState({ path: editorAuthFile })
})

setup('logs in as the seeded author and stores the session', async ({ page }) => {
	await page.goto('/admin/')

	await page.getByLabel('Email').fill(seededRoles.author.email)
	await page.getByLabel('Password').fill(seededRoles.author.password)
	await page.getByRole('button', { name: 'Log in' }).click()

	await expect(page.getByRole('heading', { name: 'Posts', level: 2 })).toBeVisible()
	await page.context().storageState({ path: authorAuthFile })
})

setup('logs in as the seeded member and stores the session', async ({ page }) => {
	await page.goto('/admin/')

	await page.getByLabel('Email').fill(seededRoles.member.email)
	await page.getByLabel('Password').fill(seededRoles.member.password)
	await page.getByRole('button', { name: 'Log in' }).click()

	await expect(page.getByText('Sorry, you are not allowed to access this page.')).toBeVisible()
	await page.context().storageState({ path: memberAuthFile })
})
