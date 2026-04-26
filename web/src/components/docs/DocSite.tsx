import './docs-ui.css'

import { Empty, Layout, Space, Spin, Tag, Typography } from '@arco-design/web-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { DocsNavFile, DocsNavItem, DocsPageFile, DocsSearchEntry } from '@/components/docs/types'
import type { OpenApiDoc } from '@/components/docs/openapi'
import { listOperations } from '@/components/docs/openapi'
import { DocBlocks } from '@/components/docs/DocBlocks'
import { DocsSearchModal } from '@/components/docs/DocsSearchModal'
import { DocsSidebar } from '@/components/docs/DocsSidebar'

const { Title, Paragraph } = Typography

function flattenNavItems(nav: DocsNavFile): DocsNavItem[] {
  return nav.groups.flatMap((g) => g.items)
}

export function DocSite() {
  const [sp, setSp] = useSearchParams()
  const pageId = sp.get('p') ?? ''

  const [nav, setNav] = useState<DocsNavFile | null>(null)
  const [openapi, setOpenapi] = useState<OpenApiDoc | null>(null)
  const [page, setPage] = useState<DocsPageFile | null>(null)
  const [pageLoading, setPageLoading] = useState(false)
  const [pageError, setPageError] = useState<string | null>(null)
  const [navError, setNavError] = useState<string | null>(null)

  const [searchOpen, setSearchOpen] = useState(false)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const [navRes, specRes] = await Promise.all([
          fetch('/docs/nav.json', { credentials: 'same-origin' }),
          fetch('/openapi.json', { credentials: 'same-origin' }),
        ])
        if (!navRes.ok) throw new Error(`nav.json HTTP ${navRes.status}`)
        const navJson = (await navRes.json()) as DocsNavFile
        if (cancelled) return
        setNav(navJson)
        setNavError(null)
        if (specRes.ok) {
          const spec = (await specRes.json()) as OpenApiDoc
          if (!cancelled) setOpenapi(spec)
        }
      } catch (e) {
        if (!cancelled) setNavError(e instanceof Error ? e.message : '加载导航失败')
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  const allItems = useMemo(() => (nav ? flattenNavItems(nav) : []), [nav])

  const openApiKeyToNavId = useMemo(() => {
    const m = new Map<string, string>()
    for (const it of allItems) {
      if (it.openApiKey) m.set(it.openApiKey, it.id)
    }
    return m
  }, [allItems])

  const keywordBoost = useMemo(() => {
    const m = new Map<string, string>()
    if (!openapi) return m
    for (const op of listOperations(openapi)) {
      const k = `${op.method}:${op.path}`
      const id = openApiKeyToNavId.get(k)
      if (!id) continue
      const chunk = [op.summary, op.operationId, ...(op.tags ?? [])].filter(Boolean).join(' ')
      m.set(id, `${m.get(id) ?? ''} ${chunk}`.trim())
    }
    return m
  }, [openapi, openApiKeyToNavId])

  useEffect(() => {
    if (!nav || allItems.length === 0) return
    const valid = allItems.some((i) => i.id === pageId)
    if (!valid) {
      setSp({ p: allItems[0].id }, { replace: true })
    }
  }, [nav, pageId, allItems, setSp])

  const currentItem = useMemo(
    () => allItems.find((i) => i.id === pageId) ?? allItems[0],
    [allItems, pageId],
  )

  useEffect(() => {
    if (!currentItem) {
      setPage(null)
      return
    }
    let cancelled = false
    setPageLoading(true)
    setPageError(null)
    ;(async () => {
      try {
        const res = await fetch(`/docs/pages/${currentItem.page}.json`, { credentials: 'same-origin' })
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        const json = (await res.json()) as DocsPageFile
        if (!cancelled) {
          setPage(json)
          setPageError(null)
        }
      } catch (e) {
        if (!cancelled) setPageError(e instanceof Error ? e.message : '加载页面失败')
      } finally {
        if (!cancelled) setPageLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [currentItem])

  const searchEntries = useMemo((): DocsSearchEntry[] => {
    if (!nav) return []
    const out: DocsSearchEntry[] = []
    for (const g of nav.groups) {
      for (const it of g.items) {
        const boost = keywordBoost.get(it.id) ?? ''
        out.push({
          id: it.id,
          label: it.label,
          subtitle: `${g.title} · ${it.openApiKey ?? it.page}`,
          keywords: `${it.keywords ?? ''} ${it.page} ${it.badge ?? ''} ${boost}`.trim(),
        })
      }
    }
    return out
  }, [nav, keywordBoost])

  const goPage = useCallback(
    (id: string) => {
      setSp({ p: id })
    },
    [setSp],
  )

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.key.toLowerCase() !== 'k') return
      const el = e.target as HTMLElement | null
      if (el?.closest?.('input, textarea, [contenteditable=true]')) return
      e.preventDefault()
      setSearchOpen((v) => !v)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  if (navError) {
    return (
      <div className="flex h-full items-center justify-center p-8">
        <Empty description={navError} />
      </div>
    )
  }

  if (!nav) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spin size={32} tip="加载文档配置…" />
      </div>
    )
  }

  const sidebarWidth = 270

  return (
    <Layout className="flex h-full min-h-0 flex-row bg-[var(--color-bg-1)]">
      <Layout.Sider
        width={sidebarWidth}
        className="!min-w-0 !border-0 !bg-transparent !shadow-none"
        style={{ width: sidebarWidth, minWidth: sidebarWidth, maxWidth: sidebarWidth }}
      >
        <div className="h-full min-h-0 border-r border-[var(--color-border-2)]">
          <DocsSidebar
            nav={nav}
            pageId={pageId}
            openapi={openapi}
            onOpenSearch={() => setSearchOpen(true)}
            onSelectPage={goPage}
          />
        </div>
      </Layout.Sider>
      <Layout.Content className="min-h-0 min-w-0 overflow-y-auto">
        <div className="mx-auto max-w-[900px] px-6 py-10 sm:px-10">
          {pageLoading ? (
            <div className="flex justify-center py-20">
              <Spin tip="加载页面…" />
            </div>
          ) : pageError ? (
            <Empty description={pageError} />
          ) : page ? (
            <>
              <div className="mb-8 border-b border-[var(--color-border-2)] pb-8">
                <Space size={8} wrap className="!mb-2">
                  {currentItem?.badge ? (
                    <Tag color={currentItem.badgeColor ?? 'arcoblue'}>{currentItem.badge}</Tag>
                  ) : null}
                  {openapi ? (
                    <Tag size="small" color="green">
                      OpenAPI 已同步
                    </Tag>
                  ) : (
                    <Tag size="small" color="gray">
                      OpenAPI 未加载
                    </Tag>
                  )}
                </Space>
                <Title heading={2} className="!mb-2 !mt-0 !text-[26px] !font-semibold !tracking-tight">
                  {page.title}
                </Title>
                {page.subtitle ? (
                  <Paragraph className="!mb-0 !text-[15px] !leading-relaxed text-[var(--color-text-2)]">{page.subtitle}</Paragraph>
                ) : null}
              </div>
              <DocBlocks blocks={page.blocks} openapi={openapi} />
            </>
          ) : (
            <Empty description="未选择页面" />
          )}
        </div>
      </Layout.Content>

      <DocsSearchModal visible={searchOpen} onClose={() => setSearchOpen(false)} entries={searchEntries} onPick={goPage} />
    </Layout>
  )
}
