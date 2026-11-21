/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { CodeBlock, CodeBlockCopyButton } from '../code-block'

function stubClipboard(value: unknown) {
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value,
  })
}

function stubExecCommand(result: boolean) {
  const execCommand = vi.fn().mockReturnValue(result)
  Object.defineProperty(document, 'execCommand', {
    configurable: true,
    value: execCommand,
  })
  return execCommand
}

afterEach(() => {
  delete (navigator as { clipboard?: unknown }).clipboard
  delete (document as { execCommand?: unknown }).execCommand
})

test('copies through the async clipboard API when it is available', async () => {
  const user = userEvent.setup()
  const writeText = vi.fn().mockResolvedValue(undefined)
  stubClipboard({ writeText })

  render(
    <CodeBlock code='{"a":1}' language='json'>
      <CodeBlockCopyButton />
    </CodeBlock>
  )
  await user.click(screen.getByRole('button', { name: 'Copy code' }))

  expect(writeText).toHaveBeenCalledWith('{"a":1}')
  expect(
    await screen.findByRole('button', { name: 'Copied!' })
  ).toBeInTheDocument()
})

test('falls back to execCommand when the clipboard API is unavailable', async () => {
  const user = userEvent.setup()
  stubClipboard(undefined)
  const execCommand = stubExecCommand(true)

  render(
    <CodeBlock code='{"a":1}' language='json'>
      <CodeBlockCopyButton />
    </CodeBlock>
  )
  await user.click(screen.getByRole('button', { name: 'Copy code' }))

  expect(execCommand).toHaveBeenCalledWith('copy')
  expect(
    await screen.findByRole('button', { name: 'Copied!' })
  ).toBeInTheDocument()
})
