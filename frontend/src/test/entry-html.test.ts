// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from 'vitest'

const entry = readFileSync(join(import.meta.dirname, '..', '..', 'index.html'), 'utf8')

test('draws nothing in the root before any script runs', () => {
	expect(entry).toMatch(/<div id="root"><\/div>/)
})

test('carries no style of its own', () => {
	expect(entry).not.toMatch(/<style/)
})
