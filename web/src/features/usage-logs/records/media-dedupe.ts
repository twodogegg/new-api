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
import type { RecordMedia } from './types'

function recordMediaGroup(media: RecordMedia) {
  return media.stage.endsWith('_request') ? 'input' : 'output'
}

// The same attachment is captured once per relayed snapshot (client request and
// upstream request share the blob), so collapse duplicates within a display
// group instead of showing two identical inputs.
export function uniqueRecordMedia(media: RecordMedia[]): RecordMedia[] {
  return media.filter(
    (item, index, items) =>
      items.findIndex(
        (candidate) =>
          candidate.blob_id === item.blob_id &&
          recordMediaGroup(candidate) === recordMediaGroup(item)
      ) === index
  )
}
