// SPDX-License-Identifier: Apache-2.0

import { expect, test } from '@playwright/test'
import type { APIRequestContext, Page } from '@playwright/test'

import { baseURL, credentials } from '../env'

/** An account a test made for itself. */
interface MadeAccount {
	id: string
	name: string
	email: string
}

/** An account as the admin API lists it. */
interface ListedAccount {
	id: string
	role: string
}

/**
 * Creates an account under an address no other attempt uses.
 * @param request - The request context of the signed in administrator.
 * @param condition - The condition the account stands for, naming it.
 * @param role - The role the account starts under.
 * @returns The account.
 */
async function madeAccount(request: APIRequestContext, condition: string, role: string): Promise<MadeAccount> {
	const stamp = `${Date.now()}-${test.info().retry}`
	const email = `${condition}-${stamp}@example.com`
	const name = `${condition} ${stamp}`
	const answer = await request.post('/api/users', {
		data: { email, name, password: credentials.password, role },
	})
	expect(answer.status()).toBe(201)
	const { id } = (await answer.json()) as { id: string }
	return { id, name, email }
}

/**
 * Returns the role an account holds, as the admin API lists it.
 * @param request - The request context of the signed in administrator.
 * @param id - The account identifier.
 * @returns The role.
 */
async function roleOf(request: APIRequestContext, id: string): Promise<string | undefined> {
	const listed = await request.get('/api/users')
	expect(listed.ok()).toBe(true)
	return ((await listed.json()) as ListedAccount[]).find((account) => account.id === id)?.role
}

/**
 * Opens the role dialog of the account and picks the role.
 * @param page - The Users screen.
 * @param account - The account whose role changes.
 * @param label - The label of the role to pick.
 */
async function pickRole(page: Page, account: MadeAccount, label: string) {
	await page.getByRole('button', { name: `Change the role of ${account.name}` }).click()
	const dialog = page.getByRole('dialog', { name: 'Change role' })
	await dialog.getByRole('combobox', { name: 'Role' }).click()
	await page.getByRole('option', { name: label, exact: true }).click()
}

test('promotes an author to editor through the role dialog', async ({ page, request }) => {
	const promoted = await madeAccount(request, 'promoted', 'author')
	await page.goto('/admin/users')

	await pickRole(page, promoted, 'Editor')
	await page.getByRole('dialog', { name: 'Change role' }).getByRole('button', { name: 'Change role' }).click()

	await expect(page.locator('#root').getByText('Role changed.')).toBeVisible()
	await page.reload()
	await expect(page.getByRole('row').filter({ hasText: promoted.email })).toContainText('Editor')
})

test('offers exactly the roles the session may give', async ({ page, request }) => {
	const session = (await (await request.get('/api/auth/session')).json()) as { grantable: string[] }
	const offered = await madeAccount(request, 'offered', 'author')
	await page.goto('/admin/users')

	await page.getByRole('button', { name: `Change the role of ${offered.name}` }).click()
	await page.getByRole('dialog', { name: 'Change role' }).getByRole('combobox', { name: 'Role' }).click()

	expect(session.grantable).toEqual(['admin', 'editor', 'author', 'member'])
	await expect(page.getByRole('option')).toHaveText(['Administrator', 'Editor', 'Author', 'Member'])
})

test('explains a role write refused after the reader lost the right to give roles', async ({ browser, request }) => {
	const lowered = await madeAccount(request, 'lowered', 'admin')
	const kept = await madeAccount(request, 'kept', 'author')
	const context = await browser.newContext({ baseURL, storageState: { cookies: [], origins: [] } })
	const page = await context.newPage()
	await page.goto('/admin/')
	await page.getByLabel('Email').fill(lowered.email)
	await page.getByLabel('Password').fill(credentials.password)
	await page.getByRole('button', { name: 'Log in' }).click()
	await expect(page.getByRole('heading', { name: 'Posts', level: 2 })).toBeVisible()
	await page.goto('/admin/users')

	await pickRole(page, kept, 'Editor')
	const demoted = await request.put(`/api/users/${lowered.id}/role`, { data: { role: 'editor' } })
	expect(demoted.ok()).toBe(true)
	const dialog = page.getByRole('dialog', { name: 'Change role' })
	await dialog.getByRole('button', { name: 'Change role' }).click()

	await expect(dialog.getByRole('alert')).toHaveText('The role could not be changed.')
	expect(await roleOf(request, kept.id)).toBe('author')
	await context.close()
})

test('refuses a role nobody declares', async ({ request }) => {
	const target = await madeAccount(request, 'undeclared', 'author')

	const answer = await request.put(`/api/users/${target.id}/role`, { data: { role: 'archivist' } })

	expect(answer.status()).toBe(422)
	const refusal = (await answer.json()) as { code: string, meta: { field: string } }
	expect(refusal.code).toBe('role_unknown')
	expect(refusal.meta.field).toBe('role')
	expect(await roleOf(request, target.id)).toBe('author')
})
