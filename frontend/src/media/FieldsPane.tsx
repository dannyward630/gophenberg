// SPDX-License-Identifier: Apache-2.0

import { __ } from '@wordpress/i18n'
import { useLayoutEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'

/**
 * Returns the class of the fields pane, marked as the components Modal marks its own content.
 * @param scrollable - Whether the fields overflow the pane.
 * @param scrolled - Whether the pane sits scrolled down from its top.
 * @returns The class names, is-scrollable while the fields overflow and has-scrolled-content once it scrolled.
 */
function paneClass(scrollable: boolean, scrolled: boolean): string {
	const marks = ['gophenberg-media__edit-fields']
	if (scrollable) {
		marks.push('is-scrollable')
	}
	if (scrolled) {
		marks.push('has-scrolled-content')
	}
	return marks.join(' ')
}

/**
 * Renders the pane the fields of Edit scroll in, as the components Modal treats its own content: named Scrollable
 * section while the fields overflow it, checked each time they or the pane resize, and marked once it scrolled. It
 * turns a tab stop only once the focus the Modal puts on its first field landed inside, so the Modal never picks the
 * pane for that.
 * @param props - The fields.
 * @returns The pane.
 */
export function FieldsPane({ children }: { children: ReactNode }) {
	const pane = useRef<HTMLDivElement>(null)
	const content = useRef<HTMLDivElement>(null)
	const [scrollable, setScrollable] = useState(false)
	const [scrolled, setScrolled] = useState(false)
	const [entered, setEntered] = useState(false)
	useLayoutEffect(() => {
		const scroller = pane.current as HTMLDivElement
		const measure = () => setScrollable(scroller.scrollHeight > scroller.clientHeight)
		const watcher = new ResizeObserver(measure)
		watcher.observe(content.current as HTMLDivElement)
		watcher.observe(scroller)
		measure()
		return () => watcher.disconnect()
	}, [])
	return (
		<div
			ref={pane}
			className={paneClass(scrollable, scrolled)}
			role="document"
			aria-label={scrollable ? __('Scrollable section') : undefined}
			tabIndex={scrollable && entered ? 0 : undefined}
			onFocus={() => setEntered(true)}
			onScroll={(event) => setScrolled(event.currentTarget.scrollTop > 0)}
		>
			<div ref={content}>{children}</div>
		</div>
	)
}
