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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { RequestRecordButton } from '../request-record-details'
import type { RequestRecordDetail } from '../types'

const detail: RequestRecordDetail = {
  record: {
    id: 1,
    user_id: 7,
    request_id: 'request-1',
    model_name: 'scene-model',
    kind: 'llm',
    operation: 'request',
    request_path: '/v1/chat/completions',
    conversation_id: '',
    run_id: '',
    task_id: '',
    parent_request_id: '',
    status: 'success',
    body_state: 'available',
    created_at: 1794650000,
    expires_at: 1795254800,
    duration_ms: 2400,
  },
  snapshots: [
    {
      stage: 'client_request',
      content_type: 'application/json',
      state: 'available',
      attempt: 0,
      body: { messages: [{ role: 'user', content: 'Draw a rainy street' }] },
    },
    {
      stage: 'upstream_request',
      content_type: 'application/json',
      state: 'available',
      attempt: 0,
      body: { model: 'mapped-scene-model' },
    },
    {
      stage: 'upstream_response',
      content_type: 'application/json',
      state: 'available',
      attempt: 0,
      body: { text: 'upstream-only-value' },
    },
    {
      stage: 'client_response',
      content_type: 'application/json',
      state: 'available',
      attempt: 0,
      body: {
        choices: [
          {
            message: {
              role: 'assistant',
              content: 'Keep the warm street light',
            },
          },
        ],
      },
    },
  ],
  media: [],
}

function renderButton(
  props: { requestId?: string; taskId?: string } = { requestId: 'request-1' }
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RequestRecordButton {...props} userId={7} />
    </QueryClientProvider>
  )
  return client
}

test('loads bodies only when opened and exposes readable roles and all four snapshots', async () => {
  const user = userEvent.setup()
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: detail } })
  const client = renderButton()
  expect(get).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Request details' }))
  expect(await screen.findByText('Draw a rainy street')).toBeVisible()
  expect(screen.getByText('Keep the warm street light')).toBeVisible()
  expect(screen.getByText('User', { exact: true })).toBeVisible()
  expect(screen.getByText('Assistant', { exact: true })).toBeVisible()
  await user.click(screen.getByRole('tab', { name: 'Inspect request' }))
  expect(
    screen.getByRole('button', { name: 'Original client request' })
  ).toBeVisible()
  await user.click(
    screen.getByRole('button', { name: 'Actual upstream request · 1' })
  )
  expect(await screen.findByText(/mapped-scene-model/)).toBeVisible()
  await user.click(
    screen.getByRole('button', { name: 'Upstream response · 1' })
  )
  expect(await screen.findByText(/upstream-only-value/)).toBeVisible()
  expect(screen.getByRole('button', { name: 'Client response' })).toBeVisible()
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  await waitFor(() =>
    expect(
      client.getQueryCache().findAll({ queryKey: ['request-record'] })
    ).toHaveLength(0)
  )
})

test('folds an injected system reminder out of the visible user message', async () => {
  const user = userEvent.setup()
  const reminderDetail: RequestRecordDetail = {
    ...detail,
    snapshots: detail.snapshots.map((snapshot) =>
      snapshot.stage === 'client_request'
        ? {
            ...snapshot,
            body: {
              messages: [
                {
                  role: 'user',
                  content:
                    '<system-reminder>hidden workspace instructions</system-reminder>\n\nHello there',
                },
              ],
            },
          }
        : snapshot
    ),
  }
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: reminderDetail },
  })
  renderButton()
  await user.click(screen.getByRole('button', { name: 'Request details' }))
  expect(await screen.findByText('Hello there')).toBeVisible()
  const panel = screen.getByRole('button', { name: /System reminder/ })
  expect(panel).toBeVisible()
  const folded = screen.queryByText(/hidden workspace instructions/)
  if (folded) expect(folded).not.toBeVisible()
  await user.click(panel)
  expect(await screen.findByText(/hidden workspace instructions/)).toBeVisible()
})

test('reads a merged stream response saved with a stream content type', async () => {
  const user = userEvent.setup()
  const streamDetail: RequestRecordDetail = {
    ...detail,
    snapshots: detail.snapshots.map((snapshot) =>
      snapshot.stage === 'client_response'
        ? {
            ...snapshot,
            content_type: 'text/event-stream',
            body: {
              choices: [
                {
                  index: 0,
                  message: { role: 'assistant', content: 'Hello world' },
                },
              ],
            },
          }
        : snapshot
    ),
  }
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: streamDetail },
  })
  renderButton()
  await user.click(screen.getByRole('button', { name: 'Request details' }))
  expect(await screen.findByText('Hello world')).toBeVisible()
  await user.click(screen.getByRole('tab', { name: 'Inspect request' }))
  await user.click(screen.getByRole('button', { name: 'Client response' }))
  expect(screen.getByRole('textbox', { name: 'Client response' })).toBeVisible()
  expect(
    screen.queryByRole('button', { name: /Raw stream frames/ })
  ).not.toBeInTheDocument()
})

test('folds legacy raw stream frames into a merged view during inspection', async () => {
  const user = userEvent.setup()
  const legacyDetail: RequestRecordDetail = {
    ...detail,
    snapshots: detail.snapshots.map((snapshot) =>
      snapshot.stage === 'client_response'
        ? {
            ...snapshot,
            content_type: 'text/event-stream',
            body: [
              {
                event: '',
                data: { choices: [{ index: 0, delta: { content: 'Hello' } }] },
              },
              {
                event: '',
                data: {
                  choices: [{ index: 0, delta: { content: ' world' } }],
                },
              },
            ],
          }
        : snapshot
    ),
  }
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: legacyDetail },
  })
  renderButton()
  await user.click(screen.getByRole('button', { name: 'Request details' }))
  expect(await screen.findByText('Hello world')).toBeVisible()
  await user.click(screen.getByRole('tab', { name: 'Inspect request' }))
  await user.click(screen.getByRole('button', { name: 'Client response' }))
  expect(
    screen.getByRole('button', { name: /Raw stream frames/ })
  ).toBeVisible()
  expect(
    screen.queryByRole('textbox', { name: 'Raw stream frames' })
  ).not.toBeInTheDocument()
})

test('renders returned reasoning with an inline label', async () => {
  const user = userEvent.setup()
  const reasoningDetail: RequestRecordDetail = {
    ...detail,
    snapshots: detail.snapshots.map((snapshot) =>
      snapshot.stage === 'client_response'
        ? {
            ...snapshot,
            body: {
              choices: [
                {
                  message: {
                    role: 'assistant',
                    content: 'Final answer',
                    reasoning_content: 'Hidden chain of thought',
                  },
                },
              ],
            },
          }
        : snapshot
    ),
  }
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: reasoningDetail },
  })
  renderButton()
  await user.click(screen.getByRole('button', { name: 'Request details' }))
  expect(await screen.findByText('Final answer')).toBeVisible()
  expect(screen.getByText('Returned reasoning')).toBeVisible()
  expect(screen.getByText(/Duration/)).toBeVisible()
})

test('shows a task-style detail without conversation context for non-LLM records', async () => {
  const user = userEvent.setup()
  const videoDetail: RequestRecordDetail = {
    ...detail,
    record: detail.record ? { ...detail.record, kind: 'video' } : null,
    snapshots: [
      {
        stage: 'client_request',
        content_type: 'application/json',
        state: 'available',
        attempt: 0,
        body: { model: 'minimax-h3-flash', prompt: 'a cat', seconds: 4 },
      },
    ],
    media: [],
  }
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: videoDetail },
  })
  renderButton()
  await user.click(screen.getByRole('button', { name: 'Request details' }))
  expect(
    await screen.findByRole('tab', { name: 'Request parameters and results' })
  ).toBeVisible()
  expect(
    screen.queryByRole('tab', { name: 'Request context' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('tab', { name: 'Read conversation' })
  ).not.toBeInTheDocument()
})

test('folds many tool calls in the model response into a collapsed group', async () => {
  const user = userEvent.setup()
  const toolDetail: RequestRecordDetail = {
    ...detail,
    snapshots: detail.snapshots.map((snapshot) =>
      snapshot.stage === 'client_response'
        ? {
            ...snapshot,
            body: {
              choices: [
                {
                  message: {
                    role: 'assistant',
                    content: 'Done',
                    tool_calls: [
                      {
                        id: 'c1',
                        function: { name: 'bash', arguments: '{"cmd":"ls"}' },
                      },
                      {
                        id: 'c2',
                        function: { name: 'bash', arguments: '{"cmd":"pwd"}' },
                      },
                    ],
                  },
                },
              ],
            },
          }
        : snapshot
    ),
  }
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: toolDetail },
  })
  renderButton()
  await user.click(screen.getByRole('button', { name: 'Request details' }))
  expect(await screen.findByText('Done')).toBeVisible()
  const group = screen.getByRole('button', { name: /Tools/ })
  expect(group).toBeVisible()
  expect(
    screen.queryByRole('button', { name: /Tool call/ })
  ).not.toBeInTheDocument()
  await user.click(group)
  expect(
    await screen.findAllByRole('button', { name: /Tool call/ })
  ).toHaveLength(2)
})

test('folds tool calls in the input conversation history', async () => {
  const user = userEvent.setup()
  const convDetail: RequestRecordDetail = {
    ...detail,
    snapshots: detail.snapshots.map((snapshot) =>
      snapshot.stage === 'client_request'
        ? {
            ...snapshot,
            body: {
              messages: [
                { role: 'user', content: 'hello' },
                {
                  role: 'assistant',
                  reasoning_content: 'thinking',
                  tool_calls: [
                    {
                      id: 't1',
                      function: { name: 'bash', arguments: '{"cmd":"ls"}' },
                    },
                  ],
                },
                { role: 'tool', tool_call_id: 't1', content: 'file.txt' },
                { role: 'assistant', content: 'final answer' },
              ],
            },
          }
        : snapshot
    ),
  }
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: convDetail },
  })
  renderButton()
  await user.click(screen.getByRole('button', { name: 'Request details' }))
  expect(await screen.findByText('hello')).toBeVisible()
  const group = screen.getByRole('button', { name: /Reasoning and tools/ })
  expect(group).toBeVisible()
  expect(
    screen.queryByRole('button', { name: /Tool (call|result)/ })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('Returned reasoning')).not.toBeInTheDocument()
  await user.click(group)
  expect(
    await screen.findAllByRole('button', { name: /Tool (call|result)/ })
  ).toHaveLength(2)
  expect(await screen.findByText('Returned reasoning')).toBeVisible()
})

test.each([
  [{ record: null, snapshots: [], media: [] }, 'Body not captured'],
  [
    {
      ...detail,
      record: { ...detail.record, body_state: 'expired' },
      snapshots: [],
      media: [],
    },
    'The request summary is retained, but its body and media have expired.',
  ],
])(
  'explains missing or expired bodies instead of presenting an empty response',
  async (response, text) => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: response },
    })
    renderButton()
    await userEvent.click(
      screen.getByRole('button', { name: 'Request details' })
    )
    expect(await screen.findByText(text)).toBeVisible()
  }
)

test('shows API errors with retry and renders the record after retry succeeds', async () => {
  const user = userEvent.setup()
  const get = vi
    .spyOn(api, 'get')
    .mockRejectedValueOnce(new Error('Cannot load recording'))
    .mockResolvedValue({ data: { success: true, data: detail } })
  renderButton()
  await user.click(screen.getByRole('button', { name: 'Request details' }))
  expect(await screen.findByText('Cannot load recording')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('Draw a rainy street')).toBeVisible()
  expect(get).toHaveBeenCalledTimes(2)
})

test('loads submission and final result through the task log entry', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (String(url).endsWith('/records')) {
      return {
        data: {
          success: true,
          data: {
            items: [
              { ...detail.record, task_id: 'task-1' },
              {
                ...detail.record,
                id: 2,
                operation: 'task_result',
                task_id: 'task-1',
              },
            ],
            total: 2,
          },
        },
      }
    }
    return { data: { success: true, data: detail } }
  })
  renderButton({ taskId: 'task-1' })
  await userEvent.click(screen.getByRole('button', { name: 'Request details' }))
  expect(
    await screen.findByText('Linked requests and task results')
  ).toBeVisible()
  expect(
    screen.getByRole('button', { name: /Submission request/ })
  ).toBeVisible()
  expect(screen.getByRole('button', { name: /Task result/ })).toBeVisible()
})
