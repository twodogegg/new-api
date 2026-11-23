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
import type { RecordMessage, RecordMessageMedia, RecordSnapshot } from './types'

export function recordObject(value: unknown): Record<string, unknown> {
  if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  return {}
}

// Tool payloads are captured exactly as the client sent them, so a JSON result
// is one compressed line with escaped line breaks. Pretty-print a copy for
// reading; the stored payload stays untouched.
export function formatToolPayload(text: string): string {
  const raw = text.trim()
  if (!raw) return text
  const parsed = parseToolPayload(raw)
  if (parsed === null || typeof parsed !== 'object') return text
  return JSON.stringify(parsed, null, 2).replaceAll(
    /\\r\\n|\\n|\\t|\\\\/g,
    (match) => {
      if (match === '\\\\') return match
      if (match === '\\t') return '\t'
      return '\n'
    }
  )
}

// Models occasionally append stray closing brackets after a complete JSON
// value. Retry without them so the payload still reads as formatted JSON
// instead of one long line; anything else falls back to the raw text.
function parseToolPayload(raw: string): unknown {
  let candidate = raw
  for (let attempt = 0; attempt < 3; attempt += 1) {
    try {
      return JSON.parse(candidate)
    } catch {
      if (!/[}\]]$/.test(candidate)) return undefined
      candidate = candidate.slice(0, -1).trimEnd()
    }
  }
  return undefined
}

function recordText(value: unknown): string {
  if (typeof value === 'string') return value
  if (Array.isArray(value)) {
    return value.map(recordText).filter(Boolean).join('\n')
  }
  const object = recordObject(value)
  if (
    object.thought === true ||
    object.type === 'thinking' ||
    object.type === 'tool_use' ||
    object.type === 'tool_result' ||
    object.functionCall ||
    object.functionResponse
  ) {
    return ''
  }
  if (typeof object.text === 'string') return object.text
  if (object.content === undefined && object.parts === undefined) return ''
  return recordText(object.content ?? object.parts)
}

// A serialized image is stored as a `_record_media` marker instead of inline
// base64. Collect those markers so the reader can render the attachment; the
// surrounding "Image ... (sha256:...)" description is harness metadata that
// would otherwise duplicate the picture as text.
function collectMedia(value: unknown, media: RecordMessageMedia[]) {
  if (Array.isArray(value)) {
    for (const child of value) collectMedia(child, media)
    return
  }
  const object = recordObject(value)
  const id = object._record_media
  if (typeof id === 'string' && id) {
    media.push({
      blobId: id,
      mimeType: typeof object.mime_type === 'string' ? object.mime_type : '',
    })
  }
  for (const key of Object.keys(object)) {
    if (key === '_record_media') continue
    const child = object[key]
    if (child !== null && typeof child === 'object') collectMedia(child, media)
  }
}

const IMAGE_METADATA_PATTERN = /^Image\s+"[^"]*"\s*\(sha256:/i

function recordDisplayText(value: unknown): string {
  if (Array.isArray(value)) {
    return value
      .map((part) => recordText(part))
      .filter((text) => text && !IMAGE_METADATA_PATTERN.test(text.trim()))
      .join('\n')
  }
  return recordText(value)
}

const SYSTEM_REMINDER_PATTERN =
  /<system-reminder>([\s\S]*?)<\/system-reminder>/gi

// Clients such as coding agents prepend workspace instructions to a user turn as
// a <system-reminder> block. Keep that block out of the visible user message and
// return it as its own entry so the reader can fold it away, matching what the
// user actually typed.
function splitSystemReminders(role: string, text: string): RecordMessage[] {
  const messages: RecordMessage[] = []
  let cursor = 0
  for (const match of text.matchAll(SYSTEM_REMINDER_PATTERN)) {
    const index = match.index ?? 0
    const before = text.slice(cursor, index).trim()
    if (before) messages.push({ role, text: before, kind: 'text' })
    const reminder = (match[1] ?? '').trim()
    if (reminder) messages.push({ role, text: reminder, kind: 'reminder' })
    cursor = index + match[0].length
  }
  const rest = text.slice(cursor).trim()
  if (rest) messages.push({ role, text: rest, kind: 'text' })
  return messages
}

function messagesFromItem(
  item: unknown,
  fallbackRole: string
): RecordMessage[] {
  if (typeof item === 'string') {
    return splitSystemReminders(fallbackRole, item)
  }
  const object = recordObject(item)
  const role = typeof object.role === 'string' ? object.role : fallbackRole
  const content = object.content ?? object.parts
  const messages: RecordMessage[] = []
  const media: RecordMessageMedia[] = []
  collectMedia(content, media)
  if (media.length > 0) {
    messages.push({ role, text: '', kind: 'image', media })
  }
  const text = recordDisplayText(content)
  if (text) {
    if (role === 'tool') {
      messages.push({
        role,
        text,
        kind: 'tool',
        name:
          typeof object.tool_call_id === 'string'
            ? object.tool_call_id
            : undefined,
      })
    } else {
      messages.push(...splitSystemReminders(role, text))
    }
  }
  const reasoning = object.reasoning_content ?? object.reasoning
  if (typeof reasoning === 'string' && reasoning) {
    messages.unshift({ role: 'assistant', text: reasoning, kind: 'reasoning' })
  }
  if (Array.isArray(content)) {
    for (const part of content) {
      const block = recordObject(part)
      if (block.type === 'thinking' && typeof block.thinking === 'string') {
        messages.push({
          role: 'assistant',
          text: block.thinking,
          kind: 'reasoning',
        })
      }
      if (block.thought === true && typeof block.text === 'string') {
        messages.push({
          role: 'assistant',
          text: block.text,
          kind: 'reasoning',
        })
      }
      if (block.type === 'tool_use' || block.functionCall) {
        const call = recordObject(block.functionCall ?? block)
        messages.push({
          role: 'assistant',
          kind: 'tool',
          name: String(call.name ?? block.id ?? ''),
          text: JSON.stringify(call.args ?? call.input ?? {}, null, 2),
        })
      }
      if (block.type === 'tool_result' || block.functionResponse) {
        const result = recordObject(block.functionResponse ?? block)
        messages.push({
          role: 'tool',
          kind: 'tool',
          name: String(result.name ?? block.tool_use_id ?? ''),
          text:
            recordText(result.content) ||
            JSON.stringify(
              result.response ?? result.content ?? result,
              null,
              2
            ),
        })
      }
    }
  }
  if (Array.isArray(object.tool_calls)) {
    for (const item of object.tool_calls) {
      const call = recordObject(item)
      const fn = recordObject(call.function)
      messages.push({
        role: 'assistant',
        kind: 'tool',
        name: String(fn.name ?? call.id ?? ''),
        text:
          typeof fn.arguments === 'string'
            ? fn.arguments
            : JSON.stringify(fn.arguments ?? {}, null, 2),
      })
    }
  }
  if (object.type === 'function_call') {
    messages.push({
      role: 'assistant',
      kind: 'tool',
      name: String(object.name ?? ''),
      text: String(object.arguments ?? ''),
    })
  }
  if (object.type === 'function_call_output') {
    messages.push({
      role: 'tool',
      kind: 'tool',
      name: String(object.call_id ?? ''),
      text: recordText(object.output),
    })
  }
  return messages
}

export function requestRecordMessages(body: unknown): RecordMessage[] {
  const object = recordObject(body)
  const messages: RecordMessage[] = []
  const instructions =
    object.system ??
    object.instructions ??
    object.systemInstruction ??
    object.system_instruction
  const instructionText = recordText(instructions)
  if (instructionText) {
    messages.push({ role: 'system', text: instructionText, kind: 'text' })
  }
  const input =
    object.messages ?? object.contents ?? object.input ?? object.prompt
  if (Array.isArray(input)) {
    for (const item of input) messages.push(...messagesFromItem(item, 'user'))
  } else if (typeof input === 'string') {
    messages.push(...splitSystemReminders('user', input))
  }
  return messages.map((message, index) => ({
    ...message,
    id: `input:${index}`,
    role: message.role === 'model' ? 'assistant' : message.role,
  }))
}

function responseMessages(body: unknown): RecordMessage[] {
  const object = recordObject(body)
  if (Array.isArray(object.choices)) {
    return object.choices.flatMap((item) => {
      const choice = recordObject(item)
      return messagesFromItem(
        choice.message ?? { content: choice.text },
        'assistant'
      )
    })
  }
  if (Array.isArray(object.output)) {
    return object.output.flatMap((item) => messagesFromItem(item, 'assistant'))
  }
  if (Array.isArray(object.candidates)) {
    return object.candidates.flatMap((item) =>
      messagesFromItem(recordObject(item).content, 'assistant')
    )
  }
  if (Array.isArray(object.content)) {
    return messagesFromItem(object, 'assistant')
  }
  if (typeof object.output_text === 'string') {
    return [{ role: 'assistant', text: object.output_text, kind: 'text' }]
  }
  return []
}

export function responseRecordMessages(
  snapshot?: RecordSnapshot
): RecordMessage[] {
  if (!snapshot) return []
  if (
    snapshot.content_type !== 'text/event-stream' ||
    !Array.isArray(snapshot.body)
  ) {
    return responseMessages(snapshot.body)
  }
  const outputs = new Map<string, { text: string; reasoning: string }>()
  const calls = new Map<string, { name: string; arguments: string }>()
  for (const item of snapshot.body) {
    const event = recordObject(item)
    const data = recordObject(event.data)
    if (
      data.type === 'response.completed' ||
      data.type === 'response.incomplete'
    ) {
      const final = responseMessages(data.response)
      if (final.length) return final
    }
    const choiceList = Array.isArray(data.choices) ? data.choices : []
    for (const item of choiceList) {
      const choice = recordObject(item)
      const key = String(choice.index ?? 0)
      const output = outputs.get(key) ?? { text: '', reasoning: '' }
      const delta = recordObject(choice.delta)
      output.text += recordText(delta.content)
      output.reasoning += recordText(delta.reasoning_content)
      outputs.set(key, output)
      if (Array.isArray(delta.tool_calls)) {
        for (const item of delta.tool_calls) {
          const call = recordObject(item),
            fn = recordObject(call.function)
          const id = `${key}:${String(call.index ?? call.id ?? 0)}`
          const current = calls.get(id) ?? { name: '', arguments: '' }
          current.name += typeof fn.name === 'string' ? fn.name : ''
          current.arguments +=
            typeof fn.arguments === 'string' ? fn.arguments : ''
          calls.set(id, current)
        }
      }
    }
    const output = outputs.get('other') ?? { text: '', reasoning: '' }
    if (data.type === 'response.output_text.delta') {
      output.text += recordText(data.delta)
    }
    if (data.type === 'response.reasoning_summary_text.delta') {
      output.reasoning += recordText(data.delta)
    }
    const delta = recordObject(data.delta)
    if (delta.type === 'text_delta') output.text += recordText(delta.text)
    if (delta.type === 'thinking_delta') {
      output.reasoning += recordText(delta.thinking)
    }
    if (data.type === 'content_block_start') {
      const block = recordObject(data.content_block)
      output.text += recordText(block.text)
      if (block.type === 'tool_use') {
        calls.set(`claude:${String(data.index)}`, {
          name: String(block.name ?? ''),
          arguments: '',
        })
      }
    }
    if (delta.type === 'input_json_delta') {
      const call = calls.get(`claude:${String(data.index)}`)
      if (call) call.arguments += recordText(delta.partial_json)
    }
    if (data.type === 'response.output_item.done') {
      const call = recordObject(data.item)
      if (call.type === 'function_call') {
        calls.set(String(call.id ?? call.call_id), {
          name: String(call.name ?? ''),
          arguments: String(call.arguments ?? ''),
        })
      }
    }
    if (Array.isArray(data.candidates)) {
      for (const item of data.candidates) {
        const candidate = recordObject(item),
          content = recordObject(candidate.content)
        for (const part of Array.isArray(content.parts) ? content.parts : []) {
          const block = recordObject(part)
          if (block.thought === true) {
            output.reasoning += String(block.text ?? '')
          } else output.text += recordText(part)
          if (block.functionCall) {
            const call = recordObject(block.functionCall)
            calls.set(
              `gemini:${String(candidate.index ?? 0)}:${String(call.name ?? '')}`,
              {
                name: String(call.name ?? ''),
                arguments: JSON.stringify(call.args ?? {}),
              }
            )
          }
        }
      }
    }
    outputs.set('other', output)
  }
  const messages: RecordMessage[] = []
  for (const output of outputs.values()) {
    if (output.reasoning) {
      messages.push({
        role: 'assistant',
        text: output.reasoning,
        kind: 'reasoning',
      })
    }
    if (output.text) {
      messages.push({ role: 'assistant', text: output.text, kind: 'text' })
    }
  }
  for (const call of calls.values()) {
    messages.push({
      role: 'assistant',
      kind: 'tool',
      name: call.name,
      text: call.arguments,
    })
  }
  return messages
}
