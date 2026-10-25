// SPDX-License-Identifier: Apache-2.0

import { InputControl } from '@gophenberg/frontend-sdk'
import type { RenderModalProps } from '@gophenberg/frontend-sdk/dataviews'
import { FormBody, markLatest, useToaster } from '@gopherium/godmin'
import type { UnsavedText } from '@gopherium/godmin'
import { useQueryClient } from '@tanstack/react-query'
import type { QueryClient } from '@tanstack/react-query'
import { __, _x, sprintf } from '@wordpress/i18n'
import { useState } from 'react'

import { RenameModal } from '../lists/RenameModal'
import { useCloseWhileOpen } from '../lists/closeWhileOpen'
import { useRefresh } from './actions'
import { createPost, fetchPost, renamePost } from './api'
import type { Post } from './api'
import { shownValues } from './conditions'
import { holdWrite, writesSettled } from './postWrites'
import type { ContentField } from './types'
import { useContentType } from './useContentType'

/**
 * Makes a draft holding the title given and the content, excerpt, parent and the field values of an item the server
 * writes, the ones its conditions show less the Linked from fields it only reads, read once every write still in
 * flight to the item has answered.
 * @param client - The query client holding the writes in flight.
 * @param post - The item to copy.
 * @param title - The title of the copy.
 * @param declared - The fields the type declares, judging which values the rules show.
 * @returns The copy.
 */
async function duplicated(client: QueryClient, post: Post, title: string, declared: ContentField[]): Promise<Post> {
	const fallback = __('The item could not be duplicated.', 'gophenberg')
	await writesSettled(client, post.id)
	const source = await fetchPost(post.id, fallback)
	const draft = {
		title,
		content: source.content,
		excerpt: source.excerpt,
		parentId: source.parentId ?? undefined,
		fields: shownValues(declared, source.fields),
	}
	return createPost(post.type, draft, fallback)
}

/** What a row modal of the posts list takes beside the props DataViews hands a modal. */
interface PostModalProps extends RenderModalProps<Post> {
	/** The text failed writes left unsaved, offered again when the modal opens for the same item. */
	unsaved: UnsavedText
}

/**
 * Renders the modal copying one item into a new draft under the title given.
 * @param props - The item, the handler closing the modal and the unsaved titles.
 * @returns The duplicate form.
 */
export function DuplicateModal({ items: [post], closeModal, unsaved }: PostModalProps) {
	const client = useQueryClient()
	const refresh = useRefresh()
	const toaster = useToaster()
	const { fields } = useContentType()
	const [title, setTitle] = useState(
		() => unsaved.get(post.id) ?? sprintf(_x('%s (Copy)', 'post', 'gophenberg'), post.title),
	)
	const [busy, setBusy] = useState(false)
	const close = useCloseWhileOpen(closeModal)
	const duplicate = () => {
		setBusy(true)
		const stillLatest = markLatest(unsaved, post.id)
		void duplicated(client, post, title, fields)
			.then(
				async () => {
					if (stillLatest()) {
						unsaved.delete(post.id)
					}
					await refresh()
					toaster.show(sprintf(__('"%s" successfully created.', 'gophenberg'), toaster.name(title)))
				},
				(failure: Error) => {
					if (stillLatest()) {
						unsaved.set(post.id, title)
					}
					toaster.show(failure.message)
				},
			)
			.finally(close)
	}
	return (
		<FormBody
			kind="copy"
			submitLabel={_x('Duplicate', 'action label', 'gophenberg')}
			cancelLabel={__('Cancel', 'gophenberg')}
			busy={busy}
			onSubmit={duplicate}
			onCancel={closeModal}
		>
			<InputControl label={__('Title', 'gophenberg')} value={title} onChange={(event) => setTitle(event.target.value)} />
		</FormBody>
	)
}

/**
 * Writes the title given to an item, its version read once every write still in flight to the item has answered.
 * @param client - The query client holding the writes in flight.
 * @param post - The item to rename.
 * @param title - The new title.
 * @returns The renamed item.
 */
async function renamed(client: QueryClient, post: Post, title: string): Promise<Post> {
	await writesSettled(client, post.id)
	return renamePost(post.id, title)
}

/**
 * Renders the modal writing a new title for one item, closed the moment Save is pressed, its failure in a toast.
 * @param props - The item, the handler closing the modal and the unsaved titles.
 * @returns The rename form.
 */
export function RenamePostModal(props: PostModalProps) {
	const client = useQueryClient()
	const refresh = useRefresh()
	return (
		<RenameModal
			{...props}
			nameOf={(post) => post.title}
			idOf={(post) => post.id}
			rename={(post, title) => holdWrite(client, post.id, renamed(client, post, title))}
			refresh={(post) => refresh([post.id])}
		/>
	)
}
