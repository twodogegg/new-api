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
import { expect, test } from 'vitest'

import { uniqueRecordMedia } from '../media-dedupe'
import type { RecordMedia } from '../types'

function media(blobId: string, stage: string): RecordMedia {
  return {
    blob_id: blobId,
    stage,
    mime_type: 'image/webp',
    size: 1,
    path: '/messages/1/content/1/image_url/url',
  }
}

test('collapses one attachment captured in both request snapshot stages', () => {
  expect(
    uniqueRecordMedia([
      media('a', 'client_request'),
      media('a', 'upstream_request'),
    ])
  ).toHaveLength(1)
})

test('keeps distinct attachments and separate input/output media', () => {
  expect(
    uniqueRecordMedia([
      media('a', 'client_request'),
      media('b', 'client_request'),
    ])
  ).toHaveLength(2)
  expect(
    uniqueRecordMedia([
      media('a', 'client_request'),
      media('a', 'client_response'),
    ])
  ).toHaveLength(2)
})
