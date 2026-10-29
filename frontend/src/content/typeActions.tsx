// SPDX-License-Identifier: Apache-2.0

import type { Action, RenderModalProps } from '@gophenberg/frontend-sdk/dataviews'
import { RunConfirm, useBulkRun, useToaster, useUnsavedText } from '@gopherium/godmin'
import type { BulkRun, ListRun, UnsavedText } from '@gopherium/godmin'
import { useQueryClient } from '@tanstack/react-query'
import type { QueryClient } from '@tanstack/react-query'
import { __ } from '@wordpress/i18n'
import { useCallback, useMemo } from 'react'

import { originOf } from './typeFields'
import { AddressModal, DescribeModal, MakeDefaultModal } from './TypeModals'
import type { RunOnList } from './TypeModals'
import type { ContentType, TypeEdit } from './types'
import { removalQuestion, removalWords, switchWords } from './typeWords'
import { deleteInTurn, editInTurn, refreshTypes } from './typeWrites'

/** A list run whose end never moves the focus, left to the caller that knows whether the rows stayed on the page. */
const NO_FOCUS_MOVE: ListRun = { onStart: () => () => {} }

/**
 * Reports whether the site made a type, so its shape is the site's to change.
 * @param type - The type.
 * @returns Whether no plugin declared it.
 */
function madeHere(type: ContentType): boolean {
	return originOf(type) === ''
}

/**
 * Reports whether a type may hand its place over or leave, being the site's and not the one at the root.
 * @param type - The type.
 * @returns Whether the site made it and it is not the default.
 */
function demotable(type: ContentType): boolean {
	return madeHere(type) && !type.isDefault
}

/** What the actions of the types list need from the screen around them. */
interface ActionTools {
	/** The query client the writes run on. */
	client: QueryClient
	/** Runs a call over types that may leave the list. */
	removals: BulkRun<ContentType>
	/** Runs a call over types, leaving the focus to the run on the list around it. */
	switches: BulkRun<ContentType>
	/** Edits one type and says in a toast how it went. */
	edit: (type: ContentType, edit: TypeEdit) => Promise<unknown>
	/** Runs a write over types on the list, moving the focus to the list once a type it reached left the page. */
	runOnList: RunOnList
	/** The descriptions failed saves left unsaved. */
	descriptions: UnsavedText
	/** The route words failed moves left unsaved. */
	routeWords: UnsavedText
}

/**
 * Returns the actions opening a form or a confirm about one type.
 * @param tools - What the actions need from the screen.
 * @returns Describe, Change address and Make default.
 */
function formActions(tools: ActionTools): Action<ContentType>[] {
	return [
		{
			id: 'describe',
			label: __('Describe…', 'gophenberg'),
			isEligible: madeHere,
			modalHeader: __('Describe', 'gophenberg'),
			modalFocusOnMount: 'firstContentElement',
			RenderModal: (props) => (
				<DescribeModal {...props} unsaved={tools.descriptions} runOnList={tools.runOnList} />
			),
		},
		{
			id: 'change-address',
			label: __('Change address…', 'gophenberg'),
			isEligible: madeHere,
			modalHeader: __('Change address', 'gophenberg'),
			modalFocusOnMount: 'firstContentElement',
			RenderModal: (props) => <AddressModal {...props} unsaved={tools.routeWords} runOnList={tools.runOnList} />,
		},
		{
			id: 'make-default',
			label: __('Make default…', 'gophenberg'),
			isEligible: demotable,
			modalHeader: __('Make default', 'gophenberg'),
			modalFocusOnMount: 'firstContentElement',
			RenderModal: (props) => <MakeDefaultModal {...props} runOnList={tools.runOnList} />,
		},
	]
}

/**
 * Returns the one click actions switching what a type does.
 * @param tools - What the actions need from the screen.
 * @returns The nesting switches, then Activate and Deactivate, both taking several types.
 */
function switchActions(tools: ActionTools): Action<ContentType>[] {
	const activeAs = (on: boolean) => (types: ContentType[]) =>
		tools.runOnList(types, () =>
			tools.switches(types, (type) => editInTurn(tools.client, type.key, { active: on }), switchWords(on)),
		)
	const nestingAs =
		(hierarchical: boolean) =>
		([type]: ContentType[]) =>
			tools.runOnList([type], () => tools.edit(type, { hierarchical }))
	return [
		{
			id: 'nest',
			label: __('Let items nest', 'gophenberg'),
			isEligible: (type) => madeHere(type) && !type.hierarchical,
			callback: nestingAs(true),
		},
		{
			id: 'stop-nesting',
			label: __('Stop nesting', 'gophenberg'),
			isEligible: (type) => madeHere(type) && type.hierarchical,
			callback: nestingAs(false),
		},
		{
			id: 'activate',
			label: __('Activate', 'gophenberg'),
			supportsBulk: true,
			isEligible: (type) => !type.active,
			callback: activeAs(true),
		},
		{
			id: 'deactivate',
			label: __('Deactivate', 'gophenberg'),
			supportsBulk: true,
			isEligible: (type) => type.active && !type.isDefault,
			callback: activeAs(false),
		},
	]
}

/**
 * Renders the confirm deleting the picked types for good, closing only itself once the run ends.
 * @param props - The types, the handler closing the modal and what the action needs from the screen.
 * @returns The confirm.
 */
function DeleteTypesModal({ items, closeModal, tools }: RenderModalProps<ContentType> & { tools: ActionTools }) {
	return (
		<RunConfirm
			items={items}
			closeModal={closeModal}
			confirmLabel={__('Delete', 'gophenberg')}
			cancelLabel={__('Cancel', 'gophenberg')}
			run={(types) => tools.removals(types, (type) => deleteInTurn(tools.client, type.key), removalWords())}
		>
			{removalQuestion(items)}
		</RunConfirm>
	)
}

/**
 * Returns the action deleting the picked types for good, behind a confirm with no header.
 * @param tools - What the action needs from the screen.
 * @returns Delete, taking several types.
 */
function deleteAction(tools: ActionTools): Action<ContentType> {
	return {
		id: 'delete',
		label: __('Delete…', 'gophenberg'),
		supportsBulk: true,
		isEligible: demotable,
		hideModalHeader: true,
		modalFocusOnMount: 'firstContentElement',
		RenderModal: (props) => <DeleteTypesModal {...props} tools={tools} />,
	}
}

/**
 * Returns the handler editing one type and saying in a toast how it went, the type named by no word that agrees.
 * @param client - The query client the writes run on.
 * @returns The handler.
 */
function useTypeEdit(client: QueryClient): ActionTools['edit'] {
	const toaster = useToaster()
	return useCallback(
		(type, edit) =>
			editInTurn(client, type.key, edit).then(
				async () => {
					await refreshTypes(client)
					toaster.show(__('Content type updated.', 'gophenberg'))
				},
				(failure: Error) => toaster.show(failure.message),
			),
		[client, toaster],
	)
}

/**
 * Returns the handler running a write over types on the list run, moving the focus to the list once a type it reached
 * left the page on show.
 * @param list - What the list does around a run.
 * @param pageKeys - Reads the keys of the types on the page the list shows.
 * @returns The handler.
 */
function useRunOnList(list: ListRun, pageKeys: () => Promise<string[]>): RunOnList {
	return useCallback(
		async (types, write) => {
			const settle = list.onStart()
			await write()
			const shown = await pageKeys()
			settle(types.some((type) => !shown.includes(type.key)))
		},
		[list, pageKeys],
	)
}

/**
 * Returns the actions offered on each row of the types list: the forms first, the one click switches, Delete last.
 * @param list - What the list does around a run.
 * @param pageKeys - Reads the keys of the types on the page the list shows.
 * @returns The row actions, the same for as long as the screen stays, so a confirm never starts over in a run.
 */
export function useTypeActions(list: ListRun, pageKeys: () => Promise<string[]>): Action<ContentType>[] {
	const client = useQueryClient()
	const refresh = useCallback(() => refreshTypes(client), [client])
	const removals = useBulkRun<ContentType>(list, refresh)
	const switches = useBulkRun<ContentType>(NO_FOCUS_MOVE, refresh)
	const edit = useTypeEdit(client)
	const runOnList = useRunOnList(list, pageKeys)
	const descriptions = useUnsavedText()
	const routeWords = useUnsavedText()
	return useMemo(() => {
		const tools = { client, removals, switches, edit, runOnList, descriptions, routeWords }
		return [...formActions(tools), ...switchActions(tools), deleteAction(tools)]
	}, [client, descriptions, edit, removals, routeWords, runOnList, switches])
}
