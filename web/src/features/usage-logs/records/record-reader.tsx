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
import { useTranslation } from 'react-i18next'

import {
  CodeBlock,
  CodeBlockCopyButton,
} from '@/components/ai-elements/code-block'
import { Message, MessageContent } from '@/components/ai-elements/message'
import {
  Reasoning,
  ReasoningContent,
  ReasoningTrigger,
} from '@/components/ai-elements/reasoning'
import { Response } from '@/components/ai-elements/response'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'

import {
  recordObject,
  requestRecordMessages,
  responseRecordMessages,
} from './messages'
import { RecordMediaGrid } from './record-media'
import type { RecordMessage, RequestRecordDetail } from './types'

export function RecordJSON(props: { value: unknown; title: string }) {
  return (
    <CodeBlock
      code={JSON.stringify(props.value, null, 2) ?? ''}
      language='json'
      title={props.title}
      showToolbar
      showLineNumbers
      collapsedLines={20}
      maxExpandedLines={80}
    >
      <CodeBlockCopyButton />
    </CodeBlock>
  )
}

function RecordMessageItem(props: { message: RecordMessage }) {
  const { t } = useTranslation()
  const message = props.message
  if (message.kind === 'reasoning') {
    return (
      <Reasoning>
        <ReasoningTrigger>{t('Returned reasoning')}</ReasoningTrigger>
        <ReasoningContent>{message.text}</ReasoningContent>
      </Reasoning>
    )
  }
  if (
    message.kind === 'tool' ||
    message.role === 'system' ||
    message.role === 'developer'
  ) {
    let label = t('System instructions')
    if (message.role === 'developer') label = t('Developer instructions')
    if (message.kind === 'tool') {
      label = message.role === 'tool' ? t('Tool result') : t('Tool call')
    }
    return (
      <Collapsible>
        <CollapsibleTrigger
          render={
            <Button
              variant='ghost'
              className='max-w-full justify-start overflow-hidden text-left'
            />
          }
        >
          <span className='truncate'>
            {label}
            {message.name ? ` · ${message.name}` : ''}
          </span>
        </CollapsibleTrigger>
        <CollapsibleContent>
          <CodeBlock
            code={message.text}
            language={message.kind === 'tool' ? 'json' : 'text'}
            title={label}
            showToolbar
            collapsedLines={16}
            maxExpandedLines={80}
          >
            <CodeBlockCopyButton />
          </CodeBlock>
        </CollapsibleContent>
      </Collapsible>
    )
  }
  const role = message.role === 'user' ? 'user' : 'assistant'
  let label = t('Message')
  if (message.role === 'user') label = t('User')
  if (message.role === 'assistant') label = t('Assistant')
  return (
    <Message from={role}>
      <MessageContent variant='flat' className='min-w-0 space-y-2 py-3'>
        <span className='text-muted-foreground text-xs'>{label}</span>
        <Response final>{message.text}</Response>
      </MessageContent>
    </Message>
  )
}

export function RecordReader(props: { detail: RequestRecordDetail }) {
  const { t } = useTranslation()
  const input = props.detail.snapshots.find(
    (snapshot) => snapshot.stage === 'client_request'
  )
  const output = props.detail.snapshots.find(
    (snapshot) => snapshot.stage === 'client_response'
  )
  const messages = requestRecordMessages(input?.body)
  const replies = responseRecordMessages(output)
  const replyKeys = new Map<string, number>()
  const identifiedReplies = replies.map((message) => {
    const key = JSON.stringify(message)
    const occurrence = replyKeys.get(key) ?? 0
    replyKeys.set(key, occurrence + 1)
    return { ...message, id: `${key}:${occurrence}` }
  })
  const tools = recordObject(input?.body).tools
  const isConversation =
    props.detail.record?.kind === 'llm' &&
    (messages.length > 0 || replies.length > 0)
  if (!isConversation) {
    const snapshot =
      input ??
      props.detail.snapshots.find(
        (snapshot) => snapshot.stage === 'task_result'
      )
    return (
      <div className='space-y-5'>
        {snapshot?.body !== undefined && (
          <RecordJSON
            value={snapshot.body}
            title={t('Request parameters and results')}
          />
        )}
        {props.detail.media.length > 0 ? (
          <RecordMediaGrid detail={props.detail} />
        ) : (
          <EmptyState
            title={t('No readable conversation')}
            description={t(
              'Use request inspection to view the captured protocol data.'
            )}
          />
        )}
      </div>
    )
  }
  return (
    <div className='space-y-5'>
      <section className='space-y-3' aria-label={t('Input context')}>
        <h3 className='text-muted-foreground text-xs font-medium'>
          {t('Input context')}
        </h3>
        {messages.map((message) => (
          <RecordMessageItem key={message.id} message={message} />
        ))}
        {Array.isArray(tools) && tools.length > 0 && (
          <Collapsible>
            <CollapsibleTrigger render={<Button variant='ghost' />}>
              {t('Tool definitions')}
            </CollapsibleTrigger>
            <CollapsibleContent>
              <RecordJSON value={tools} title={t('Tool definitions')} />
            </CollapsibleContent>
          </Collapsible>
        )}
      </section>
      <section
        className='space-y-3 border-t pt-4'
        aria-label={t('Model response')}
      >
        <h3 className='text-muted-foreground text-xs font-medium'>
          {t('Model response')}
        </h3>
        {replies.length ? (
          identifiedReplies.map((message) => (
            <RecordMessageItem key={message.id} message={message} />
          ))
        ) : (
          <p className='text-muted-foreground text-sm'>
            {t('No readable response was captured.')}
          </p>
        )}
      </section>
    </div>
  )
}
