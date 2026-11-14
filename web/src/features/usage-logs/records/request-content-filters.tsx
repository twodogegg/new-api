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

import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import {
  LogsFilterField,
  LogsFilterInput,
} from '../components/logs-filter-toolbar'
import type { RequestContentFilters } from './types'

export function RequestContentFilterFields(props: {
  filters: RequestContentFilters
  onChange: (key: keyof RequestContentFilters, value: string) => void
  onKeyDown: React.KeyboardEventHandler
}) {
  const { t } = useTranslation()
  return (
    <>
      <LogsFilterField wide>
        <LogsFilterInput
          aria-label={t('Search recorded content')}
          placeholder={t('Search prompts, replies, tool messages, or errors')}
          maxLength={512}
          value={props.filters.contentKeyword ?? ''}
          onChange={(event) =>
            props.onChange('contentKeyword', event.target.value)
          }
          onKeyDown={props.onKeyDown}
        />
      </LogsFilterField>
      <LogsFilterField>
        <NativeSelect
          aria-label={t('Request type')}
          value={props.filters.requestKind ?? ''}
          onChange={(event) =>
            props.onChange('requestKind', event.target.value)
          }
        >
          <NativeSelectOption value=''>
            {t('All request types')}
          </NativeSelectOption>
          <NativeSelectOption value='llm'>LLM</NativeSelectOption>
          <NativeSelectOption value='image'>{t('Image')}</NativeSelectOption>
          <NativeSelectOption value='video'>{t('Video')}</NativeSelectOption>
          <NativeSelectOption value='audio'>{t('Audio')}</NativeSelectOption>
          <NativeSelectOption value='task'>
            {t('Other tasks')}
          </NativeSelectOption>
        </NativeSelect>
      </LogsFilterField>
      <LogsFilterField>
        <NativeSelect
          aria-label={t('Body recording status')}
          value={props.filters.bodyState ?? ''}
          onChange={(event) => props.onChange('bodyState', event.target.value)}
        >
          <NativeSelectOption value=''>
            {t('All recording states')}
          </NativeSelectOption>
          <NativeSelectOption value='available'>
            {t('Body saved')}
          </NativeSelectOption>
          <NativeSelectOption value='truncated'>
            {t('Recording truncated')}
          </NativeSelectOption>
          <NativeSelectOption value='expired'>
            {t('Recording expired')}
          </NativeSelectOption>
        </NativeSelect>
      </LogsFilterField>
      <LogsFilterField>
        <LogsFilterInput
          aria-label={t('Conversation ID')}
          placeholder={t('Conversation ID')}
          value={props.filters.conversationId ?? ''}
          onChange={(event) =>
            props.onChange('conversationId', event.target.value)
          }
          onKeyDown={props.onKeyDown}
        />
      </LogsFilterField>
      <LogsFilterField>
        <LogsFilterInput
          aria-label={t('Agent run ID')}
          placeholder={t('Agent run ID')}
          value={props.filters.runId ?? ''}
          onChange={(event) => props.onChange('runId', event.target.value)}
          onKeyDown={props.onKeyDown}
        />
      </LogsFilterField>
    </>
  )
}
