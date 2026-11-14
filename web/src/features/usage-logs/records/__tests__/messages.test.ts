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
import { describe, expect, test } from 'vitest'

import { buildSearchParams } from '../../lib/filter'
import { buildApiParams } from '../../lib/utils'
import { requestRecordMessages, responseRecordMessages } from '../messages'

describe('request record reading', () => {
  test('keeps Claude tool results and Gemini thoughts separate from ordinary text', () => {
    expect(
      requestRecordMessages({
        messages: [
          {
            role: 'user',
            content: [
              {
                type: 'tool_result',
                tool_use_id: 'call-1',
                content: 'tool-only',
              },
            ],
          },
        ],
      }).map((message) => [message.role, message.text])
    ).toEqual([['tool', 'tool-only']])
    expect(
      responseRecordMessages({
        stage: 'client_response',
        content_type: 'application/json',
        state: 'available',
        attempt: 0,
        body: {
          candidates: [
            {
              content: {
                role: 'model',
                parts: [
                  { text: 'returned thought', thought: true },
                  { text: 'answer' },
                ],
              },
            },
          ],
        },
      })
    ).toEqual([
      { role: 'model', text: 'answer', kind: 'text', name: undefined },
      { role: 'assistant', text: 'returned thought', kind: 'reasoning' },
    ])
  })
  test('separates instructions, user input, tool calls and tool results in client order', () => {
    const messages = requestRecordMessages({
      system: 'Follow the scene brief',
      messages: [
        { role: 'user', content: 'Find the night scene' },
        {
          role: 'assistant',
          tool_calls: [
            {
              id: 'call-1',
              function: { name: 'lookup_scene', arguments: '{"name":"night"}' },
            },
          ],
        },
        { role: 'tool', tool_call_id: 'call-1', content: 'Warm street light' },
      ],
    })
    expect(messages.map((message) => [message.role, message.kind])).toEqual([
      ['system', 'text'],
      ['user', 'text'],
      ['assistant', 'tool'],
      ['tool', 'tool'],
    ])
    expect(messages[2].name).toBe('lookup_scene')
    expect(messages[3].name).toBe('call-1')
  })

  test.each([
    [
      { contents: [{ role: 'model', parts: [{ text: 'Gemini answer' }] }] },
      'assistant',
      'Gemini answer',
    ],
    [{ input: 'Responses input' }, 'user', 'Responses input'],
    [
      {
        messages: [
          {
            role: 'user',
            content: [
              { type: 'text', text: 'Claude input' },
              { type: 'image', source: { _record_media: 'private-ref' } },
            ],
          },
        ],
      },
      'user',
      'Claude input',
    ],
  ])(
    'reads protocol messages without rendering inline media as text',
    (body, role, text) => {
      expect(requestRecordMessages(body)).toMatchObject([{ role, text }])
    }
  )

  test('joins streamed text and tool arguments without repeating final response text', () => {
    const output = responseRecordMessages({
      stage: 'client_response',
      content_type: 'text/event-stream',
      state: 'available',
      attempt: 0,
      body: [
        {
          data: {
            choices: [
              {
                index: 0,
                delta: {
                  content: 'Hello ',
                  tool_calls: [
                    {
                      index: 0,
                      function: { name: 'lookup', arguments: '{"name":' },
                    },
                  ],
                },
              },
            ],
          },
        },
        {
          data: {
            choices: [
              {
                index: 0,
                delta: {
                  content: 'world',
                  tool_calls: [
                    { index: 0, function: { arguments: '"night"}' } },
                  ],
                },
              },
            ],
          },
        },
      ],
    })
    expect(output).toEqual([
      { role: 'assistant', text: 'Hello world', kind: 'text' },
      {
        role: 'assistant',
        name: 'lookup',
        text: '{"name":"night"}',
        kind: 'tool',
      },
    ])
    const final = responseRecordMessages({
      stage: 'client_response',
      content_type: 'text/event-stream',
      state: 'available',
      attempt: 0,
      body: [
        { data: { type: 'response.output_text.delta', delta: 'Hello' } },
        {
          data: {
            type: 'response.completed',
            response: {
              output: [
                {
                  role: 'assistant',
                  content: [{ type: 'output_text', text: 'Hello' }],
                },
              ],
            },
          },
        },
      ],
    })
    expect(final).toEqual([{ role: 'assistant', text: 'Hello', kind: 'text' }])
  })

  test('accepts missing or unrecognized content without inventing an assistant response', () => {
    expect(
      requestRecordMessages({
        messages: [{ role: 'user', content: { _record_media: 'file' } }],
      })
    ).toEqual([])
    expect(responseRecordMessages()).toEqual([])
    expect(
      responseRecordMessages({
        stage: 'client_response',
        content_type: 'application/json',
        state: 'truncated',
        attempt: 0,
      })
    ).toEqual([])
  })
})

test('content search and record filters survive route serialization and reach the server', () => {
  const search = buildSearchParams(
    {
      contentKeyword: 'rain 100%',
      requestKind: 'video',
      bodyState: 'available',
      conversationId: 'conversation-1',
      runId: 'run-1',
    },
    'common'
  )
  const params = buildApiParams({
    page: 2,
    pageSize: 20,
    searchParams: search,
    isAdmin: false,
  })
  expect(params).toMatchObject({
    p: 2,
    content_keyword: 'rain 100%',
    request_kind: 'video',
    body_state: 'available',
    conversation_id: 'conversation-1',
    run_id: 'run-1',
  })
  expect(
    buildSearchParams(
      { taskId: 'task-1', status: 'FAILURE', contentKeyword: 'rain' },
      'task'
    )
  ).toMatchObject({
    filter: 'task-1',
    taskStatus: 'FAILURE',
    contentKeyword: 'rain',
  })
})
