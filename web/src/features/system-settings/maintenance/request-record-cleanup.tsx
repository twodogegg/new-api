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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatBytes } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { createServerError } from '@/lib/server-error-message'

import { clearRequestRecords, getRequestRecordStorage } from '../api'

/**
 * Shows how much disk the request-record bodies use and lets an administrator
 * delete every saved body/media file in one action.
 */
export function RequestRecordCleanup() {
  const { t, i18n } = useTranslation()
  const titleId = useId()
  const queryClient = useQueryClient()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [clearing, setClearing] = useState(false)
  const storage = useQuery({
    queryKey: ['request-record-storage'],
    queryFn: getRequestRecordStorage,
    retry: false,
  })
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const bytes = storage.data?.data?.bytes
  const usage = typeof bytes === 'number' ? formatBytes(bytes, locale) : '—'

  const handleClear = async () => {
    setClearing(true)
    try {
      const res = await clearRequestRecords()
      if (!res.success) {
        throw createServerError(res, t('Failed to clear request records'))
      }
      setConfirmOpen(false)
      toast.success(t('Request records cleared.'))
      await queryClient.invalidateQueries({
        queryKey: ['request-record-storage'],
      })
    } catch (error) {
      handleServerError(
        error,
        error instanceof Error
          ? error.message
          : t('Failed to clear request records')
      )
    } finally {
      setClearing(false)
    }
  }

  return (
    <div
      role='group'
      aria-labelledby={titleId}
      className='flex min-w-0 flex-col gap-3'
    >
      <div>
        <h4 id={titleId} className='font-medium'>
          {t('Request record storage')}
        </h4>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t(
            'Saved request/response bodies and media are stored in private local files. Clearing deletes all of them; usage logs and task summaries remain.'
          )}
        </p>
      </div>
      <div className='flex flex-wrap items-center gap-3'>
        <span className='text-sm'>
          {t('Currently using {{size}}.', { size: usage })}
        </span>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => void storage.refetch()}
          disabled={storage.isFetching}
        >
          {t('Refresh')}
        </Button>
        <Button
          type='button'
          variant='destructive'
          size='sm'
          onClick={() => setConfirmOpen(true)}
          disabled={clearing}
        >
          {clearing ? t('Clearing...') : t('Clear request records')}
        </Button>
      </div>
      {storage.isError && (
        <p className='text-destructive text-xs'>
          {t('Failed to load recording storage usage')}
        </p>
      )}
      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t('Confirm clearing request records')}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'This permanently deletes every saved request/response body and media file. Usage logs and task summaries are kept. This action cannot be undone.'
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={clearing}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              variant='destructive'
              onClick={handleClear}
              disabled={clearing}
            >
              {clearing ? t('Clearing...') : t('Clear records')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
