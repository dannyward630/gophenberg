// SPDX-License-Identifier: Apache-2.0

import * as godmin from '@gopherium/godmin/dialogs'
import { expect, test } from 'vitest'

import { ConfirmDialog, FormDialog } from '../dialogs'
import type { ConfirmDialogProps, FormDialogProps } from '../dialogs'
import dialogs from '../dialogs.ts?raw'
import manifest from '../package.json'

/** The exports the SDK package names in its manifest. */
const exported: Record<string, string> = manifest.exports

test('hands plugins the dialogs the admin asks with outside a list, the very ones godmin ships', () => {
	const handed = [ConfirmDialog, FormDialog]

	expect(handed.map((piece) => typeof piece)).toEqual(['function', 'function'])
	expect(handed).toEqual([godmin.ConfirmDialog, godmin.FormDialog])
})

test('hands plugins the types the dialogs are written in', () => {
	const confirm: Pick<ConfirmDialogProps, 'size' | 'confirmLabel'> = { size: 'small', confirmLabel: 'Move to trash' }
	const form: Pick<FormDialogProps, 'size' | 'title'> = { size: 'medium', title: 'Add Event' }

	expect([confirm.size, confirm.confirmLabel, form.size, form.title]).toEqual([
		'small',
		'Move to trash',
		'medium',
		'Add Event',
	])
})

test('serves the dialogs from their own entry, apart from the main one', () => {
	expect(exported['./dialogs']).toBe('./dialogs.ts')
})

test('loads the stylesheet of the components the dialogs draw with', () => {
	expect(dialogs).toContain('@wordpress/components/build-style/style.css')
})
