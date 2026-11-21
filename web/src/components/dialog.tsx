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
import { ArrowDown, ArrowUp } from 'lucide-react'
import * as React from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog as DialogRoot,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { cn } from '@/lib/utils'

type DialogProps = React.ComponentProps<typeof DialogRoot> & {
  title: React.ReactNode
  description?: React.ReactNode
  children: React.ReactNode
  trigger?: React.ReactElement
  footer?: React.ReactNode
  contentHeight?: React.CSSProperties['height']
  contentClassName?: string
  headerClassName?: string
  titleClassName?: string
  descriptionClassName?: string
  bodyClassName?: string
  footerClassName?: string
  initialFocus?: boolean
  showCloseButton?: boolean
  /** Show floating buttons that jump to the top or bottom of the body. */
  scrollControls?: boolean
}

const dialogContentMotionClassName =
  'data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95 duration-100'

export function Dialog({
  title,
  description,
  children,
  trigger,
  footer,
  contentHeight = 'auto',
  contentClassName,
  headerClassName,
  titleClassName,
  descriptionClassName,
  bodyClassName,
  footerClassName,
  initialFocus,
  showCloseButton,
  scrollControls = false,
  ...dialogProps
}: DialogProps) {
  const { t } = useTranslation()
  // The dialog body is mounted lazily inside a portal, so track it with a
  // callback ref instead of a ref read from the first effect run.
  const [body, setBody] = React.useState<HTMLDivElement | null>(null)
  const [edges, setEdges] = React.useState({
    scrollable: false,
    atTop: true,
    atBottom: true,
  })

  React.useEffect(() => {
    if (!scrollControls || !body) return
    const update = () => {
      const max = body.scrollHeight - body.clientHeight
      const next = {
        scrollable: max > 4,
        atTop: body.scrollTop <= 4,
        atBottom: body.scrollTop >= max - 4,
      }
      setEdges((current) =>
        current.scrollable === next.scrollable &&
        current.atTop === next.atTop &&
        current.atBottom === next.atBottom
          ? current
          : next
      )
    }
    update()
    body.addEventListener('scroll', update, { passive: true })
    const observer = new ResizeObserver(update)
    observer.observe(body)
    if (body.firstElementChild) observer.observe(body.firstElementChild)
    return () => {
      body.removeEventListener('scroll', update)
      observer.disconnect()
    }
  }, [scrollControls, body])

  return (
    <DialogRoot {...dialogProps}>
      {trigger ? <DialogTrigger render={trigger} /> : null}
      <DialogContent
        className={cn(
          'flex w-full flex-col gap-4 overflow-x-hidden overflow-y-auto p-4 sm:max-w-2xl sm:p-6',
          contentClassName,
          dialogContentMotionClassName
        )}
        initialFocus={initialFocus}
        showCloseButton={showCloseButton}
        style={
          {
            '--dialog-content-height': contentHeight,
          } as React.CSSProperties
        }
      >
        <DialogHeader
          className={cn('flex-shrink-0 text-start', headerClassName)}
        >
          <DialogTitle className={titleClassName}>{title}</DialogTitle>
          {description ? (
            <DialogDescription className={descriptionClassName}>
              {description}
            </DialogDescription>
          ) : null}
        </DialogHeader>

        <div className='relative min-h-0'>
          <div
            ref={setBody}
            className={cn(
              '-mx-1 min-h-0 overflow-x-hidden overflow-y-auto overscroll-contain',
              'h-[var(--dialog-content-height)]'
            )}
          >
            <div
              className={cn(
                'min-w-0 px-1 py-1',
                '[&_form]:overflow-x-visible',
                '[&_[data-slot=scroll-area-viewport]]:px-1 [&_[data-slot=scroll-area-viewport]]:py-1',
                bodyClassName
              )}
            >
              {children}
            </div>
          </div>
          {scrollControls && edges.scrollable && (
            <div className='pointer-events-none absolute right-3 bottom-3 z-10 flex flex-col gap-1.5'>
              <Button
                type='button'
                variant='secondary'
                size='icon-sm'
                className='pointer-events-auto size-8 rounded-full border shadow-sm'
                disabled={edges.atTop}
                aria-label={t('Scroll to top')}
                onClick={() => body?.scrollTo({ top: 0, behavior: 'smooth' })}
              >
                <ArrowUp className='size-4' />
              </Button>
              <Button
                type='button'
                variant='secondary'
                size='icon-sm'
                className='pointer-events-auto size-8 rounded-full border shadow-sm'
                disabled={edges.atBottom}
                aria-label={t('Scroll to bottom')}
                onClick={() =>
                  body?.scrollTo({ top: body.scrollHeight, behavior: 'smooth' })
                }
              >
                <ArrowDown className='size-4' />
              </Button>
            </div>
          )}
        </div>

        {footer ? (
          <DialogFooter
            className={cn(
              'flex-shrink-0 gap-2 sm:-mx-6 sm:-mb-6 sm:justify-end sm:p-6',
              footerClassName
            )}
          >
            {footer}
          </DialogFooter>
        ) : null}
      </DialogContent>
    </DialogRoot>
  )
}
