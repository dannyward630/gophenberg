// SPDX-License-Identifier: Apache-2.0

import type { RenderModalProps } from '@gophenberg/frontend-sdk/dataviews'
import { RenameBody, markLatest, useToaster } from '@gopherium/godmin'
import type { UnsavedText } from '@gopherium/godmin'
import { __ } from '@wordpress/i18n'

/** What the send of a new name for one item needs. */
export interface RenameSend<Item> {
	/** Returns the id an item is known by. */
	idOf: (item: Item) => string
	/** Writes the new name of an item. */
	rename: (item: Item, name: string) => Promise<unknown>
	/** Reloads the list once an item took its new name. */
	refresh: (item: Item) => Promise<unknown>
	/** The names failed writes left unsaved, offered again when the modal opens for the same item. */
	unsaved: UnsavedText
}

/** What the rename modal of a list takes beside the props DataViews hands a modal. */
interface RenameModalProps<Item> extends RenderModalProps<Item>, RenameSend<Item> {
	/** Returns the name an item carries now. */
	nameOf: (item: Item) => string
}

/**
 * Returns the send of a new name for one item, which closes the dialog at once and toasts how the write ended.
 * @param item - The item.
 * @param close - Takes the dialog away.
 * @param send - The id reader, the write, the refresh and the unsaved names.
 * @returns The send, given the name as typed.
 */
export function useRenameSend<Item>(item: Item, close: (() => void) | undefined, send: RenameSend<Item>) {
	const toaster = useToaster()
	const { idOf, rename, refresh, unsaved } = send
	const id = idOf(item)
	return (name: string) => {
		close?.()
		unsaved.delete(id)
		const stillLatest = markLatest(unsaved, id)
		void rename(item, name).then(
			async () => {
				await refresh(item)
				toaster.show(__('Name updated.', 'gophenberg'))
			},
			(failure: Error) => {
				if (stillLatest()) {
					unsaved.set(id, name)
				}
				toaster.show(failure.message)
			},
		)
	}
}

/**
 * Renders the modal writing a new name for one item, closed the moment Save is pressed, its failure in a toast.
 * @param props - The item, the handler closing the modal, the name and id readers, the write, the refresh and the
 *   unsaved names.
 * @returns The rename form.
 */
export function RenameModal<Item>(props: RenameModalProps<Item>) {
	const { items: [item], closeModal, nameOf } = props
	const save = useRenameSend(item, closeModal, props)
	return (
		<RenameBody
			name={props.unsaved.get(props.idOf(item)) ?? nameOf(item)}
			fieldLabel={__('Name', 'gophenberg')}
			submitLabel={__('Save', 'gophenberg')}
			cancelLabel={__('Cancel', 'gophenberg')}
			onSubmit={save}
			onCancel={closeModal}
		/>
	)
}
