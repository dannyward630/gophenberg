// SPDX-License-Identifier: Apache-2.0

import { http, HttpResponse, server } from '@gophenberg/frontend-sdk/testing'
import { useToaster } from '@gopherium/godmin'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { AdminToaster } from '../toasts'
import { siteSettings } from './siteSettings'

const LONG_TITLE = 'A title far longer than the forty five characters a toast once kept of a name'

/**
 * Renders the admin toast region around two controls, one raising a plain toast and one raising a toast with an action.
 */
function renderToaster() {
	/**
	 * Renders the controls raising a toast that names an item.
	 * @returns The controls.
	 */
	function Raise() {
		const toaster = useToaster()
		const undo = { label: 'Undo', onAct: () => {} }
		return (
			<>
				<button onClick={() => toaster.show(LONG_TITLE)}>raise</button>
				<button onClick={() => toaster.show(LONG_TITLE, undo)}>raise with undo</button>
			</>
		)
	}
	render(
		<QueryClientProvider client={new QueryClient()}>
			<AdminToaster>
				<Raise />
			</AdminToaster>
		</QueryClientProvider>,
	)
}

/**
 * Serves the list settings with the given toast settings in place of the defaults, noting each time they are asked.
 * @param toast - The toast settings the site picks.
 * @returns The asks the server answered.
 */
function serveToastSettings(toast: { toast_milliseconds?: number, toast_action_milliseconds?: number }): string[] {
	const asked: string[] = []
	server.use(
		http.get('/api/settings', () => {
			asked.push('settings')
			return HttpResponse.json({ ...siteSettings, ...toast })
		}),
	)
	return asked
}

test('shows the whole name of an item in a toast, however long', async () => {
	renderToaster()

	await userEvent.click(screen.getByRole('button', { name: 'raise' }))

	expect(await screen.findByText(LONG_TITLE, { selector: '.godmin-toast' })).toBeInTheDocument()
})

test('sends a toast away once the time the site serves runs out', async () => {
	const asked = serveToastSettings({ toast_milliseconds: 50 })
	renderToaster()
	await waitFor(() => expect(asked).toEqual(['settings']))

	await userEvent.click(screen.getByRole('button', { name: 'raise' }))

	expect(await screen.findByText(LONG_TITLE, { selector: '.godmin-toast' })).toBeInTheDocument()
	await waitFor(() => expect(screen.queryByText(LONG_TITLE, { selector: '.godmin-toast' })).toBeNull())
})

test('sends a toast with an action away once the time the site serves for one runs out', async () => {
	const asked = serveToastSettings({ toast_action_milliseconds: 50 })
	renderToaster()
	await waitFor(() => expect(asked).toEqual(['settings']))

	await userEvent.click(screen.getByRole('button', { name: 'raise with undo' }))

	expect(await screen.findByRole('button', { name: 'Undo' })).toBeInTheDocument()
	await waitFor(() => expect(screen.queryByRole('button', { name: 'Undo' })).toBeNull())
})

test('still raises a toast when the settings cannot be read', async () => {
	vi.spyOn(console, 'error').mockImplementation(() => {})
	const asked: string[] = []
	server.use(
		http.get('/api/settings', () => {
			asked.push('settings')
			return HttpResponse.json({ error: 'the store is down' }, { status: 500 })
		}),
	)
	renderToaster()
	await waitFor(() => expect(asked).toEqual(['settings']))

	await userEvent.click(screen.getByRole('button', { name: 'raise' }))

	expect(await screen.findByText(LONG_TITLE, { selector: '.godmin-toast' })).toBeInTheDocument()
})
