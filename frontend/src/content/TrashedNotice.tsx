// SPDX-License-Identifier: Apache-2.0

import { Notice, speak } from '@gophenberg/frontend-sdk'
import { useToaster } from '@gopherium/godmin'
import { __, _x, sprintf } from '@wordpress/i18n'
import { useMutation, useQueryClient } from '@tanstack/react-query'

import { useRefresh } from './actions'
import { restorePost } from './api'
import type { PostDetail } from './api'

/**
 * Renders the notice that a post is in the trash, with the control returning it to draft.
 * @param props - The post in the trash and the name it is known by.
 * @returns The notice.
 */
export function TrashedNotice({ postId, title }: { postId: string, title: string }) {
	const client = useQueryClient()
	const refresh = useRefresh()
	const toaster = useToaster()
	const restore = useMutation({
		mutationFn: () => restorePost(postId),
		onSuccess: async () => {
			await Promise.all([client.invalidateQueries({ queryKey: ['post', postId], exact: true }), refresh()])
			toaster.show(sprintf(__('"%s" has been restored.', 'gophenberg'), title))
		},
		onError: () => client.invalidateQueries({ queryKey: ['post', postId], exact: true }),
	})
	const speakFailure = (error: Error) => {
		if (client.getQueryData<PostDetail>(['post', postId])?.status === 'trash') {
			speak(error.message, 'assertive')
		}
	}
	const message =
		restore.error?.message ?? __('This item is in the trash. Restore it first to work on it again.', 'gophenberg')
	return (
		<Notice.Root intent={restore.isError ? 'error' : 'warning'}>
			<Notice.Description>{message}</Notice.Description>
			<Notice.Actions>
				<Notice.ActionButton
					loading={restore.isPending}
					onClick={() => restore.mutate(undefined, { onError: speakFailure })}
				>
					{_x('Restore', 'trash', 'gophenberg')}
				</Notice.ActionButton>
			</Notice.Actions>
		</Notice.Root>
	)
}
