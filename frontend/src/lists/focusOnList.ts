// SPDX-License-Identifier: Apache-2.0

import type { ListRun } from '@gopherium/godmin'
import { useMemo } from 'react'
import type { RefObject } from 'react'

/** Finds the dialog holding the focus. */
const DIALOG_IN_FOCUS = '[role="dialog"]:focus-within'

/**
 * Reports whether a dialog holds the focus, which a write finished in the background leaves where it is.
 * @returns Whether the focus sits inside a dialog.
 */
export function focusInDialog(): boolean {
	return document.querySelector(DIALOG_IN_FOCUS) !== null
}

/**
 * Reports whether the reader is still on the list, the focus on the page, inside the list or in the dialog that ran.
 * @param list - The list region.
 * @param startedIn - The dialog that held the focus as the run started, none for a run started outside every dialog.
 * @returns Whether a finished run may move the focus to the list.
 */
function readerOnList(list: HTMLElement | null, startedIn: Element | null): boolean {
	const active = document.activeElement
	return active === document.body || [list, startedIn].some((holder) => holder !== null && holder.contains(active))
}

/**
 * Returns the list run that moves the focus to the list only while the reader is still on it, never out of a dialog
 * opened since.
 * @param run - What the list does around a run.
 * @param region - The list region a finished run focuses.
 * @returns The run, the same for as long as the screen stays.
 */
export function useFocusOnList(run: ListRun, region: RefObject<HTMLElement | null>): ListRun {
	return useMemo(
		() => ({
			onStart: () => {
				const settle = run.onStart()
				const startedIn = document.querySelector(DIALOG_IN_FOCUS)
				return (moved) => settle(moved && readerOnList(region.current, startedIn))
			},
		}),
		[run, region],
	)
}
