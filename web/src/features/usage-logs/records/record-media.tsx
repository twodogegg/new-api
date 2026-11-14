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
import { Download } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatBytes } from '@/lib/format'

import { ArtifactMedia } from '../components/task-artifacts'
import type { TaskArtifactType } from '../types'
import { getRecordMedia } from './api'
import type { RecordMedia, RequestRecordDetail } from './types'

function RecordMediaItem(props: { recordId: number; media: RecordMedia }) {
  const { t, i18n } = useTranslation()
  const [url, setUrl] = useState('')
  const query = useQuery({
    queryKey: ['request-record-media', props.recordId, props.media.blob_id],
    queryFn: () => getRecordMedia(props.recordId, props.media.blob_id),
    retry: false,
    gcTime: 0,
  })
  useEffect(() => {
    if (!query.data) return
    const objectUrl = URL.createObjectURL(query.data)
    setUrl(objectUrl)
    return () => URL.revokeObjectURL(objectUrl)
  }, [query.data])
  if (query.isPending) return <LoadingState size='sm' />
  if (query.isError) {
    return (
      <ErrorState
        title={t('Media preview unavailable')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  let type: TaskArtifactType = 'file'
  if (props.media.mime_type.startsWith('image/')) type = 'image'
  if (props.media.mime_type.startsWith('video/')) type = 'video'
  if (props.media.mime_type.startsWith('audio/')) type = 'audio'
  return (
    <div className='min-w-0 space-y-3 rounded-lg border p-3'>
      <p className='text-muted-foreground text-xs'>
        {props.media.mime_type} ·{' '}
        {formatBytes(
          props.media.size,
          toIntlLocale(i18n.resolvedLanguage || i18n.language)
        )}
      </p>
      {url && (
        <ArtifactMedia
          artifact={{
            key: props.media.blob_id,
            type,
            content_url: url,
            mime_type: props.media.mime_type,
          }}
          mediaUrl={url}
          onError={() => setUrl('')}
        />
      )}
      {!url && !query.isFetching && (
        <p className='text-muted-foreground text-sm'>
          {t('Media preview unavailable')}
        </p>
      )}
      {url && (
        <Button
          variant='outline'
          size='sm'
          nativeButton={false}
          render={<a href={url} download={`record-${props.media.blob_id}`} />}
        >
          <Download />
          {t('Download')}
        </Button>
      )}
    </div>
  )
}

export function RecordMediaGrid(props: { detail: RequestRecordDetail }) {
  const { t } = useTranslation()
  const unique = props.detail.media.filter(
    (media, index, items) =>
      items.findIndex(
        (item) => item.blob_id === media.blob_id && item.stage === media.stage
      ) === index
  )
  if (!unique.length || !props.detail.record) {
    return (
      <EmptyState
        title={t('No saved media')}
        description={t('Media may not have been captured or may have expired.')}
      />
    )
  }
  const input = unique.filter((media) => media.stage.endsWith('_request'))
  const recordId = props.detail.record.id
  const output = unique.filter((media) => !media.stage.endsWith('_request'))
  return (
    <div className='space-y-6'>
      {[
        { label: t('Input references'), items: input },
        { label: t('Generated results'), items: output },
      ].map(
        (group) =>
          group.items.length > 0 && (
            <section key={group.label} className='space-y-3'>
              <h3 className='text-sm font-medium'>{group.label}</h3>
              <div className='grid min-w-0 gap-3 md:grid-cols-2'>
                {group.items.map((media) => (
                  <RecordMediaItem
                    key={`${media.stage}:${media.blob_id}`}
                    recordId={recordId}
                    media={media}
                  />
                ))}
              </div>
            </section>
          )
      )}
    </div>
  )
}
