// SPDX-License-Identifier: Apache-2.0

import { DropZone, mediaIcon, uploadIcon } from '@gophenberg/frontend-sdk'
import { DataViews } from '@gophenberg/frontend-sdk/dataviews'
import type { View } from '@gophenberg/frontend-sdk/dataviews'
import {
	ErrorNotice,
	FileButton,
	ListEmpty,
	Page,
	bulkNotes,
	paginationOf,
	runEach,
	useToaster,
} from '@gopherium/godmin'
import type { BulkFailure, BulkWords } from '@gopherium/godmin'
import { formatNumber } from '@gopherium/gottext'
import { useQuery } from '@tanstack/react-query'
import type { UseQueryResult } from '@tanstack/react-query'
import { __, _n, _x, sprintf } from '@wordpress/i18n'
import { useRef, useState } from 'react'
import type { RefObject } from 'react'

import { useListSettings } from '../settings/useListSettings'
import './media.css'
import { useMediaActions, useRefreshMedia } from './actions'
import { listMedia, mediaQueryKey, uploadMedia } from './api'
import type { MediaItem, MediaPage } from './api'
import { mediaFields } from './fields'

const PREVIEW_SIZE = 230

const EMPTY_PAGE: MediaPage = { items: [], total: 0, perPage: 0 }

const INITIAL_VIEW: View = {
	type: 'grid',
	fields: ['dimensions', 'filesize'],
	titleField: 'title',
	mediaField: 'thumbnail',
	search: '',
	page: 1,
	layout: { previewSize: PREVIEW_SIZE },
}

/**
 * Returns the kind a view is filtered to.
 * @param view - The view the library is shown through.
 * @returns The kind to ask for, empty for every kind.
 */
export function filteredKind(view: View): string {
	const chosen = view.filters?.find((filter) => filter.field === 'type')?.value
	return typeof chosen === 'string' ? chosen : ''
}

/**
 * Returns the reason a file was not stored, already in the reader's words.
 * @param failure - The file and what its upload failed with.
 * @returns The reason.
 */
function reasonOf(failure: BulkFailure<File>): string {
	return (failure.error as Error).message
}

/**
 * Returns the notice naming the files an upload could not store, one line per file when several were sent.
 * @param failures - The files that failed and what each failed with.
 * @param asked - How many files the upload was given.
 * @returns The reason alone for one file sent, else a line naming each failed file with its reason.
 */
function refusedLines(failures: BulkFailure<File>[], asked: number): string {
	if (asked === 1) {
		return reasonOf(failures[0])
	}
	const line = __('%(file)s: %(reason)s', 'gophenberg')
	return failures.map((failure) => sprintf(line, { file: failure.item.name, reason: reasonOf(failure) })).join('\n')
}

/**
 * Returns the words an upload to the library reads as.
 * @param name - Cuts a file name to the length a toast shows.
 * @returns The toast naming the one file stored or counting the files stored, and the notice for the refused ones.
 */
function uploadWords(name: (title: string) => string): BulkWords<File> {
	return {
		done: (count, only) =>
			only === undefined
				? sprintf(_n('Uploaded %s file.', 'Uploaded %s files.', count, 'gophenberg'), formatNumber(count))
				: sprintf(__('"%s" uploaded.', 'gophenberg'), name(only.name)),
		failed: refusedLines,
	}
}

/** The uploads running at once, how many files they were given and the files they could not store between them. */
interface UploadBatch {
	runs: number
	asked: number
	failures: BulkFailure<File>[]
}

/**
 * Returns the batch once another upload joins it, a fresh one when no upload was running.
 * @param held - The batch before the upload starts.
 * @param files - The files the upload was given.
 * @returns The batch counting the upload and its files.
 */
function joined(held: UploadBatch, files: File[]): UploadBatch {
	if (held.runs === 0) {
		return { runs: 1, asked: files.length, failures: [] }
	}
	return { ...held, runs: held.runs + 1, asked: held.asked + files.length }
}

/**
 * Returns the batch once one of its uploads ended.
 * @param held - The batch before the upload ended.
 * @param failures - The files the upload could not store.
 * @returns The batch holding one upload less and every failure so far.
 */
function ended(held: UploadBatch, failures: BulkFailure<File>[]): UploadBatch {
	return { ...held, runs: held.runs - 1, failures: [...held.failures, ...failures] }
}

/**
 * Returns the notice a finished batch reads as.
 * @param held - The batch whose last upload ended.
 * @returns The notice naming the refused files, none when every file was stored.
 */
function batchNotice(held: UploadBatch): string | undefined {
	return held.failures.length === 0 ? undefined : refusedLines(held.failures, held.asked)
}

/**
 * Holds the uploads to the library: a request per file, a toast per upload, one notice per batch running together.
 * @param region - The list region focused once a file landed.
 * @returns Whether an upload runs, the notice of the last uploads, and the handler taking the files.
 */
function useUpload(region: RefObject<HTMLDivElement | null>) {
	const toaster = useToaster()
	const refresh = useRefreshMedia()
	const batch = useRef<UploadBatch>({ runs: 0, asked: 0, failures: [] })
	const [busy, setBusy] = useState(false)
	const [notice, setNotice] = useState<string>()
	const finish = (failures: BulkFailure<File>[]) => {
		batch.current = ended(batch.current, failures)
		if (batch.current.runs === 0) {
			setBusy(false)
			setNotice(batchNotice(batch.current))
		}
	}
	const upload = async (files: File[]) => {
		batch.current = joined(batch.current, files)
		setBusy(true)
		setNotice(undefined)
		const outcome = await runEach(files, uploadMedia)
		if (outcome.done > 0) {
			await refresh()
		}
		const toast = bulkNotes(files, outcome, uploadWords(toaster.name)).toast
		if (toast !== undefined) {
			toaster.show(toast)
		}
		finish(outcome.failures)
		if (outcome.done > 0) {
			region.current?.focus()
		}
	}
	return { busy, notice, upload: (files: File[]) => void upload(files) }
}

/**
 * Returns the words of the header button adding files to the library.
 * @returns The button's words in the reader's language.
 */
function addLabel(): string {
	return __('Add Media File', 'gophenberg')
}

/**
 * Renders what the library shows when it holds no item.
 * @param props - Whether a search or a filter narrowed the library.
 * @returns The empty state, telling how to add a file when nothing narrowed the library.
 */
function MediaEmpty({ narrowed }: { narrowed: boolean }) {
	if (narrowed) {
		return <ListEmpty icon={mediaIcon} title={__('No media items found.', 'gophenberg')} />
	}
	const hint = sprintf(__('Add one with %(button)s.', 'gophenberg'), { button: addLabel() })
	return <ListEmpty icon={mediaIcon} title={__('No media items yet.', 'gophenberg')} hint={hint} />
}

/**
 * Renders the items of the library, or the failure of their read.
 * @param props - The query of the page, the view and what changes it, the ticked items and what changes them.
 * @returns The list, or the failure notice.
 */
function MediaList(props: {
	media: UseQueryResult<MediaPage>
	view: View
	onChangeView: (view: View) => void
	selection: string[]
	onChangeSelection: (selection: string[]) => void
}) {
	const actions = useMediaActions()
	const { media, view } = props
	if (media.isError) {
		return <ErrorNotice>{__('The media library could not be loaded.', 'gophenberg')}</ErrorNotice>
	}
	const page = media.data ?? EMPTY_PAGE
	return (
		<DataViews
			data={page.items}
			fields={mediaFields}
			actions={actions}
			view={view}
			onChangeView={props.onChangeView}
			selection={props.selection}
			onChangeSelection={props.onChangeSelection}
			isLoading={media.isPending}
			getItemId={(item: MediaItem) => String(item.id)}
			searchLabel={__('Search media', 'gophenberg')}
			config={{ perPageSizes: [] }}
			paginationInfo={paginationOf({ total: page.total, limit: page.perPage })}
			defaultLayouts={{
				grid: { fields: ['dimensions', 'filesize'] },
				table: {
					fields: ['filename', 'mimeType', 'dimensions', 'filesize', 'date'],
				},
			}}
			empty={<MediaEmpty narrowed={Boolean(view.search) || filteredKind(view) !== ''} />}
		/>
	)
}

/**
 * Renders the media library screen.
 * @returns The library screen element.
 */
export function MediaScreen() {
	const settings = useListSettings().data
	const [held, setHeld] = useState<View>(INITIAL_VIEW)
	const view = { ...held, perPage: held.perPage ?? settings?.list_page_size }
	const [selection, setSelection] = useState<string[]>([])
	const region = useRef<HTMLDivElement>(null)
	const { busy, notice, upload } = useUpload(region)
	const kind = filteredKind(view)
	const media = useQuery({
		queryKey: [...mediaQueryKey, kind, view.search, view.page, view.perPage],
		queryFn: () => listMedia({ type: kind, search: view.search, page: view.page, perPage: view.perPage }),
	})
	const title = _x('Media', 'admin section', 'gophenberg')
	const uploadLabel = addLabel()
	return (
		<Page
			title={title}
			headingLevel={2}
			subtitle={__('Manage the images and files this site holds.', 'gophenberg')}
			actions={
				<FileButton multiple icon={uploadIcon} busy={busy} inputLabel={uploadLabel} onChoose={upload}>
					{uploadLabel}
				</FileButton>
			}
		>
			<div ref={region} className="godmin-page__list" role="region" aria-label={title} tabIndex={-1}>
				{notice === undefined ? null : (
					<div className="gophenberg-media__notice">
						<ErrorNotice>{notice}</ErrorNotice>
					</div>
				)}
				<MediaList
					media={media}
					view={view}
					onChangeView={setHeld}
					selection={selection}
					onChangeSelection={setSelection}
				/>
				<DropZone className="godmin-list-overlay" label={__('Drop files to upload', 'gophenberg')} onFilesDrop={upload} />
			</div>
		</Page>
	)
}
