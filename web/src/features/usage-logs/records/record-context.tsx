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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatBytes } from '@/lib/format'

import { recordObject, requestRecordMessages } from './messages'
import { RecordJSON } from './record-reader'
import type { RequestRecordDetail } from './types'

export function RecordContext(props: { detail: RequestRecordDetail }) {
  const { t, i18n } = useTranslation()
  const [selected, setSelected] = useState('instructions')
  const snapshot =
    [...props.detail.snapshots]
      .reverse()
      .find((item) => item.stage === 'upstream_request') ??
    props.detail.snapshots.find((item) => item.stage === 'client_request')
  const body = recordObject(snapshot?.body)
  const messages = requestRecordMessages(body)
  const groups = [
    {
      id: 'instructions',
      label: t('Instructions'),
      value: messages.filter(
        (message) => message.role === 'system' || message.role === 'developer'
      ),
    },
    { id: 'tools', label: t('Tool definitions'), value: body.tools ?? [] },
    {
      id: 'messages',
      label: t('Conversation messages'),
      value: messages.filter(
        (message) => message.role !== 'system' && message.role !== 'developer'
      ),
    },
  ].map((group) => ({
    ...group,
    bytes: new TextEncoder().encode(JSON.stringify(group.value)).length,
  }))
  const active = groups.find((group) => group.id === selected) ?? groups[0]
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Repeated context is stored once and restored here. Storage deduplication is independent of upstream prompt caching.'
        )}
      </p>
      <div
        className='grid gap-2 sm:grid-cols-3'
        role='group'
        aria-label={t('Context composition')}
      >
        {groups.map((group) => (
          <Button
            key={group.id}
            variant={active.id === group.id ? 'secondary' : 'outline'}
            className='h-auto flex-col items-start gap-1 px-3 py-3'
            aria-pressed={active.id === group.id}
            onClick={() => setSelected(group.id)}
          >
            <span>{group.label}</span>
            <span className='text-muted-foreground text-xs'>
              {formatBytes(group.bytes, locale)}
            </span>
          </Button>
        ))}
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Sizes describe the saved text, not billed tokens or the model context limit.'
        )}
      </p>
      <RecordJSON value={active.value} title={active.label} />
    </div>
  )
}
