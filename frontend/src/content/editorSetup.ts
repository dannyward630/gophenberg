// SPDX-License-Identifier: Apache-2.0

import { CANVAS_LAYOUT } from '@gophenberg/frontend-sdk/editor'
import { __ } from '@wordpress/i18n'

import {
	ALLOWED_MIME_TYPES,
	IMAGE_SIZES,
	MAX_UPLOAD_BYTES,
	editorMediaUpload,
} from '../media/editorMedia'

export const EDITOR_SETTINGS = {
	__experimentalBlockPatterns: [],
	__experimentalFeatures: { layout: CANVAS_LAYOUT, useRootPaddingAwareAlignments: true },
	__unstableIsBlockBasedTheme: true,
	mediaUpload: editorMediaUpload,
	allowedMimeTypes: ALLOWED_MIME_TYPES,
	imageSizes: IMAGE_SIZES,
	imageDefaultSize: 'large',
	imageEditing: false,
	maxUploadFileSize: MAX_UPLOAD_BYTES,
}

/** The classes and layout of the canvas root, as the WordPress post editor gives its root container. */
export const CANVAS_ROOT = {
	className: 'is-layout-constrained has-global-padding',
	layout: { ...CANVAS_LAYOUT, type: 'constrained' },
}

/**
 * Returns the block editor settings for a window, the block toolbar fixed above the canvas when it is narrow.
 * @param narrow - Whether the window is below the editor breakpoint.
 * @returns The settings the block editor provider takes.
 */
export function editorSettings(narrow: boolean) {
	return { ...EDITOR_SETTINGS, hasFixedToolbar: narrow }
}

const PREVIEW_ITEMS = {
	desktop: {
		get label() {
			return __('Desktop', 'gophenberg')
		},
		value: 'desktop',
	},
	tablet: {
		get label() {
			return __('Tablet', 'gophenberg')
		},
		value: 'tablet',
	},
	mobile: {
		get label() {
			return __('Mobile', 'gophenberg')
		},
		value: 'mobile',
	},
}

export type PreviewWidth = keyof typeof PREVIEW_ITEMS

export const PREVIEW_WIDTHS = Object.values(PREVIEW_ITEMS)

/**
 * Returns the item the preview select holds for a width.
 * @param width - The width the canvas renders at.
 * @returns The item standing for that width.
 */
export function previewItem(width: PreviewWidth) {
	return PREVIEW_ITEMS[width]
}

/**
 * Returns the classes the editor root carries for a window.
 * @param narrow - Whether the window is below the editor breakpoint.
 * @returns The class attribute of the editor root.
 */
export function editorClass(narrow: boolean): string {
	return narrow ? 'gophenberg-editor gophenberg-editor--narrow' : 'gophenberg-editor'
}

/**
 * Returns the classes the settings sidebar carries, the sliding one only once it has moved since load.
 * @param moved - Whether the sidebar was toggled, closed or carried over the breakpoint since the editor loaded.
 * @returns The class attribute of the sidebar.
 */
export function sidebarClass(moved: boolean): string {
	const base = 'gophenberg-editor__sidebar'
	return moved ? `${base} ${base}--moved` : base
}

/**
 * Returns the classes the canvas carries at a preview width.
 * @param width - The width the canvas renders at.
 * @returns The class attribute of the canvas.
 */
export function canvasClass(width: PreviewWidth): string {
	const base = 'gophenberg-editor__canvas'
	if (width === 'desktop') {
		return base
	}
	return `${base} ${base}--${width}`
}
