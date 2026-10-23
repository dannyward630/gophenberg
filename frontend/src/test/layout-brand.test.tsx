// SPDX-License-Identifier: Apache-2.0

import { http, HttpResponse, server } from '@gophenberg/frontend-sdk/testing'
import { setViewport, textClasses } from '@gopherium/godmin/testing'
import { screen, within } from '@testing-library/react'
import { beforeEach, expect, test } from 'vitest'

import { renderAt } from './render'

beforeEach(() => {
	server.use(http.get('/api/content', () => HttpResponse.json({ items: [], total: 0 })))
})

/**
 * Returns the bar a narrow viewport draws in place of the sidebar.
 * @returns The top bar element.
 */
async function topBar(): Promise<HTMLElement> {
	const menu = await screen.findByRole('button', { name: 'Open navigation panel' })
	return menu.closest('.godmin-layout__topbar') as HTMLElement
}

test('names the site beside the menu button of a phone', async () => {
	setViewport({ matches: true })

	renderAt('/content/post')

	const brand = within(await topBar()).getByText('Gophenberg')
	expect([...brand.classList]).toEqual(textClasses('heading-lg'))
})

test('keeps the brand of the phone bar out of the heading outline', async () => {
	setViewport({ matches: true })

	renderAt('/content/post')

	within(await topBar()).getByText('Gophenberg')
	expect(screen.queryByRole('heading', { name: 'Gophenberg' })).not.toBeInTheDocument()
})
