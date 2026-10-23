// SPDX-License-Identifier: Apache-2.0

import { screen, within } from '@testing-library/react'
import { expect, test } from 'vitest'

import { renderAt } from './render'

test('shows the app version in the sidebar', async () => {
	renderAt('/', undefined, '2.5.1')

	expect(await screen.findByText('v2.5.1')).toBeInTheDocument()
})

test('notes the version under the account panel, in the sidebar footer outside the menu', async () => {
	renderAt('/', undefined, '2.5.1')

	const note = await screen.findByText('v2.5.1')
	const footer = note.closest('.godmin-sidebar__footer') as HTMLElement
	expect(note).toHaveClass('godmin-sidebar__footer-note')
	expect(within(footer).getByRole('button', { name: 'Log out' })).toBeInTheDocument()
	const sidebar = screen.getByRole('region', { name: 'Navigation' })
	expect(sidebar).toContainElement(footer)
	expect(within(within(sidebar).getByRole('list')).queryByText('v2.5.1')).toBeNull()
})

test('omits the version when it is unavailable', async () => {
	renderAt('/', undefined, null)

	await screen.findByRole('heading', { name: 'Gophenberg' })
	expect(screen.queryByText(/^v\d/)).toBeNull()
})
