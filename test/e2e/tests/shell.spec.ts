// SPDX-License-Identifier: Apache-2.0

import { expect, test } from '@playwright/test'

const PHONE = { width: 390, height: 844 }

test('pads the canvas and keeps the sidebar beside it on a desktop', async ({ page }) => {
	await page.goto('/admin/')

	await expect(page.getByRole('region', { name: 'Navigation' })).toBeVisible()
	await expect(page.getByRole('button', { name: 'Open navigation panel' })).toBeHidden()
	const padding = await page
		.locator('.godmin-layout__canvas')
		.evaluate((canvas) => getComputedStyle(canvas).padding)
	expect(padding).toBe('16px 24px')
})

test('reads every sidebar row clearly against the chrome', async ({ page }) => {
	await page.goto('/admin/content/post')
	const sidebar = page.getByRole('region', { name: 'Navigation' })
	await expect(sidebar.getByRole('link', { name: 'Users' })).toBeVisible()

	const rows = await sidebar.getByRole('listitem').getByRole('link').evaluateAll((found) =>
		found.map((row) => {
			const luminance = (colour: string) => {
				const [r = 0, g = 0, b = 0] = (colour.match(/\d+/g) ?? []).map((raw) => {
					const value = Number(raw) / 255
					return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
				})
				return 0.2126 * r + 0.7152 * g + 0.0722 * b
			}
			let behind: Element | null = row
			while (behind !== null && getComputedStyle(behind).backgroundColor === 'rgba(0, 0, 0, 0)') {
				behind = behind.parentElement
			}
			const fill = behind === null ? 'rgb(255, 255, 255)' : getComputedStyle(behind).backgroundColor
			const [light = 0, dark = 0] = [luminance(getComputedStyle(row).color), luminance(fill)].sort((a, b) => b - a)
			return { text: row.textContent, contrast: (light + 0.05) / (dark + 0.05) }
		}),
	)

	expect(rows.length).toBeGreaterThan(1)
	for (const row of rows) {
		expect(row.contrast, `${row.text} is too faint for the chrome`).toBeGreaterThanOrEqual(4.5)
	}
})

test('spans the posts listing across the canvas', async ({ page }) => {
	await page.goto('/admin/content/post')
	await expect(page.getByRole('heading', { level: 2, name: 'Posts' })).toBeVisible()
	await expect(page.locator('.dataviews-view-table')).toBeVisible()

	const fit = await page.evaluate(() => {
		const canvas = document.querySelector('.godmin-layout__canvas') as HTMLElement
		const list = document.querySelector('.godmin-page__list') as HTMLElement
		const table = document.querySelector('.dataviews-view-table') as HTMLElement
		const inside = canvas.getBoundingClientRect().left + canvas.clientLeft
		const edges = list.getBoundingClientRect()
		return {
			styled: getComputedStyle(table).borderCollapse,
			start: edges.left - inside,
			end: inside + canvas.clientWidth - edges.right,
			filled: table.getBoundingClientRect().width / edges.width,
		}
	})

	expect(fit.styled).toBe('collapse')
	expect(fit.start).toBeCloseTo(0, 0)
	expect(fit.end).toBeCloseTo(0, 0)
	expect(fit.filled).toBeGreaterThan(0.99)
})

test('folds the sidebar into a drawer on a phone', async ({ page }) => {
	await page.setViewportSize(PHONE)

	await page.goto('/admin/')

	await expect(page.locator('.godmin-layout__sidebar')).toHaveCount(0)
	await page.getByRole('button', { name: 'Open navigation panel' }).click()
	const drawer = page.getByRole('dialog')
	await expect(drawer.getByRole('link', { name: 'Posts' })).toBeVisible()
})

test('marks the current row apart from the drawer surface on a phone', async ({ page }) => {
	await page.setViewportSize(PHONE)

	await page.goto('/admin/content/post')
	await page.getByRole('button', { name: 'Open navigation panel' }).click()

	const current = page.getByRole('dialog').getByRole('link', { name: 'Posts', exact: true })
	await expect(current).toHaveAttribute('data-status', 'active')
	const fills = await current.evaluate((row) => {
		let surface = row.parentElement
		while (surface !== null && getComputedStyle(surface).backgroundColor === 'rgba(0, 0, 0, 0)') {
			surface = surface.parentElement
		}
		return {
			row: getComputedStyle(row).backgroundColor,
			drawer: surface === null ? 'none' : getComputedStyle(surface).backgroundColor,
		}
	})
	expect(fills.row).not.toBe(fills.drawer)
})

test.describe('on a phone with a touch screen', () => {
	test.use({ viewport: PHONE, isMobile: true, hasTouch: true })

	test('closes the drawer on a tap beside it and on Escape', async ({ page }) => {
		await page.goto('/admin/content/post')
		await expect(page.getByRole('heading', { level: 2, name: 'Posts' })).toBeVisible()
		const address = page.url()
		const menu = page.getByRole('button', { name: 'Open navigation panel' })
		const drawer = page.getByRole('dialog')

		await menu.tap()
		await expect(drawer).toBeVisible()
		await page.touchscreen.tap(PHONE.width - 16, PHONE.height / 2)

		await expect(drawer).toBeHidden()
		await expect(page).toHaveURL(address)

		await menu.tap()
		await expect(drawer).toBeVisible()
		await page.keyboard.press('Escape')

		await expect(drawer).toBeHidden()
	})
})

test('names the site beside the menu button on a phone', async ({ page }) => {
	await page.setViewportSize(PHONE)

	await page.goto('/admin/')

	await expect(page.locator('.godmin-layout__topbar').getByText('Gophenberg', { exact: true })).toBeVisible()
})

test('meets the screen edges on a phone without spilling it', async ({ page }) => {
	await page.setViewportSize(PHONE)

	await page.goto('/admin/')

	const canvas = page.locator('.godmin-layout__canvas')
	await expect(canvas).toBeVisible()
	const fit = await canvas.evaluate((element) => ({
		margin: getComputedStyle(element).margin,
		spills: element.getBoundingClientRect().right > document.documentElement.clientWidth,
	}))
	expect(fit.margin).toBe('0px')
	expect(fit.spills).toBe(false)
})
