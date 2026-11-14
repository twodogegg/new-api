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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { formatTimestampToDate } from '@/lib/format'

import { getRelatedRequestRecords } from './api'
import type { RequestRecord } from './types'

export function RelatedRecordList(props: {
  record: RequestRecord
  scope: 'conversation' | 'run'
  admin: boolean
  onSelect: (id: number) => void
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [page, setPage] = useState(1)
  const query = useQuery({
    queryKey: [
      'request-record-related',
      props.record.user_id,
      props.record.conversation_id,
      props.record.run_id,
      props.scope,
      props.admin,
      page,
    ],
    queryFn: () =>
      getRelatedRequestRecords(props.record, props.scope, props.admin, page),
    enabled: open,
    gcTime: 0,
  })
  let content = <LoadingState size='sm' />
  if (query.isError) {
    content = <ErrorState onRetry={() => void query.refetch()} />
  } else if (query.data) {
    content = (
      <div className='flex flex-wrap gap-2'>
        {query.data.items.map((record) => (
          <Button
            key={record.id}
            variant='outline'
            size='sm'
            onClick={() => props.onSelect(record.id)}
          >
            {record.model_name} ·{' '}
            {formatTimestampToDate(record.created_at, 'seconds')}
          </Button>
        ))}
      </div>
    )
  }
  return (
    <Collapsible open={open} onOpenChange={setOpen} className='border-t pt-3'>
      <CollapsibleTrigger render={<Button variant='ghost' />}>
        {t(
          props.scope === 'conversation'
            ? 'Other requests in this conversation'
            : 'Other requests in this Agent run'
        )}
      </CollapsibleTrigger>
      <CollapsibleContent className='space-y-3 pt-3'>
        {content}
        {query.data && query.data.total > 20 && (
          <div className='flex gap-2'>
            <Button
              size='sm'
              variant='outline'
              disabled={page === 1}
              onClick={() => setPage(page - 1)}
            >
              {t('Previous')}
            </Button>
            <Button
              size='sm'
              variant='outline'
              disabled={page * 20 >= query.data.total}
              onClick={() => setPage(page + 1)}
            >
              {t('Next')}
            </Button>
          </div>
        )}
        {props.scope === 'run' && (
          <p className='text-muted-foreground text-xs'>
            {t(
              'Only captured requests are listed. Local tool execution and Skills require client trace reporting.'
            )}
          </p>
        )}
      </CollapsibleContent>
    </Collapsible>
  )
}
