// SPDX-License-Identifier: Apache-2.0

import type { QueryClient, QueryKey } from '@tanstack/react-query'
import { act, screen } from '@testing-library/react'

import type { createAppRouter } from '../router'

/**
 * Leaves a list for the Settings screen and marks the rows it read stale, as time does on a live site.
 * @param router - The router the admin renders with.
 * @param client - The query client the admin renders with.
 * @param key - The query key the rows of the list are cached under.
 */
export async function leaveStale(router: ReturnType<typeof createAppRouter>, client: QueryClient, key: QueryKey) {
	await act(async () => {
		await router.navigate({ to: '/settings' })
	})
	await screen.findByRole('heading', { level: 2, name: 'Settings' })
	await client.invalidateQueries({ queryKey: key, refetchType: 'none' })
}
