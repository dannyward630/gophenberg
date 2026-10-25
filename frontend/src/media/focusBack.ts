// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'

/** Finds the card of a grid or the row of a list that holds a file. */
const HOLDERS = '.dataviews-view-grid__card, .dataviews-view-list__item-wrapper'

/** Finds each element a dialog on a file opens from, inside the card or the row holding the file. */
const DOORS = {
	actions: '[aria-haspopup="menu"]',
	title: '.dataviews-title-field[role="button"]',
	picture: '.dataviews-view-grid__media[role="button"]',
	row: '.dataviews-view-list__item',
}

/** The element a dialog on a file opened from: its actions menu, the title or the picture of its card, or its row. */
export type Door = keyof typeof DOORS

/** One request to put the focus back on the element a dialog on a file opened from, a new one each time it is asked. */
interface FocusBack {
	/** The file. */
	id: number
	/** The element the dialog opened from. */
	door: Door
}

/**
 * Returns the part of a card a click opened a dialog from, read from the focus the click put on it.
 * @returns The picture while the focus sits on it, the title otherwise.
 */
export function clickedDoor(): Door {
	return (document.activeElement as Element).matches(DOORS.picture) ? 'picture' : 'title'
}

/**
 * Returns the element a dialog on a file opened from, found through the file wherever its card or row now sits.
 * @param asked - The file and the door.
 * @returns The door, the actions menu of the file once the layout draws no such door, none while the list draws no card
 *   or row of the file.
 */
function doorOf({ id, door }: FocusBack): HTMLElement | null {
	const marked = document.querySelector(`[data-media-item="${id}"]`)
	if (marked === null) {
		return null
	}
	const holder = marked.closest(HOLDERS) as Element
	return holder.querySelector<HTMLElement>(DOORS[door]) ?? (holder.querySelector(DOORS.actions) as HTMLElement)
}

/**
 * Returns the handler putting a focus the page lost back on the element a dialog on a file opened from, once the list
 * is drawn again, as WordPress's modal gives the focus back to what opened it, and on the list once the file left it.
 * @param busy - Whether the list reads the library, its cards then out of reach of the focus.
 * @param region - The list region.
 * @returns The handler, given the file and the element the dialog opened from.
 */
export function useFocusBack(busy: boolean, region: RefObject<HTMLElement | null>): (id: number, door: Door) => void {
	const [asked, setAsked] = useState<FocusBack>()
	const handled = useRef<FocusBack>(undefined)
	useEffect(() => {
		if (busy || asked === handled.current) {
			return
		}
		handled.current = asked
		if (document.activeElement === document.body) {
			const target = doorOf(asked as FocusBack) ?? (region.current as HTMLElement)
			target.focus()
		}
	}, [asked, busy, region])
	return useCallback((id: number, door: Door) => setAsked({ id, door }), [])
}
