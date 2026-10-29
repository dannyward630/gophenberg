// SPDX-License-Identifier: Apache-2.0

import { setViewport } from '@gopherium/godmin/testing'
import { LIST_CHROME_DOMAIN, listChromeCatalogFor, useToaster } from '@gopherium/godmin'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { resetLocaleData, setLocaleData } from '@wordpress/i18n'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, onTestFinished, test } from 'vitest'

import { AdminToaster } from '../toasts'
import { renderAt } from './render'

/**
 * Renders a control raising one toast inside the admin toast region.
 * @returns The rendered region.
 */
function renderToast() {
	/**
	 * Renders the control raising the toast.
	 * @returns The control element.
	 */
	function Raise() {
		const toaster = useToaster()
		return <button onClick={() => toaster.show('Saved.')}>raise</button>
	}
	return render(
		<QueryClientProvider client={new QueryClient()}>
			<AdminToaster>
				<Raise />
			</AdminToaster>
		</QueryClientProvider>,
	)
}

test('names the menu button in the language the reader loaded, from the words godmin ships', async () => {
	setLocaleData(await listChromeCatalogFor('es-ES'), LIST_CHROME_DOMAIN)
	onTestFinished(() => resetLocaleData({}, LIST_CHROME_DOMAIN))
	setViewport({ matches: true })

	renderAt('/')

	expect(
		await screen.findByRole('button', { name: 'Abrir el panel de navegación' }),
	).toBeInTheDocument()
})

test('describes how to dismiss a toast in the language the reader loaded', async () => {
	setLocaleData({ 'Dismiss this notice': ['Descartar este aviso'] }, 'gophenberg')
	onTestFinished(() => resetLocaleData({}, 'gophenberg'))
	renderToast()

	await userEvent.click(screen.getByRole('button', { name: 'raise' }))

	expect(
		await screen.findByRole('button', { name: 'Saved.' }),
	).toHaveAccessibleDescription('Descartar este aviso')
})
