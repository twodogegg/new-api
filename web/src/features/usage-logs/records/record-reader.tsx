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
import { useQuery } from '@tanstack/react-query'
import { ChevronDown, Wrench } from 'lucide-react'
import { useEffect, useState } from 'react'
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
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getRecordMedia } from './api'
import {
  recordObject,
  requestRecordMessages,
  responseRecordMessages,
} from './messages'
import { RecordMediaGrid } from './record-media'
import type {
  RecordMessage,
  RecordMessageMedia,
  RequestRecordDetail,
} from './types'

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

function PanelCount(props: { value: number; label: string }) {
  const { i18n } = useTranslation()
  return (
    <span className='text-muted-foreground shrink-0 text-xs font-medium'>
      {formatNumber(
        props.value,
        toIntlLocale(i18n.resolvedLanguage || i18n.language)
      )}{' '}
      {props.label}
    </span>
  )
}

function ReaderPanel(props: {
  title: string
  count: number
  countLabel?: string
  defaultOpen?: boolean
  children: React.ReactNode
}) {
  const { t } = useTranslation()
  return (
    <Collapsible
      defaultOpen={props.defaultOpen}
      className='bg-muted/20 overflow-hidden rounded-xl border'
    >
      <CollapsibleTrigger
        render={
          <Button
            variant='ghost'
            className='group h-auto w-full justify-between gap-3 rounded-none px-3 py-2.5 text-left font-medium'
          />
        }
      >
        <span className='flex min-w-0 items-center gap-2'>
          <ChevronDown className='text-muted-foreground size-4 shrink-0 -rotate-90 transition-transform group-data-[panel-open]:rotate-0' />
          <span className='truncate'>{props.title}</span>
        </span>
        <PanelCount
          value={props.count}
          label={props.countLabel ?? t('characters')}
        />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className='border-t px-3 py-2'>{props.children}</div>
      </CollapsibleContent>
    </Collapsible>
  )
}

function ReaderCode(props: { text: string; language: string }) {
  return (
    <CodeBlock
      code={props.text}
      language={props.language}
      showLineNumbers={false}
      collapsedLines={18}
      maxExpandedLines={48}
      className='my-1'
    >
      <CodeBlockCopyButton />
    </CodeBlock>
  )
}

function InstructionPanel(props: { message: RecordMessage }) {
  const { t } = useTranslation()
  const message = props.message
  let label = t('System instructions')
  if (message.role === 'developer') label = t('Developer instructions')
  return (
    <ReaderPanel
      title={message.name ? `${label} · ${message.name}` : label}
      count={message.text.length}
      defaultOpen={message.text.length <= 800}
    >
      <ReaderCode text={message.text} language='text' />
    </ReaderPanel>
  )
}

function ReminderPanel(props: { message: RecordMessage }) {
  const { t } = useTranslation()
  return (
    <ReaderPanel
      title={t('System reminder')}
      count={props.message.text.length}
      defaultOpen={false}
    >
      <ReaderCode text={props.message.text} language='text' />
    </ReaderPanel>
  )
}

function ToolCallPanel(props: { message: RecordMessage }) {
  const { t } = useTranslation()
  const label =
    props.message.role === 'tool' ? t('Tool result') : t('Tool call')
  return (
    <ReaderPanel
      title={props.message.name ? `${label} · ${props.message.name}` : label}
      count={props.message.text.length}
      defaultOpen={false}
    >
      <ReaderCode text={props.message.text} language='json' />
    </ReaderPanel>
  )
}

function ToolDefinitions(props: { tools: unknown[] }) {
  const { t } = useTranslation()
  return (
    <ReaderPanel
      title={t('Tool definitions')}
      count={props.tools.length}
      countLabel={t('Tools')}
      defaultOpen={false}
    >
      <div className='space-y-1'>
        {props.tools.map((tool, index) => {
          const object = recordObject(tool)
          const definition = recordObject(object.function ?? object)
          const name = String(definition.name ?? object.name ?? `#${index + 1}`)
          const description =
            typeof definition.description === 'string'
              ? definition.description
              : ''
          return (
            <Collapsible
              key={`${name}:${JSON.stringify(tool)}`}
              className='rounded-lg border'
            >
              <CollapsibleTrigger
                render={
                  <Button
                    variant='ghost'
                    className='group h-auto w-full justify-start gap-2 rounded-lg px-3 py-2 text-left font-normal'
                  />
                }
              >
                <Wrench className='text-muted-foreground size-3.5 shrink-0' />
                <span className='flex min-w-0 flex-1 flex-col'>
                  <span className='truncate font-mono text-xs'>{name}</span>
                  {description && (
                    <span className='text-muted-foreground truncate text-xs font-normal'>
                      {description}
                    </span>
                  )}
                </span>
                <ChevronDown className='text-muted-foreground size-4 shrink-0 -rotate-90 transition-transform group-data-[panel-open]:rotate-0' />
              </CollapsibleTrigger>
              <CollapsibleContent>
                <div className='border-t px-2 py-2'>
                  <RecordJSON value={tool} title={name} />
                </div>
              </CollapsibleContent>
            </Collapsible>
          )
        })}
      </div>
    </ReaderPanel>
  )
}

function InlineMedia(props: { recordId: number; media: RecordMessageMedia }) {
  const { t } = useTranslation()
  const [url, setUrl] = useState('')
  const query = useQuery({
    queryKey: ['request-record-media', props.recordId, props.media.blobId],
    queryFn: () => getRecordMedia(props.recordId, props.media.blobId),
    retry: false,
    gcTime: 0,
  })
  useEffect(() => {
    if (!query.data) return
    const objectUrl = URL.createObjectURL(query.data)
    setUrl(objectUrl)
    return () => URL.revokeObjectURL(objectUrl)
  }, [query.data])
  if (query.isPending) {
    return <div className='bg-muted h-32 w-48 animate-pulse rounded-lg' />
  }
  if (query.isError || !url) {
    return (
      <p className='text-muted-foreground text-xs'>
        {t('Media preview unavailable')}
      </p>
    )
  }
  if (props.media.mimeType.startsWith('image/')) {
    return (
      <img
        alt={t('Image')}
        className='max-h-64 max-w-full rounded-lg border object-contain'
        src={url}
      />
    )
  }
  if (props.media.mimeType.startsWith('video/')) {
    return (
      <video
        className='max-h-64 max-w-full rounded-lg border'
        controls
        src={url}
      />
    )
  }
  if (props.media.mimeType.startsWith('audio/')) {
    return <audio className='w-full' controls src={url} />
  }
  return (
    <a className='text-primary text-sm underline' download href={url}>
      {t('Download')}
    </a>
  )
}

function RecordMessageItem(props: {
  message: RecordMessage
  recordId?: number
}) {
  const { t } = useTranslation()
  const message = props.message
  if (message.kind === 'image') {
    if (!props.recordId || !message.media?.length) return null
    const recordId = props.recordId
    return (
      <Message from={message.role === 'user' ? 'user' : 'assistant'}>
        <MessageContent variant='flat' className='min-w-0 gap-2 py-1.5'>
          <div className='flex flex-wrap gap-2'>
            {message.media.map((media) => (
              <InlineMedia
                key={media.blobId}
                media={media}
                recordId={recordId}
              />
            ))}
          </div>
        </MessageContent>
      </Message>
    )
  }
  if (message.kind === 'reasoning') {
    return (
      <Reasoning>
        <ReasoningTrigger label={t('Returned reasoning')} />
        <ReasoningContent>{message.text}</ReasoningContent>
      </Reasoning>
    )
  }
  if (message.kind === 'reminder') {
    return <ReminderPanel message={message} />
  }
  if (message.kind === 'tool') {
    return <ToolCallPanel message={message} />
  }
  if (message.role === 'system' || message.role === 'developer') {
    return <InstructionPanel message={message} />
  }
  const role = message.role === 'user' ? 'user' : 'assistant'
  let label = t('Message')
  if (message.role === 'user') label = t('User')
  if (message.role === 'assistant') label = t('Assistant')
  return (
    <Message from={role}>
      <MessageContent variant='flat' className='min-w-0 gap-1.5 py-1.5'>
        <span className='text-muted-foreground text-xs font-medium'>
          {label}
        </span>
        <Response final>{message.text}</Response>
      </MessageContent>
    </Message>
  )
}

function ToolReplyGroup(props: {
  messages: RecordMessage[]
  recordId?: number
}) {
  const { t } = useTranslation()
  return (
    <Collapsible
      defaultOpen={false}
      className='bg-muted/20 overflow-hidden rounded-xl border'
    >
      <CollapsibleTrigger
        render={
          <Button
            variant='ghost'
            className='group h-auto w-full justify-between gap-3 rounded-none px-3 py-2.5 text-left font-medium'
          />
        }
      >
        <span className='flex min-w-0 items-center gap-2'>
          <ChevronDown className='text-muted-foreground size-4 shrink-0 -rotate-90 transition-transform group-data-[panel-open]:rotate-0' />
          <span className='truncate'>{t('Tools')}</span>
        </span>
        <span className='text-muted-foreground shrink-0 text-xs font-medium'>
          {props.messages.length}
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className='space-y-2 border-t px-3 py-2'>
          {props.messages.map((message) => (
            <RecordMessageItem
              key={message.id}
              message={message}
              recordId={props.recordId}
            />
          ))}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

function ReaderSection(props: {
  label: string
  children: React.ReactNode
  className?: string
}) {
  return (
    <section
      className={cn('space-y-3', props.className)}
      aria-label={props.label}
    >
      <h3 className='text-muted-foreground text-xs font-medium tracking-wide uppercase'>
        {props.label}
      </h3>
      {props.children}
    </section>
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
  const toolReplies = identifiedReplies.filter(
    (message) => message.kind === 'tool'
  )
  const visibleReplies = identifiedReplies.filter(
    (message) => message.kind !== 'tool'
  )
  const tools = recordObject(input?.body).tools
  const toolList = Array.isArray(tools) ? tools : []
  const reminders = messages.filter((message) => message.kind === 'reminder')
  const instructions = messages.filter(
    (message) =>
      (message.role === 'system' || message.role === 'developer') &&
      message.kind !== 'reminder'
  )
  const conversation = messages.filter(
    (message) =>
      message.role !== 'system' &&
      message.role !== 'developer' &&
      message.kind !== 'reminder'
  )
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
        {props.detail.media.length > 0 && (
          <RecordMediaGrid detail={props.detail} />
        )}
        {snapshot?.body === undefined && props.detail.media.length === 0 && (
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
    <div className='mx-auto w-full max-w-3xl space-y-6'>
      <ReaderSection label={t('Input context')}>
        {instructions.map((message) => (
          <InstructionPanel key={message.id} message={message} />
        ))}
        {reminders.map((message) => (
          <ReminderPanel key={message.id} message={message} />
        ))}
        {toolList.length > 0 && <ToolDefinitions tools={toolList} />}
        {conversation.length > 0
          ? conversation.map((message) => (
              <RecordMessageItem
                key={message.id}
                message={message}
                recordId={props.detail.record?.id}
              />
            ))
          : instructions.length === 0 &&
            reminders.length === 0 &&
            toolList.length === 0 && (
              <p className='text-muted-foreground text-sm'>
                {t('No readable conversation')}
              </p>
            )}
      </ReaderSection>
      <ReaderSection label={t('Model response')} className='border-t pt-5'>
        {identifiedReplies.length ? (
          <>
            {visibleReplies.map((message) => (
              <RecordMessageItem
                key={message.id}
                message={message}
                recordId={props.detail.record?.id}
              />
            ))}
            {toolReplies.length > 0 && (
              <ToolReplyGroup
                messages={toolReplies}
                recordId={props.detail.record?.id}
              />
            )}
          </>
        ) : (
          <p className='text-muted-foreground text-sm'>
            {t('No readable response was captured.')}
          </p>
        )}
      </ReaderSection>
    </div>
  )
}
