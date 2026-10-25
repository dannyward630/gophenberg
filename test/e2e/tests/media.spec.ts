// SPDX-License-Identifier: Apache-2.0

import { expect, test } from '@playwright/test'
import type { Locator, Page } from '@playwright/test'

import { credentials } from '../env'

const RUN = Math.random().toString(36).slice(2, 8)
const PICTURE = `harbor-e2e-${RUN}`
const POST_TITLE = `A post carrying a picture ${RUN}`
const LIBRARY = `library-e2e-${RUN}`

const PHONE = { width: 390, height: 844 }

const LAPTOP = { width: 1280, height: 900 }

const DESKTOP = { width: 1920, height: 1080 }

// PHOTO_BASE64 is an eight by eight JPEG painted for this suite.
const PHOTO_BASE64 =
	'/9j/2wCEAAYEBQYFBAYGBQYHBwYIChAKCgkJChQODwwQFxQYGBcUFhYaHSUfGhsjHBYWICwgIyYnKSopGR8tMC0oMCUo' +
	'KSgBBwcHCggKEwoKEygaFhooKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKCgoKP/A' +
	'ABEIAAgACAMBIgACEQEDEQH/xAGiAAABBQEBAQEBAQAAAAAAAAAAAQIDBAUGBwgJCgsQAAIBAwMCBAMFBQQEAAABfQEC' +
	'AwAEEQUSITFBBhNRYQcicRQygZGhCCNCscEVUtHwJDNicoIJChYXGBkaJSYnKCkqNDU2Nzg5OkNERUZHSElKU1RVVldY' +
	'WVpjZGVmZ2hpanN0dXZ3eHl6g4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV1tfY' +
	'2drh4uPk5ebn6Onq8fLz9PX29/j5+gEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoLEQACAQIEBAMEBwUEBAABAncA' +
	'AQIDEQQFITEGEkFRB2FxEyIygQgUQpGhscEJIzNS8BVictEKFiQ04SXxFxgZGiYnKCkqNTY3ODk6Q0RFRkdISUpTVFVW' +
	'V1hZWmNkZWZnaGlqc3R1dnd4eXqCg4SFhoeIiYqSk5SVlpeYmZqio6Slpqeoqaqys7S1tre4ubrCw8TFxsfIycrS09TV' +
	'1tfY2dri4+Tl5ufo6ery8/T19vf4+fr/2gAMAwEAAhEDEQA/AMTwt8P/ALn7n9K6r/hX/wD0x/Suo8LfwV1VedjM3xPt' +
	'HqXw5nuL+pR94//Z'

/**
 * Returns the painted photo as an upload payload.
 * @param name - The file name the upload carries.
 * @returns The file Playwright hands a chooser.
 */
function photo(name: string) {
	return { name, mimeType: 'image/jpeg', buffer: Buffer.from(PHOTO_BASE64, 'base64') }
}

/**
 * Returns the canvas the block editor writes into.
 * @param page - The page holding the editor.
 * @returns The canvas frame.
 */
function canvas(page: Page) {
	return page.frameLocator('iframe[name="editor-canvas"]')
}

const createdPosts: string[] = []

test.afterEach(async ({ page }) => {
	for (const id of createdPosts.splice(0)) {
		await page.request.delete(`/api/content/${id}?force=true`)
	}
	const listed = await page.request.get(`/api/media?search=e2e-${RUN}`)
	const body = (await listed.json()) as { items: { id: number }[] }
	for (const item of body.items) {
		await page.request.delete(`/api/media/${item.id}`)
	}
})

/**
 * Stores the painted photo through the API the admin uses, its title taken from its file name.
 * @param page - The page whose session uploads the photo.
 * @param title - The title the photo takes, its file name less the extension.
 * @returns The id of the stored photo.
 */
async function stored(page: Page, title: string): Promise<number> {
	const answer = await page.request.post('/api/media', { multipart: { file: photo(`${title}.jpg`) } })
	expect(answer.status()).toBe(201)
	return ((await answer.json()) as { id: number }).id
}

/**
 * Opens a new post in the editor, keeping its id for the clean up.
 * @param page - The page to drive.
 */
async function newPost(page: Page) {
	await page.goto('/admin/content/post')
	await page.getByRole('main').getByRole('button', { name: 'Add Post', exact: true }).click()
	await expect(page.getByRole('textbox', { name: 'Title' })).toBeVisible()
	const postId = page.url().match(/content\/[a-z-]+\/([0-9a-f-]+)\/edit/)?.[1]
	if (postId !== undefined) {
		createdPosts.push(postId)
	}
}

/**
 * Adds an image block to the open post.
 * @param page - The page holding the editor.
 */
async function addImageBlock(page: Page) {
	await canvas(page).getByRole('document', { name: 'Add default block' }).click()
	await page.keyboard.type('/image')
	await page.keyboard.press('Enter')
}

/**
 * Opens a new post, adds an image block and opens the media library picker from it.
 * @param page - The page to drive.
 * @returns The picker dialog.
 */
async function openPicker(page: Page): Promise<Locator> {
	await newPost(page)
	await addImageBlock(page)
	await canvas(page).getByRole('button', { name: 'Media Library' }).click()
	return page.getByRole('dialog', { name: 'Media Library' })
}

/**
 * Returns the card of the library grid showing a title, matched whole.
 * @param page - The page holding the library.
 * @param title - The title the card shows.
 * @returns The card locator.
 */
function cardOf(page: Page, title: string): Locator {
	return page.locator('.dataviews-view-grid__card').filter({ has: page.getByText(title, { exact: true }) })
}

/**
 * Opens the library narrowed to this run's files and the actions menu of the card showing a title.
 * @param page - The page to drive.
 * @param title - The title the card shows.
 */
async function openCardMenu(page: Page, title: string) {
	await page.goto(`/admin/media?search=${LIBRARY}`)
	await cardOf(page, title).getByRole('button', { name: 'Actions' }).click()
}

/**
 * Holds every listing of the library for 800ms from now on, as a real server over a network answers.
 * @param page - The page whose listings are held.
 */
async function slowListings(page: Page) {
	await page.route('**/api/media?*', async (route) => {
		await new Promise((resolve) => setTimeout(resolve, 800))
		await route.continue()
	})
}

/** How fast the server lists the library again after a save, at once from localhost or held as over a network. */
const LISTINGS = [
	{ speed: 'slow', hold: slowListings },
	{ speed: 'quick', hold: async () => {} },
]

/**
 * Returns the box of the open modal frame once its opening animation ends, rounded to the pixel.
 * @param page - The page holding the modal.
 * @param side - The edge or size of the box to read.
 * @returns The poll that reads it until it holds still.
 */
function frameBox(page: Page, side: 'width' | 'bottom') {
	return expect.poll(async () => {
		const box = await page.locator('.components-modal__frame').boundingBox()
		return Math.round(side === 'width' ? (box?.width ?? 0) : (box?.y ?? 0) + (box?.height ?? 0))
	})
}

/**
 * Returns the id of the account the page is signed in as.
 * @param page - The signed in page.
 * @returns The account id.
 */
async function sessionId(page: Page): Promise<string> {
	const answer = await page.request.get('/api/auth/session')
	return ((await answer.json()) as { id: string }).id
}

/**
 * Waits for the request listing the library that carries every given parameter.
 * @param page - The page that sends it.
 * @param wanted - The parameters and the values the request must carry.
 * @returns The wait.
 */
function listingAsking(page: Page, wanted: Record<string, string>) {
	return page.waitForRequest((request) => {
		const url = new URL(request.url())
		return url.pathname === '/api/media' && Object.entries(wanted).every(([key, value]) => url.searchParams.get(key) === value)
	})
}

test('uploads two pictures chosen at once, counts them in a toast and serves them publicly', async ({ page }) => {
	await page.goto('/admin/media')
	await expect(page.getByRole('heading', { name: 'Media' })).toBeVisible()

	const chooser = page.waitForEvent('filechooser')
	await page.getByRole('main').getByRole('button', { name: 'Add Media File' }).click()
	await (await chooser).setFiles([photo(`${PICTURE}.jpg`), photo(`${PICTURE}-second.jpg`)])

	await expect(page.locator('#root').getByText('Uploaded 2 files.')).toBeVisible()
	const listed = await page.request.get(`/api/media?search=${PICTURE}`)
	const body = (await listed.json()) as { items: { file: string }[] }
	expect(body.items).toHaveLength(2)

	const served = await page.request.get(`/media/${body.items[0].file}`)
	expect(served.status()).toBe(200)
	expect(served.headers()['content-type']).toContain('image/jpeg')
})

test('uploads a picture dropped on the library, over a zone reading Drop files to upload', async ({ page }) => {
	const name = `${PICTURE}-dropped.jpg`
	await page.goto('/admin/media')
	await expect(page.getByRole('heading', { name: 'Media' })).toBeVisible()
	const transfer = await page.evaluateHandle(
		([base64, named]) => {
			const bytes = Uint8Array.from(atob(base64), (char) => char.charCodeAt(0))
			const carried = new DataTransfer()
			carried.items.add(new File([bytes], named, { type: 'image/jpeg' }))
			return carried
		},
		[PHOTO_BASE64, name],
	)

	await page.dispatchEvent('main', 'dragenter', { dataTransfer: transfer })
	const zone = page.locator('.godmin-list-overlay')
	await zone.dispatchEvent('dragenter', { dataTransfer: transfer })
	await expect(zone).toHaveClass(/is-dragging-over-element/)
	await expect(zone.locator('.components-drop-zone__content')).toHaveCSS('opacity', '1')
	await expect(zone.locator('.components-drop-zone__content-inner')).toHaveCSS('opacity', '1')
	await expect(zone.getByText('Drop files to upload')).toBeVisible()
	await zone.dispatchEvent('drop', { dataTransfer: transfer })

	await expect(page.locator('#root').getByText(`"${name}" uploaded.`)).toBeVisible()
})

test('places a picture already in the library through the media library picker', async ({
	page,
}) => {
	await stored(page, `${PICTURE}-stored`)

	const library = await openPicker(page)

	await expect(library.getByText(`${PICTURE}-stored`)).toBeVisible()
	await library.getByText(`${PICTURE}-stored`).click()
	await library.getByRole('button', { name: 'Select' }).click()

	await expect(canvas(page).locator('img[src^="/media/"]')).toBeVisible()
})

test('frames the media library search box when an image block opens the library', async ({ page }) => {
	const library = await openPicker(page)

	await library.getByRole('searchbox', { name: 'Search media' }).focus()
	const frame = library.locator('.components-input-control__backdrop')

	await expect(frame).toHaveCSS('position', 'absolute')
	expect((await frame.boundingBox())?.height).toBeGreaterThan(0)
})

test('places an uploaded picture in a post and publishes it', async ({ page }) => {
	await newPost(page)
	const postId = createdPosts.at(-1)
	await page.getByRole('textbox', { name: 'Title' }).fill(POST_TITLE)

	await addImageBlock(page)
	const chooserOpens = page.waitForEvent('filechooser')
	await canvas(page).getByRole('button', { name: 'Upload' }).click()
	const chooser = await chooserOpens
	await chooser.setFiles(photo(`${PICTURE}-placed.jpg`))
	await expect(canvas(page).locator('img[src^="/media/"]')).toBeVisible()

	await page.getByRole('button', { name: 'Save draft' }).click()
	await expect(page.locator('#root').getByText('Draft saved.')).toBeVisible()
	await page.reload()
	const placed = canvas(page).locator('img[src^="/media/"]')
	await expect(placed).toBeVisible()

	await placed.click()
	await expect(placed).toBeVisible()

	await page.getByRole('button', { name: 'Publish', exact: true }).click()
	await expect(page.locator('#root').getByText('Post published.')).toBeVisible()

	const addressed = await page.request.get(`/api/content/${postId}`)
	const post = (await addressed.json()) as { path: string }
	await page.goto(`/${post.path}`)
	const published = page.locator(`img[src^="/media/"]`)
	await expect(published).toBeVisible()
	const src = await published.getAttribute('src')
	const served = await page.request.get(src as string)
	expect(served.status()).toBe(200)
})

test('lists the library as a grid of cards, each title bold above the name of who uploaded it', async ({ page }) => {
	const title = `${LIBRARY}-grid`
	await stored(page, title)
	await page.setViewportSize(LAPTOP)

	await page.goto(`/admin/media?search=${LIBRARY}`)

	const card = cardOf(page, title)
	await expect(card).toBeVisible()
	await expect(card.locator('.dataviews-title-field')).toHaveCSS('font-weight', '600')
	await expect(card.getByText(credentials.name, { exact: true })).toBeVisible()
	await expect(page.getByRole('button', { name: /layout/i })).toHaveCount(0)
})

test('keeps a sort and a Type filter the reader picks across a reload', async ({ page }) => {
	const title = `${LIBRARY}-kept`
	await stored(page, title)
	await page.goto(`/admin/media?search=${LIBRARY}`)
	await page.getByRole('button', { name: 'View options' }).click()
	await page.getByRole('combobox', { name: 'Sort by' }).selectOption('title')
	await page.keyboard.press('Escape')
	await page.getByRole('button', { name: 'Add filter' }).click()
	await page.getByRole('menuitem', { name: 'Type', exact: true }).click()
	await page.getByRole('option', { name: 'Images' }).click()
	await expect(page).toHaveURL(/sort=title/)
	await expect(page).toHaveURL(/media_type/)

	await page.reload()

	await expect(cardOf(page, title)).toBeVisible()
	await page.getByRole('button', { name: 'Filter', exact: true }).click()
	await expect(page.locator('.dataviews-filters__summary-chip')).toContainText('Images')
	await page.getByRole('button', { name: 'View options' }).click()
	await expect(page.getByRole('combobox', { name: 'Sort by' })).toHaveValue('title')
})

test('opens Edit media from the title of a card in a 560 pixel dialog that saves and says so', async ({ page }) => {
	const title = `${LIBRARY}-edited`
	const id = await stored(page, title)
	await page.setViewportSize(LAPTOP)
	await page.goto(`/admin/media?search=${LIBRARY}`)

	await cardOf(page, title).getByText(title, { exact: true }).click()
	const dialog = page.getByRole('dialog', { name: 'Edit media' })
	await expect(dialog.getByRole('textbox', { name: 'Title' })).toBeFocused()
	await frameBox(page, 'width').toBe(560)
	await dialog.getByRole('textbox', { name: 'Alt text' }).fill('Boats leaving the harbor')
	await dialog.getByRole('button', { name: 'Save' }).click()

	await expect(dialog).toHaveCount(0)
	await expect(page.locator('.godmin-toast', { hasText: 'Media file updated.' })).toBeVisible()
	const read = await page.request.get(`/api/media/${id}`)
	expect(((await read.json()) as { alt_text: string }).alt_text).toBe('Boats leaving the harbor')
})

test('renames a file from its card menu in a 560 pixel dialog, the Name box focused', async ({ page }) => {
	const title = `${LIBRARY}-named`
	await stored(page, title)
	await openCardMenu(page, title)

	await page.getByRole('menuitem', { name: 'Rename…' }).click()
	const dialog = page.getByRole('dialog', { name: 'Rename' })
	await expect(dialog.getByRole('textbox', { name: 'Name' })).toBeFocused()
	await frameBox(page, 'width').toBe(560)
	await dialog.getByRole('textbox', { name: 'Name' }).fill(`${LIBRARY}-renamed`)
	await dialog.getByRole('button', { name: 'Save' }).click()

	await expect(dialog).toHaveCount(0)
	await expect(page.locator('.godmin-toast', { hasText: 'Name updated.' })).toBeVisible()
	await expect(cardOf(page, `${LIBRARY}-renamed`)).toBeVisible()
})

test('gives the focus back to the card menu once a dialog it opened is closed', async ({ page }) => {
	const title = `${LIBRARY}-returned`
	await stored(page, title)
	await openCardMenu(page, title)

	await page.getByRole('menuitem', { name: 'Edit…' }).click()
	const dialog = page.getByRole('dialog', { name: 'Edit media' })
	await expect(dialog.getByRole('textbox', { name: 'Title' })).toBeFocused()
	await dialog.getByRole('button', { name: 'Cancel' }).click()

	await expect(dialog).toHaveCount(0)
	await expect(cardOf(page, title).getByRole('button', { name: 'Actions' })).toBeFocused()
})

test('gives the focus back to the card menu once Edit saves and a slow server lists the library again', async ({
	page,
}) => {
	const title = `${LIBRARY}-saved`
	await stored(page, title)
	await openCardMenu(page, title)

	await page.getByRole('menuitem', { name: 'Edit…' }).click()
	const dialog = page.getByRole('dialog', { name: 'Edit media' })
	await dialog.getByRole('textbox', { name: 'Alt text' }).fill('Boats leaving the harbor')
	await slowListings(page)
	await dialog.getByRole('button', { name: 'Save' }).click()

	await expect(page.locator('.godmin-toast', { hasText: 'Media file updated.' })).toBeVisible()
	await expect(cardOf(page, title).getByRole('button', { name: 'Actions' })).toBeFocused()
})

for (const { speed, hold } of LISTINGS) {
	test(`gives the focus back to the card title Edit media opened from once it saves and a ${speed} server lists again`, async ({
		page,
	}) => {
		const title = `${LIBRARY}-title-${speed}`
		await stored(page, title)
		await page.setViewportSize(LAPTOP)
		await page.goto(`/admin/media?search=${LIBRARY}`)
		const door = cardOf(page, title).locator('.dataviews-title-field')

		await door.click()
		const dialog = page.getByRole('dialog', { name: 'Edit media' })
		await dialog.getByRole('textbox', { name: 'Alt text' }).fill('Boats leaving the harbor')
		await hold(page)
		await dialog.getByRole('button', { name: 'Save' }).click()

		await expect(page.locator('.godmin-toast', { hasText: 'Media file updated.' })).toBeVisible()
		await expect(door).toBeFocused()
	})
}

test('scrolls the fields of Edit media at 1280 by 900 from the keyboard, through a pane named Scrollable section', async ({
	page,
}) => {
	const title = `${LIBRARY}-scrolled`
	await stored(page, title)
	await page.setViewportSize(LAPTOP)
	await openCardMenu(page, title)
	await page.getByRole('menuitem', { name: 'Edit…' }).click()
	const dialog = page.getByRole('dialog', { name: 'Edit media' })
	const pane = dialog.locator('.gophenberg-media__edit-fields')
	const dimensions = pane.getByText('Dimensions', { exact: true })
	const header = dialog.locator('.components-modal__header')
	await dialog.getByRole('textbox', { name: 'Description' }).focus()
	await expect(dimensions).not.toBeInViewport()
	await expect(header).toHaveCSS('border-bottom-color', 'rgba(0, 0, 0, 0)')

	for (const stop of ['Cancel', 'Save', 'Close']) {
		await page.keyboard.press('Tab')
		await expect(dialog.getByRole('button', { name: stop })).toBeFocused()
	}
	await page.keyboard.press('Tab')
	await expect(dialog.getByRole('document', { name: 'Scrollable section' })).toBeFocused()
	await expect(pane).toHaveCSS('box-shadow', /inset$/)
	await page.keyboard.press('End')

	await expect(dimensions).toBeInViewport()
	await expect(header).not.toHaveCSS('border-bottom-color', 'rgba(0, 0, 0, 0)')
	await expect.poll(() => gapUnder(dimensions)).toBeGreaterThanOrEqual(24)
})

/**
 * Measures the space between the bottom of a field and the bottom edge of the Edit pane it scrolls in.
 * @param field - A field inside the pane.
 * @returns The gap in CSS pixels.
 */
function gapUnder(field: Locator): Promise<number> {
	return field.evaluate((element) => {
		const pane = element.closest('.gophenberg-media__edit-fields') as Element
		return Math.round(pane.getBoundingClientRect().bottom - element.getBoundingClientRect().bottom)
	})
}

for (const size of [LAPTOP, DESKTOP]) {
	test(`keeps Cancel and Save of Edit media in view at ${size.width} by ${size.height}, the fields scrolling above them`, async ({
		page,
	}) => {
		const title = `${LIBRARY}-tall-${size.width}`
		await stored(page, title)
		await page.setViewportSize(size)
		await openCardMenu(page, title)

		await page.getByRole('menuitem', { name: 'Edit…' }).click()
		const dialog = page.getByRole('dialog', { name: 'Edit media' })
		await expect(dialog.getByRole('textbox', { name: 'Title' })).toBeFocused()

		await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeInViewport({ ratio: 1 })
		await expect(dialog.getByRole('button', { name: 'Save' })).toBeInViewport({ ratio: 1 })
	})
}

test('deletes a file behind a headerless 560 pixel confirm, Cancel focused', async ({ page }) => {
	const title = `${LIBRARY}-deleted`
	await stored(page, title)
	await openCardMenu(page, title)

	await page.getByRole('menuitem', { name: 'Permanently delete…' }).click()
	const dialog = page.getByRole('dialog')
	await expect(dialog.getByText(`Delete "${title}" for good? This cannot be undone.`)).toBeVisible()
	await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
	await expect(dialog.getByRole('heading')).toHaveCount(0)
	await frameBox(page, 'width').toBe(560)
	await dialog.getByRole('button', { name: 'Delete permanently' }).click()

	await expect(page.locator('.godmin-toast', { hasText: `"${title}" permanently deleted.` })).toBeVisible()
	await expect(cardOf(page, title)).toHaveCount(0)
})

test('keeps a Rename opened on the last card of a row, with what was typed, once an upload lands a file before it', async ({
	page,
}) => {
	for (const at of ['a', 'b', 'c', 'd', 'e', 'f']) {
		await stored(page, `${LIBRARY}-row-${at}`)
	}
	await page.setViewportSize(LAPTOP)
	await page.goto(`/admin/media?search=${LIBRARY}`)
	const last = page.locator('.dataviews-view-grid__row').first().locator('.dataviews-view-grid__card').last()
	const title = (await last.locator('.dataviews-title-field').textContent()) as string
	let release = () => {}
	const held = new Promise<void>((resolve) => {
		release = resolve
	})
	await page.route('**/api/media', async (route) => {
		if (route.request().method() === 'POST') {
			await held
		}
		await route.continue()
	})
	const chooser = page.waitForEvent('filechooser')
	await page.getByRole('main').getByRole('button', { name: 'Add Media File' }).click()
	await (await chooser).setFiles(photo(`${LIBRARY}-row-landed.jpg`))
	await last.getByRole('button', { name: 'Actions' }).click()
	await page.getByRole('menuitem', { name: 'Rename…' }).click()
	const name = page.getByRole('dialog', { name: 'Rename' }).getByRole('textbox', { name: 'Name' })
	await expect(name).toBeFocused()
	await name.press('End')
	await name.pressSequentially(' kept')

	release()
	await expect(page.locator('.godmin-toast', { hasText: `"${LIBRARY}-row-landed.jpg" uploaded.` })).toBeVisible()

	await expect(name).toHaveValue(`${title} kept`)
	await expect(name).toBeFocused()
})

test('narrows and sorts the media library picker with the pointer, then places the picked picture', async ({
	page,
}) => {
	const title = `${LIBRARY}-pointed`
	await stored(page, title)
	const library = await openPicker(page)
	await expect(library.getByText(title, { exact: true })).toBeVisible()

	await library.getByRole('button', { name: 'Add filter' }).click()
	await page.getByRole('menuitem', { name: 'Author' }).click()
	const narrowed = listingAsking(page, { author: await sessionId(page) })
	await library.getByRole('option', { name: credentials.name }).click()
	await narrowed
	await page.keyboard.press('Escape')
	await library.getByRole('button', { name: 'View options' }).click()
	const sorted = listingAsking(page, { orderby: 'modified' })
	await library.getByRole('combobox', { name: 'Sort by' }).selectOption('modified')
	await sorted
	await page.keyboard.press('Escape')
	await library.getByText(title, { exact: true }).click()
	await library.getByRole('button', { name: 'Select' }).click()

	await expect(library).toHaveCount(0)
	await expect(canvas(page).locator('img[src^="/media/"]')).toBeVisible()
})

test('searches, narrows, sorts and places from the media library picker by keyboard, each popover inside it', async ({
	page,
}) => {
	const title = `${LIBRARY}-typed`
	await stored(page, title)
	const library = await openPicker(page)
	await library.getByRole('searchbox', { name: 'Search media' }).focus()
	await page.keyboard.type(title)
	await expect(library.getByText(title, { exact: true })).toBeVisible()

	await library.getByRole('button', { name: 'Add filter' }).focus()
	await page.keyboard.press('Enter')
	await expect(page.getByRole('menuitem', { name: 'Author' })).toBeFocused()
	await page.keyboard.press('Enter')
	await expect(library.getByRole('combobox', { name: 'Conditions' })).toBeFocused()
	const authors = library.getByRole('listbox', { name: 'List of: Author' })
	await expect(async () => {
		await page.keyboard.press('Tab')
		await expect(authors).toBeFocused({ timeout: 500 })
	}).toPass()
	const mine = authors.getByRole('option', { name: credentials.name })
	for (let step = 0; step < 10 && (await mine.getAttribute('data-active-item')) === null; step++) {
		await page.keyboard.press('ArrowDown')
	}
	const narrowed = listingAsking(page, { author: await sessionId(page) })
	await page.keyboard.press('Enter')
	await narrowed
	await expect(authors).toBeFocused()
	await page.keyboard.press('Escape')
	await library.getByRole('button', { name: 'View options' }).focus()
	await page.keyboard.press('Enter')
	await expect(library.getByRole('combobox', { name: 'Sort by' })).toBeFocused()
	const sorted = page.waitForResponse((answer) => new URL(answer.url()).searchParams.has('orderby'))
	await page.keyboard.press('ArrowDown')
	await sorted
	await page.keyboard.press('Escape')
	await expect(library.getByRole('button', { name: 'View options' })).toBeFocused()
	await expect(library.getByText(title, { exact: true })).toBeVisible()
	await library.getByRole('button', { name: 'Reset', exact: true }).focus()
	await page.keyboard.press('Tab')
	await page.keyboard.press('Space')
	await page.keyboard.press('Tab')
	await expect(library.getByRole('button', { name: 'Select' })).toBeFocused()
	await page.keyboard.press('Enter')

	await expect(library).toHaveCount(0)
	await expect(canvas(page).locator('img[src^="/media/"]')).toBeVisible()
})

test.describe('on a phone', () => {
	test.use({ viewport: PHONE, isMobile: true, hasTouch: true })

	test('lists the library one file per row and opens Edit media as a sheet from a tap on a row', async ({ page }) => {
		const title = `${LIBRARY}-phone`
		await stored(page, title)

		await page.goto(`/admin/media?search=${LIBRARY}`)

		await expect(page.locator('.dataviews-view-list')).toBeVisible()
		await expect(page.locator('.dataviews-view-grid')).toHaveCount(0)
		await page.getByRole('button', { name: new RegExp(`^${title}`) }).tap()
		await expect(page.getByRole('dialog', { name: 'Edit media' })).toBeVisible()
		await frameBox(page, 'width').toBe(PHONE.width)
		await frameBox(page, 'bottom').toBe(PHONE.height)
	})

	test('keeps Cancel and Save of Edit media in view in the sheet, the fields scrolling above them', async ({ page }) => {
		const title = `${LIBRARY}-sheet`
		await stored(page, title)
		await page.goto(`/admin/media?search=${LIBRARY}`)

		await page.getByRole('button', { name: new RegExp(`^${title}`) }).tap()
		const dialog = page.getByRole('dialog', { name: 'Edit media' })
		await frameBox(page, 'bottom').toBe(PHONE.height)

		await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeInViewport({ ratio: 1 })
		await expect(dialog.getByRole('button', { name: 'Save' })).toBeInViewport({ ratio: 1 })
	})

	for (const { speed, hold } of LISTINGS) {
		test(`gives the focus back to the tapped row once Edit media saves and a ${speed} server lists again`, async ({
			page,
		}) => {
			const title = `${LIBRARY}-tap-${speed}`
			await stored(page, title)
			await page.goto(`/admin/media?search=${LIBRARY}`)
			const door = page.getByRole('button', { name: new RegExp(`^${title}`) })

			await door.tap()
			const dialog = page.getByRole('dialog', { name: 'Edit media' })
			await dialog.getByRole('textbox', { name: 'Alt text' }).fill('Boats leaving the harbor')
			await hold(page)
			await dialog.getByRole('button', { name: 'Save' }).tap()

			await expect(page.locator('.godmin-toast', { hasText: 'Media file updated.' })).toBeVisible()
			await expect(door).toBeFocused()
		})
	}

	test('gives the focus back to the tapped row once Edit media fails to save and a slow server lists again', async ({
		page,
	}) => {
		const title = `${LIBRARY}-tap-failed`
		await stored(page, title)
		await page.goto(`/admin/media?search=${LIBRARY}`)
		const door = page.getByRole('button', { name: new RegExp(`^${title}`) })

		await door.tap()
		const dialog = page.getByRole('dialog', { name: 'Edit media' })
		await dialog.getByRole('textbox', { name: 'Alt text' }).fill('Boats leaving the harbor')
		await page.route('**/api/media/*', (route) => (route.request().method() === 'PATCH' ? route.abort() : route.continue()))
		await slowListings(page)
		await dialog.getByRole('button', { name: 'Save' }).tap()

		await expect(page.locator('.godmin-toast', { hasText: 'Could not save image.' })).toBeVisible()
		await expect(door).toBeFocused()
	})
})
