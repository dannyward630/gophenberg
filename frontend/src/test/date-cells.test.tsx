// SPDX-License-Identifier: Apache-2.0

import { DataViews } from '@gophenberg/frontend-sdk/dataviews'
import { http, HttpResponse, server } from '@gophenberg/frontend-sdk/testing'
import { LIST_CHROME_DOMAIN, listChromeCatalogFor } from '@gopherium/godmin'
import { startListDates } from '@gopherium/godmin/dates'
import { render, screen } from '@testing-library/react'
import { getSettings, setSettings } from '@wordpress/date'
import { resetLocaleData, setLocaleData } from '@wordpress/i18n'
import type { ReactElement } from 'react'
import { afterEach, expect, onTestFinished, test } from 'vitest'

import { postFields } from '../content/fields'
import { placeholderType } from '../content/useContentType'
import { DEFAULT_LOCALE, catalogFor } from '../i18n/catalog'
import { startLocale } from '../i18n/start'
import { rememberFormatLocale, rememberLocale } from '@gopherium/gottext'
import { mediaFields } from '../media/fields'
import type { MediaItem } from '../media/api'
import type { Post } from '../content/api'

const NOON = '2026-08-16T12:00:00Z'

const WINTER_MORNING = '2026-01-05T08:09:00Z'

/** The date settings in place before any test hands the dates a language. */
const DATE_DEFAULTS = getSettings()

afterEach(() => {
	rememberLocale(DEFAULT_LOCALE)
	setSettings(DATE_DEFAULTS)
})

/**
 * Renders the date cell a field list carries.
 * @param fields - The fields the screen lists with.
 * @param item - The row to render.
 */
function renderDate<T>(fields: { id: string, render?: unknown }[], item: T) {
	const field = fields.find((held) => held.id === 'date')!
	const Cell = field.render as (props: { item: T }) => ReactElement
	render(<Cell item={item} />)
}

const post: Post = {
	id: '1',
	type: 'post',
	parentId: null,
	parentTitle: '',
	path: '',
	slug: 'one',
	title: 'One',
	status: 'published',
	excerpt: '',
	authorId: '',
	authorName: '',
	authorAvatar: '',
	publishedAt: NOON,
	createdAt: NOON,
	updatedAt: NOON,
	date: NOON,
}

const item: MediaItem = {
	id: 1,
	type: 'image',
	file: 'one.png',
	title: 'One',
	altText: '',
	caption: '',
	description: '',
	mimeType: 'image/png',
	width: 1,
	height: 1,
	filesize: 1,
	sizes: {},
	authorId: '1',
	authorName: 'Maria Perez',
	authorAvatar: '',
	createdAt: NOON,
	updatedAt: NOON,
}

test('shows a post date in the abbreviated date and time format WordPress lists dates in, not the site format', () => {
	rememberFormatLocale('es-ES')
	startListDates(DEFAULT_LOCALE)

	renderDate(postFields(placeholderType('post')), post)

	expect(screen.getByText('Published: Aug 16, 2026 12:00 pm')).toBeInTheDocument()
})

test('captions and writes a publication date as WordPress es_ES does for a Spanish reader', async () => {
	setLocaleData(await catalogFor('es-ES'), 'gophenberg')
	setLocaleData(await listChromeCatalogFor('es-ES'), LIST_CHROME_DOMAIN)
	onTestFinished(() => resetLocaleData({}, 'gophenberg'))
	onTestFinished(() => resetLocaleData({}, LIST_CHROME_DOMAIN))
	rememberFormatLocale('es-ES')
	startListDates('es-ES')

	renderDate(postFields(placeholderType('post')), post)

	expect(screen.getByText('Publicado: 16 Ago 2026 12:00')).toBeInTheDocument()
})

test('writes a post date in the words of the list chrome catalogue when its own catalogue lacks them', async () => {
	server.use(
		http.get('/api/locale', () => HttpResponse.json({ locale: 'es-ES', supported: [DEFAULT_LOCALE, 'es-ES'] })),
	)
	const own = { '': { 'plural-forms': 'nplurals=2; plural=(n != 1);' }, 'Published: %s': ['Publicado: %s'] }
	const none = async () => undefined
	onTestFinished(() => resetLocaleData({}, 'gophenberg'))
	onTestFinished(() => resetLocaleData({}, LIST_CHROME_DOMAIN))

	await startLocale({ own: async () => own, editor: none, brick: none, chrome: listChromeCatalogFor })
	renderDate(postFields(placeholderType('post')), { ...post, date: WINTER_MORNING })

	expect(screen.getByText('Publicado: 5 Ene 2026 08:09')).toBeInTheDocument()
})

/**
 * Renders the Date added and Date modified of a media item as a DataViews table draws them.
 * @param held - The item to render.
 */
function renderMediaDates(held: MediaItem) {
	render(
		<DataViews
			data={[held]}
			fields={mediaFields()}
			view={{ type: 'table', fields: ['date', 'modified'], titleField: 'title' }}
			onChangeView={() => {}}
			paginationInfo={{ totalItems: 1, totalPages: 1 }}
			defaultLayouts={{ table: {} }}
			getItemId={(row) => String(row.id)}
		/>,
	)
}

test('writes both media dates in the full date and time format WordPress media fields use', () => {
	startListDates(DEFAULT_LOCALE)

	renderMediaDates({ ...item, updatedAt: WINTER_MORNING })

	expect(screen.getByText('August 16, 2026 12:00 pm')).toBeInTheDocument()
	expect(screen.getByText('January 5, 2026 8:09 am')).toBeInTheDocument()
})

test('writes both media dates in the full format of the language the admin booted in', async () => {
	setLocaleData(await listChromeCatalogFor('es-ES'), LIST_CHROME_DOMAIN)
	onTestFinished(() => resetLocaleData({}, LIST_CHROME_DOMAIN))
	startListDates('es-ES')

	renderMediaDates({ ...item, updatedAt: WINTER_MORNING })

	expect(screen.getByText('16 de agosto de 2026 12:00')).toBeInTheDocument()
	expect(screen.getByText('5 de enero de 2026 08:09')).toBeInTheDocument()
})

test('writes no date for a media item that carries no moment', () => {
	startListDates(DEFAULT_LOCALE)

	renderMediaDates({ ...item, createdAt: '', updatedAt: '' })

	const cells = [...document.querySelectorAll('.dataviews-view-table tbody td')].map((cell) => cell.textContent)
	expect(cells).toEqual(['One', '', ''])
})
