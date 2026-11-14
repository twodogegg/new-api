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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { RequestRecord, RequestRecordDetail } from './types'

export async function lookupRequestRecord(requestId: string, userId?: number) {
  const response = await api.get<{
    success: boolean
    message?: string
    data: RequestRecordDetail
  }>('/api/request-record/lookup', {
    params: { request_id: requestId, user_id: userId },
  })
  return requireServerSuccess(response.data).data
}

export async function getRequestRecord(id: number) {
  const response = await api.get<{
    success: boolean
    message?: string
    data: RequestRecordDetail
  }>(`/api/request-record/${id}`)
  return requireServerSuccess(response.data).data
}

export async function getTaskRequestRecords(
  taskId: string,
  page = 1,
  includePolls = false
) {
  const response = await api.get<{
    success: boolean
    message?: string
    data: {
      items: RequestRecord[]
      total: number
      page_size: number
      poll_count: number
    }
  }>(`/api/task/${encodeURIComponent(taskId)}/records`, {
    params: { p: page, page_size: 20, include_polls: includePolls },
  })
  return requireServerSuccess(response.data).data
}

export async function getRelatedRequestRecords(
  record: RequestRecord,
  scope: 'conversation' | 'run',
  admin: boolean,
  page = 1
) {
  const response = await api.get<{
    success: boolean
    message?: string
    data: { items: RequestRecord[]; total: number; page_size: number }
  }>('/api/request-record/list', {
    params: {
      conversation_id:
        scope === 'conversation' ? record.conversation_id : undefined,
      run_id: scope === 'run' ? record.run_id : undefined,
      scope: admin ? 'all' : 'self',
      user_id: record.user_id,
      p: page,
      page_size: 20,
    },
  })
  return requireServerSuccess(response.data).data
}

export async function getRecordMedia(recordId: number, blobId: string) {
  const response = await api.get<Blob>(
    `/api/request-record/${recordId}/media/${encodeURIComponent(blobId)}`,
    { responseType: 'blob' }
  )
  return response.data
}
