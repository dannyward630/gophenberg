// SPDX-License-Identifier: Apache-2.0

import { defaultUser, sessionOk } from '@gopherium/react-auth/testing'
import { createRoute } from '@tanstack/react-router'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { delay } from 'msw'
import { expect, test } from 'vitest'

import { NavigationScreen, can, useSession } from '../index'
import type { FrontendPlugin, MenuItem } from '../index'
import { http, renderPluginAt, server } from '../testing'

/**
 * Renders whether the signed in account may manage events, and whether the session is being asked of the server.
 * @returns The reported readings.
 */
function EventsCanvas() {
	const session = useSession()
	return (
		<>
			<p>{`manages events:${String(can(session.data, 'events.manage'))}`}</p>
			<p>{`asking the server:${String(session.isFetching)}`}</p>
		</>
	)
}

const eventsPlugin: FrontendPlugin = {
	id: 'events',
	nav: [],
	routes: (parent) => [
		createRoute({
			getParentRoute: () => parent,
			path: '/events',
			component: EventsCanvas,
		}),
	],
}

/** The rows of the section plugin, one open to every reader and one asking for a capability. */
const sectionRows: MenuItem[] = [
	{ label: 'Section', to: '/section' },
	{ label: 'Section settings', to: '/section-settings', capability: 'section.manage' },
]

const sectionPlugin: FrontendPlugin = {
	id: 'section',
	nav: sectionRows,
	routes: (parent) => [
		createRoute({
			getParentRoute: () => parent,
			path: '/section',
			staticData: {
				Sidebar: function SectionSidebar() {
					return (
						<NavigationScreen title="Section">
							<p>section sidebar</p>
						</NavigationScreen>
					)
				},
			},
			component: function SectionCanvas() {
				return <p>section canvas</p>
			},
		}),
		createRoute({
			getParentRoute: () => parent,
			path: '/section-settings',
			component: function SectionSettings() {
				return <p>section settings</p>
			},
		}),
	],
}

/** A signed in account carrying no capability. */
const reader = { ...defaultUser, role: 'reader', capabilities: [] }

/**
 * Returns the sidebar the test host draws.
 * @returns The region named Navigation.
 */
function sidebar(): Promise<HTMLElement> {
	return screen.findByRole('region', { name: 'Navigation' })
}

test('draws the plugin rows as a list in the real sidebar, under the host title', async () => {
	renderPluginAt(sectionPlugin, '/', { session: reader })

	expect(await screen.findByText('Test host home')).toBeInTheDocument()
	const shown = await sidebar()
	expect(within(shown).getByRole('heading', { level: 1, name: 'Test host' })).toBeInTheDocument()
	const rows = within(within(shown).getByRole('list')).getAllByRole('listitem')
	expect(rows.map((row) => row.textContent)).toEqual(['Section'])
	expect(document.querySelector('.godmin-layout__canvas')).toContainElement(screen.getByText('Test host home'))
})

test('shows a plugin row naming a capability to a session holding it', async () => {
	renderPluginAt(sectionPlugin, '/', { session: { ...reader, capabilities: ['section.manage'] } })

	expect(await within(await sidebar()).findByRole('link', { name: 'Section settings' })).toBeInTheDocument()
})

test('asks the server for the account when the test seeds none, showing the rows that account may open', async () => {
	server.use(sessionOk({ ...reader, capabilities: ['section.manage'] }))

	renderPluginAt(sectionPlugin, '/')

	expect(await within(await sidebar()).findByRole('link', { name: 'Section settings' })).toBeInTheDocument()
})

test('renders the section sidebar and canvas on a section route', async () => {
	renderPluginAt(sectionPlugin, '/section', { session: reader })

	const shown = await sidebar()
	expect(await within(shown).findByText('section sidebar')).toBeInTheDocument()
	expect(within(shown).getByRole('heading', { level: 1, name: 'Section' })).toBeInTheDocument()
	expect(screen.getByText('section canvas')).toBeInTheDocument()
})

test('takes Back on a plugin sub menu to the host home', async () => {
	renderPluginAt(sectionPlugin, '/section', { session: reader })

	fireEvent.click(await within(await sidebar()).findByRole('link', { name: 'Back' }))

	expect(await screen.findByText('Test host home')).toBeInTheDocument()
	const row = within(await sidebar()).getByRole('link', { name: 'Section' })
	await waitFor(() => expect(row).toHaveFocus())
})

test('renders a plugin screen with the session the test seeds', async () => {
	const session = { ...defaultUser, role: 'moderator', capabilities: ['events.manage'] }

	renderPluginAt(eventsPlugin, '/events', { session })

	expect(await screen.findByText('manages events:true')).toBeInTheDocument()
})

test('keeps the session the test seeds without asking the server for it', async () => {
	server.use(http.get('/api/auth/session', () => delay('infinite')))

	renderPluginAt(eventsPlugin, '/events', { session: { ...reader, capabilities: ['events.manage'] } })

	expect(await screen.findByText('asking the server:false')).toBeInTheDocument()
	expect(screen.getByText('manages events:true')).toBeInTheDocument()
})

test('renders a plugin screen signed out when the test seeds no account', async () => {
	renderPluginAt(eventsPlugin, '/events', { session: null })

	expect(await screen.findByText('manages events:false')).toBeInTheDocument()
})
