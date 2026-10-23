// SPDX-License-Identifier: Apache-2.0

import { NavigationScreen } from '@gophenberg/frontend-sdk'
import type { FrontendPlugin } from '@gophenberg/frontend-sdk'
import { createRoute } from '@tanstack/react-router'

/**
 * Renders the sub menu the section opens.
 * @returns The section's sidebar screen.
 */
function SectionSidebar() {
	return (
		<NavigationScreen title="Section">
			<p>section sidebar</p>
		</NavigationScreen>
	)
}

/**
 * Renders the page the section opens.
 * @returns The section's canvas.
 */
function SectionCanvas() {
	return <p>section canvas</p>
}

/** A plugin whose one row opens a sub menu, the way a plugin brings one into the admin. */
export const sectionPlugin: FrontendPlugin = {
	id: 'section',
	nav: [{ label: 'Section', to: '/section' }],
	routes: (parent) => [
		createRoute({
			getParentRoute: () => parent,
			path: '/section',
			staticData: { Sidebar: SectionSidebar },
			component: SectionCanvas,
		}),
	],
}
