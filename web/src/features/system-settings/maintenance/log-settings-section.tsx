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
import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect, useId, useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'

import {
  SettingsForm,
  SettingsFormGrid,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import { safeNumberFieldProps } from '../utils/numeric-field'
import { HistoryLogCleanup } from './history-log-cleanup'
import { RequestRecordCleanup } from './request-record-cleanup'
import { ServerLogFiles } from './server-log-files'

const logSettingsSchema = z.object({
  LogConsumeEnabled: z.boolean(),
  log_setting: z.object({
    user_export_enabled: z.boolean(),
    max_range_days: z.number().int().min(1),
  }),
  system_task_setting: z.object({
    retention_days: z.number().int().min(1),
  }),
  request_record_setting: z.object({
    enabled: z.boolean(),
    retention_days: z.number().int().min(1).max(3650),
    max_body_mb: z.number().int().min(1).max(64),
  }),
})

type LogSettingsFormValues = z.infer<typeof logSettingsSchema>

type LogSettingsSectionProps = {
  defaultValues: {
    LogConsumeEnabled: boolean
    'log_setting.user_export_enabled': boolean
    'log_setting.max_range_days': number
    'system_task_setting.retention_days': number
    'request_record_setting.enabled'?: boolean
    'request_record_setting.retention_days'?: number
    'request_record_setting.max_body_mb'?: number
  }
}

export function LogSettingsSection(props: LogSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const recordingTitleId = useId()
  const requestRecordingTitleId = useId()
  const searchTitleId = useId()
  const taskHistoryTitleId = useId()
  const formDefaults = useMemo<LogSettingsFormValues>(
    () => ({
      LogConsumeEnabled: props.defaultValues.LogConsumeEnabled,
      log_setting: {
        user_export_enabled:
          props.defaultValues['log_setting.user_export_enabled'],
        max_range_days: props.defaultValues['log_setting.max_range_days'],
      },
      system_task_setting: {
        retention_days:
          props.defaultValues['system_task_setting.retention_days'],
      },
      request_record_setting: {
        enabled: props.defaultValues['request_record_setting.enabled'] ?? false,
        retention_days:
          props.defaultValues['request_record_setting.retention_days'] ?? 7,
        max_body_mb:
          props.defaultValues['request_record_setting.max_body_mb'] ?? 16,
      },
    }),
    [props.defaultValues]
  )
  const form = useForm<LogSettingsFormValues>({
    resolver: zodResolver(logSettingsSchema),
    defaultValues: formDefaults,
  })

  useEffect(() => {
    form.reset(formDefaults)
  }, [formDefaults, form])

  const onSubmit = async (values: LogSettingsFormValues) => {
    const previous = {
      ...props.defaultValues,
      'request_record_setting.enabled':
        formDefaults.request_record_setting.enabled,
      'request_record_setting.retention_days':
        formDefaults.request_record_setting.retention_days,
      'request_record_setting.max_body_mb':
        formDefaults.request_record_setting.max_body_mb,
    }
    const changes = [
      ['LogConsumeEnabled', values.LogConsumeEnabled],
      [
        'log_setting.user_export_enabled',
        values.log_setting.user_export_enabled,
      ],
      ['log_setting.max_range_days', values.log_setting.max_range_days],
      ['request_record_setting.enabled', values.request_record_setting.enabled],
      [
        'request_record_setting.retention_days',
        values.request_record_setting.retention_days,
      ],
      [
        'request_record_setting.max_body_mb',
        values.request_record_setting.max_body_mb,
      ],
      [
        'system_task_setting.retention_days',
        values.system_task_setting.retention_days,
      ],
    ] as const
    for (const [key, value] of changes) {
      if (value === previous[key]) continue
      await updateOption.mutateAsync({ key, value })
    }
  }

  return (
    <SettingsSection title={t('Log Maintenance')} className='gap-6'>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save log settings'
          />
          <div
            role='group'
            aria-labelledby={recordingTitleId}
            className='flex min-w-0 flex-col gap-4'
          >
            <h4 id={recordingTitleId} className='font-medium'>
              {t('Log recording')}
            </h4>
            <FormField
              control={form.control}
              name='LogConsumeEnabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Record quota usage')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Track per-request consumption to power usage analytics. Keeping this on increases database writes.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                  <FormMessage />
                </SettingsSwitchItem>
              )}
            />
          </div>

          <Separator />

          <div
            role='group'
            aria-labelledby={requestRecordingTitleId}
            className='flex min-w-0 flex-col gap-4'
          >
            <h4 id={requestRecordingTitleId} className='font-medium'>
              {t('Request and response recording')}
            </h4>
            <FormField
              control={form.control}
              name='request_record_setting.enabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>
                      {t('Save request and response bodies')}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'Keep prompts, responses, and media in private local files. Repeated context is deduplicated. Recording is disabled by default.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                  <FormMessage />
                </SettingsSwitchItem>
              )}
            />
            <SettingsFormGrid>
              <FormField
                control={form.control}
                name='request_record_setting.retention_days'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('Maximum recording retention (days)')}
                    </FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        max={3650}
                        step={1}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Expired bodies and unreferenced media are cleaned automatically. Usage logs and task summaries remain available.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='request_record_setting.max_body_mb'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('Maximum captured body size (MB)')}
                    </FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        max={64}
                        step={1}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Content above this limit is marked as truncated. Recording failures do not interrupt model requests.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SettingsFormGrid>
          </div>
          <Separator />
          <RequestRecordCleanup />
          <Separator />
          <div
            role='group'
            aria-labelledby={searchTitleId}
            className='flex min-w-0 flex-col gap-4'
          >
            <h4 id={searchTitleId} className='font-medium'>
              {t('Log search and export')}
            </h4>
            <SettingsFormGrid>
              <FormField
                control={form.control}
                name='log_setting.user_export_enabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Allow users to export logs')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Users can download their own logs from the usage log page. Administrators can always export.'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                    <FormMessage />
                  </SettingsSwitchItem>
                )}
              />
              <FormField
                control={form.control}
                name='log_setting.max_range_days'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Longest log time range (days)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        step={1}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'The longest time range a user can view at once in the logs and on the dashboard, and the longest range any export can cover.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SettingsFormGrid>
          </div>

          <Separator />

          <div
            role='group'
            aria-labelledby={taskHistoryTitleId}
            className='flex min-w-0 flex-col gap-4'
          >
            <h4 id={taskHistoryTitleId} className='font-medium'>
              {t('System task records')}
            </h4>
            <SettingsFormGrid>
              <FormField
                control={form.control}
                name='system_task_setting.retention_days'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Keep finished tasks for (days)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        step={1}
                        placeholder='30'
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Finished system tasks older than this are deleted automatically every day.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SettingsFormGrid>
          </div>
        </SettingsForm>
      </Form>

      <Separator />

      <HistoryLogCleanup />

      <Separator />

      <ServerLogFiles />
    </SettingsSection>
  )
}
