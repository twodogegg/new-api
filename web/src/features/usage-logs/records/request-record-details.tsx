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
import { FileText } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampToDate } from '@/lib/format'

import { TaskArtifacts } from '../components/task-artifacts'
import {
  getRequestRecord,
  getTaskRequestRecords,
  lookupRequestRecord,
} from './api'
import { RecordContext } from './record-context'
import { RecordMediaGrid } from './record-media'
import { RecordJSON, RecordReader } from './record-reader'
import { RelatedRecordList } from './related-record-list'
import type { RequestRecordDetail } from './types'

const operationLabels: Record<string, string> = {
  task_result: 'Task result',
  poll: 'Polling request',
}

function recordStateLabel(state: string) {
  switch (state) {
    case 'available':
      return 'Body saved'
    case 'truncated':
      return 'Recording truncated'
    case 'expired':
      return 'Recording expired'
    case 'unavailable':
      return 'Recording unavailable on this node'
    default:
      return 'Body not captured'
  }
}

function RecordInspector(props: { detail: RequestRecordDetail }) {
  const { t } = useTranslation()
  const [index, setIndex] = useState(0)
  const selected = props.detail.snapshots[index]
  const labels: Record<string, string> = {
    client_request: 'Original client request',
    upstream_request: 'Actual upstream request',
    upstream_response: 'Upstream response',
    client_response: 'Client response',
    task_result: 'Task result',
  }
  return (
    <div className='grid min-w-0 gap-4 md:grid-cols-[12rem_minmax(0,1fr)]'>
      <div className='flex flex-wrap gap-2 md:flex-col'>
        {props.detail.snapshots.map((snapshot, i) => (
          <Button
            key={`${snapshot.stage}:${snapshot.attempt}`}
            variant={index === i ? 'secondary' : 'ghost'}
            className='justify-start text-left text-xs'
            aria-pressed={index === i}
            onClick={() => setIndex(i)}
          >
            {t(labels[snapshot.stage] ?? 'Snapshot')}
            {snapshot.stage.startsWith('upstream_')
              ? ` · ${snapshot.attempt + 1}`
              : ''}
          </Button>
        ))}
      </div>
      <div className='min-w-0 space-y-3'>
        {selected && (
          <p className='text-muted-foreground text-xs'>
            {selected.content_type} · {t(recordStateLabel(selected.state))}
            {selected.status ? ` · HTTP ${selected.status}` : ''}
          </p>
        )}
        {selected?.body !== undefined ? (
          <RecordJSON
            value={selected.body}
            title={t(labels[selected.stage] ?? 'Snapshot')}
          />
        ) : (
          <EmptyState
            title={t(recordStateLabel(selected?.state ?? 'not_captured'))}
          />
        )}
      </div>
    </div>
  )
}

function RecordDetailContent(props: {
  detail: RequestRecordDetail
  taskId?: string
  admin: boolean
  onSelect: (id: number) => void
}) {
  const { t, i18n } = useTranslation()
  const record = props.detail.record
  if (!record) {
    return (
      <EmptyState
        title={t('Body not captured')}
        description={t(
          'Recording may have been disabled when this request was made.'
        )}
      />
    )
  }
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  let mediaContent = <EmptyState title={t('No saved media')} />
  if (props.detail.media.length > 0) {
    mediaContent = <RecordMediaGrid detail={props.detail} />
  } else if (props.taskId) {
    mediaContent = <TaskArtifacts taskId={props.taskId} enabled />
  }
  return (
    <div className='min-w-0 space-y-5'>
      <div className='text-muted-foreground flex flex-wrap gap-x-5 gap-y-2 text-xs'>
        <span>{record.model_name}</span>
        <span>{t(recordStateLabel(record.body_state))}</span>
        <span>{formatTimestampToDate(record.created_at, 'seconds')}</span>
        <span>{formatNumber(record.duration_ms, locale)} ms</span>
        <span>
          {t('Expires at')}:{' '}
          {formatTimestampToDate(record.expires_at, 'seconds')}
        </span>
      </div>
      {record.body_state === 'truncated' && (
        <p role='status' className='text-muted-foreground text-sm'>
          {t(
            'The recording limit was reached. Available content is shown; missing content is not reconstructed.'
          )}
        </p>
      )}
      {record.body_state === 'expired' ? (
        <EmptyState
          title={t('Recording expired')}
          description={t(
            'The request summary is retained, but its body and media have expired.'
          )}
        />
      ) : (
        <Tabs defaultValue='reading' className='min-w-0 gap-5'>
          <TabsList className='h-auto max-w-full flex-wrap justify-start'>
            <TabsTrigger value='reading'>{t('Read conversation')}</TabsTrigger>
            <TabsTrigger value='context'>{t('Request context')}</TabsTrigger>
            <TabsTrigger value='inspect'>{t('Inspect request')}</TabsTrigger>
            <TabsTrigger value='media'>{t('Media')}</TabsTrigger>
          </TabsList>
          <TabsContent value='reading'>
            <RecordReader detail={props.detail} />
          </TabsContent>
          <TabsContent value='context'>
            <RecordContext detail={props.detail} />
          </TabsContent>
          <TabsContent value='inspect'>
            <RecordInspector detail={props.detail} />
          </TabsContent>
          <TabsContent value='media'>{mediaContent}</TabsContent>
        </Tabs>
      )}
      {record.conversation_id && (
        <RelatedRecordList
          scope='conversation'
          record={record}
          admin={props.admin}
          onSelect={props.onSelect}
        />
      )}
      {record.run_id && (
        <RelatedRecordList
          scope='run'
          record={record}
          admin={props.admin}
          onSelect={props.onSelect}
        />
      )}
    </div>
  )
}

type RequestRecordButtonProps = {
  requestId?: string
  userId?: number
  taskId?: string
  isAdmin?: boolean
}

export function RequestRecordButton(props: RequestRecordButtonProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button
        type='button'
        variant='ghost'
        size='sm'
        disabled={!props.requestId && !props.taskId}
        onClick={() => setOpen(true)}
      >
        <FileText />
        {t('Request details')}
      </Button>
      {open && (
        <RequestRecordDialog {...props} onClose={() => setOpen(false)} />
      )}
    </>
  )
}

function RequestRecordDialog(
  props: RequestRecordButtonProps & { onClose: () => void }
) {
  const { t, i18n } = useTranslation()
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [page, setPage] = useState(1)
  const [includePolls, setIncludePolls] = useState(false)
  const tasks = useQuery({
    queryKey: ['request-record-tasks', props.taskId, page, includePolls],
    queryFn: () =>
      getTaskRequestRecords(props.taskId ?? '', page, includePolls),
    enabled: !!props.taskId,
    gcTime: 0,
  })
  const id =
    selectedId ??
    tasks.data?.items.find((item) => item.operation === 'request')?.id ??
    tasks.data?.items[0]?.id
  const query = useQuery({
    queryKey: ['request-record', props.requestId, props.userId, id],
    queryFn: () =>
      id
        ? getRequestRecord(id)
        : lookupRequestRecord(props.requestId ?? '', props.userId),
    enabled: !!id || !!props.requestId,
    gcTime: 0,
  })
  const busy =
    (props.taskId && tasks.isPending) ||
    ((!!id || !!props.requestId) && query.isPending)
  const error = tasks.error ?? query.error
  let feedback: React.ReactNode = null
  if (busy) feedback = <LoadingState />
  if (error) {
    feedback = (
      <ErrorState
        description={error.message}
        onRetry={() => {
          if (id || props.requestId) void query.refetch()
          if (props.taskId) void tasks.refetch()
        }}
      />
    )
  }
  return (
    <Dialog
      open
      onOpenChange={(value) => {
        if (!value) props.onClose()
      }}
      title={t('Request details')}
      description={props.taskId ?? props.requestId}
      contentClassName='sm:max-w-6xl'
      contentHeight='min(74dvh, 900px)'
    >
      {feedback ?? (
        <div className='min-w-0 space-y-5'>
          {props.taskId && tasks.data && (
            <div className='space-y-2'>
              <h3 className='text-sm font-medium'>
                {t('Linked requests and task results')}
              </h3>
              {tasks.data.poll_count > 0 && (
                <Label className='flex items-center gap-2 text-xs'>
                  <Checkbox
                    checked={includePolls}
                    onCheckedChange={(checked) => {
                      setIncludePolls(checked === true)
                      setPage(1)
                      setSelectedId(null)
                    }}
                  />
                  {t('Show polling requests')} ·{' '}
                  {formatNumber(
                    tasks.data.poll_count,
                    toIntlLocale(i18n.resolvedLanguage || i18n.language)
                  )}
                </Label>
              )}
              <div className='flex flex-wrap gap-2'>
                {tasks.data.items.map((record) => (
                  <Button
                    key={record.id}
                    size='sm'
                    variant={id === record.id ? 'secondary' : 'outline'}
                    aria-pressed={id === record.id}
                    onClick={() => setSelectedId(record.id)}
                  >
                    {t(
                      operationLabels[record.operation] ?? 'Submission request'
                    )}{' '}
                    · {formatTimestampToDate(record.created_at, 'seconds')}
                  </Button>
                ))}
              </div>
              {tasks.data.total > 20 && (
                <div className='flex items-center gap-3'>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={page <= 1}
                    onClick={() => {
                      setPage(page - 1)
                      setSelectedId(null)
                    }}
                  >
                    {t('Previous')}
                  </Button>
                  <span className='text-muted-foreground text-xs'>
                    {formatNumber(
                      page,
                      toIntlLocale(i18n.resolvedLanguage || i18n.language)
                    )}
                  </span>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={page * 20 >= tasks.data.total}
                    onClick={() => {
                      setPage(page + 1)
                      setSelectedId(null)
                    }}
                  >
                    {t('Next')}
                  </Button>
                </div>
              )}
            </div>
          )}
          {query.data ? (
            <RecordDetailContent
              key={query.data.record?.id ?? 'missing'}
              detail={query.data}
              taskId={props.taskId}
              admin={props.isAdmin ?? false}
              onSelect={setSelectedId}
            />
          ) : (
            <EmptyState
              title={t('Body not captured')}
              description={t(
                'Recording may have been disabled when this request was made.'
              )}
            />
          )}
          {(!query.data?.record ||
            query.data.record.body_state === 'unavailable') && (
            <Button
              variant='outline'
              onClick={() => {
                if (props.taskId) void tasks.refetch()
                else void query.refetch()
              }}
            >
              {t('Refresh')}
            </Button>
          )}
        </div>
      )}
    </Dialog>
  )
}
