import CodeMirror from '@uiw/react-codemirror'
import { vscodeLight, vscodeDark } from '@uiw/codemirror-theme-vscode'
import { html } from '@codemirror/lang-html'
import { javascript } from '@codemirror/lang-javascript'
import { EditorView } from '@codemirror/view'
import { useEffect, useState } from 'react'
import { cn } from '@/lib/cn'

const langExtensions = (language: string) => {
  const l = language.toLowerCase()
  if (l === 'html' || l === 'xml') return [html()]
  const isTs = l === 'ts' || l === 'tsx'
  const isJsx = l === 'jsx' || l === 'tsx'
  if (
    l === 'javascript' ||
    l === 'js' ||
    l === 'typescript' ||
    l === 'ts' ||
    l === 'tsx' ||
    l === 'jsx' ||
    l === 'json' ||
    l === 'bash' ||
    l === 'shell' ||
    l === 'sh'
  ) {
    return [javascript({ jsx: isJsx, typescript: isTs })]
  }
  return [javascript({ jsx: false, typescript: false })]
}

function useArcoDark(): boolean {
  const [dark, setDark] = useState(() => document.body.getAttribute('arco-theme') === 'dark')
  useEffect(() => {
    const obs = new MutationObserver(() => {
      setDark(document.body.getAttribute('arco-theme') === 'dark')
    })
    obs.observe(document.body, { attributes: true, attributeFilter: ['arco-theme'] })
    return () => obs.disconnect()
  }, [])
  return dark
}

export interface DocCodeEditorProps {
  value: string
  onChange?: (v: string) => void
  language?: string
  readOnly?: boolean
  height?: string
  minHeight?: string
  className?: string
}

/**
 * 文档站统一代码编辑器：随 Arco 亮/暗主题切换 CodeMirror 主题，背景与边框使用设计变量。
 */
export function DocCodeEditor({
  value,
  onChange,
  language = 'json',
  readOnly = true,
  height,
  minHeight = '120px',
  className,
}: DocCodeEditorProps) {
  const dark = useArcoDark()
  const ext = langExtensions(language)
  const theme = dark ? vscodeDark : vscodeLight

  return (
    <CodeMirror
      value={value}
      height={height ?? minHeight}
      theme={theme}
      editable={!readOnly}
      basicSetup={{
        lineNumbers: !readOnly,
        foldGutter: !readOnly,
        highlightActiveLine: !readOnly,
        highlightActiveLineGutter: !readOnly,
      }}
      extensions={[
        ...ext,
        EditorView.theme({
          '&': {
            fontSize: '13px',
            backgroundColor: 'var(--color-fill-2)',
          },
          '.cm-scroller': {
            fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
            lineHeight: '1.55',
          },
          '.cm-gutters': {
            backgroundColor: 'var(--color-fill-3)',
            color: 'var(--color-text-3)',
            borderRight: '1px solid var(--color-border-2)',
          },
          '.cm-activeLineGutter': {
            backgroundColor: 'var(--color-fill-2)',
          },
          '.cm-activeLine': {
            backgroundColor: 'var(--color-fill-1)',
          },
        }),
      ]}
      onChange={readOnly ? undefined : onChange}
      className={cn(
        'doc-cm overflow-hidden rounded-[10px] border text-left',
        'border-[var(--color-border-2)]',
        readOnly ? 'opacity-[0.99]' : '',
        className,
      )}
    />
  )
}
