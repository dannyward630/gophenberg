// SPDX-License-Identifier: Apache-2.0

import '@testing-library/jest-dom/vitest'
import { Frame } from '@gopherium/godmin'
import { Navigation, useFrameLocation } from '@gopherium/godmin/router'
import { installTestEnvironment as installGodminTestEnvironment } from '@gopherium/godmin/testing'
import { can, sessionQueryKey, useSession } from '@gopherium/react-auth'
import { installTestEnvironment as installAuthTestEnvironment, seedSession } from '@gopherium/react-auth/testing'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
	Link,
	Outlet,
	RouterProvider,
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
} from '@tanstack/react-router'
import { render } from '@testing-library/react'

import type { FrontendPlugin, MenuItem, Session } from './index'

export { HttpResponse, http, server } from '@gopherium/react-auth/testing'

/**
 * Installs global stubs and vitest lifecycle hooks for the test environment.
 */
export function installTestEnvironment() {
	installGodminTestEnvironment()
	installAuthTestEnvironment()
}

/**
 * Renders the frame the admin draws around a plugin's screens: the sidebar holding the plugin's rows, and the canvas.
 * @param props - The rows the plugin adds to the menu.
 * @returns The frame element.
 */
function TestHost({ nav }: { nav: readonly MenuItem[] }) {
	const session = useSession().data
	return (
		<Frame.Root location={useFrameLocation()}>
			<Frame.Sidebar>
				<Navigation
					title="Test host"
					items={nav}
					home={<Link to="/" />}
					can={(capability) => can(session, capability)}
				/>
			</Frame.Sidebar>
			<Frame.Canvas>
				<Outlet />
			</Frame.Canvas>
		</Frame.Root>
	)
}

/**
 * Renders the given frontend plugin mounted at a specific route path, with the signed in account the test seeds.
 * @param plugin - The frontend plugin whose nav and routes are mounted.
 * @param path - The initial router path to render at.
 * @param options - The account the plugin's screens read, null for none, left out to ask the server.
 */
export function renderPluginAt(plugin: FrontendPlugin, path: string, options: { session?: Session | null } = {}) {
	const rootRoute = createRootRoute({
		component: function TestHostFrame() {
			return <TestHost nav={plugin.nav} />
		},
	})
	const homeRoute = createRoute({
		getParentRoute: () => rootRoute,
		path: '/',
		component: function TestHostHome() {
			return <p>Test host home</p>
		},
	})
	const routeTree = rootRoute.addChildren([
		homeRoute,
		...plugin.routes(rootRoute),
	])
	const router = createRouter({
		routeTree,
		history: createMemoryHistory({ initialEntries: [path] }),
	})
	const client = new QueryClient({
		defaultOptions: { queries: { retry: false } },
	})
	if (options.session !== undefined) {
		client.setQueryDefaults(sessionQueryKey, { staleTime: Infinity })
		seedSession(client, options.session)
	}
	render(
		<QueryClientProvider client={client}>
			<RouterProvider router={router} />
		</QueryClientProvider>,
	)
}
