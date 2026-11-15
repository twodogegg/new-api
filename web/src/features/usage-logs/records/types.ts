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
export interface RequestRecord {
  id: number
  user_id: number
  request_id: string
  model_name: string
  kind: string
  operation: string
  request_path: string
  conversation_id: string
  run_id: string
  task_id: string
  parent_request_id: string
  status: string
  body_state: string
  created_at: number
  expires_at: number
  duration_ms: number
}

export interface RecordSnapshot {
  stage: string
  content_type: string
  attempt: number
  status?: number
  state: string
  body?: unknown
}

export interface RecordMedia {
  blob_id: string
  mime_type: string
  size: number
  stage: string
  path: string
}

export interface RequestRecordDetail {
  record: RequestRecord | null
  snapshots: RecordSnapshot[]
  media: RecordMedia[]
  state?: string
}

export interface RecordMessage {
  id?: string
  role: string
  text: string
  kind: 'text' | 'tool' | 'reasoning' | 'reminder'
  name?: string
}

export interface RequestContentFilters {
  contentKeyword?: string
  requestKind?: string
  bodyState?: string
  conversationId?: string
  runId?: string
}
