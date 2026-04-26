import ReactMarkdown from 'react-markdown'

type Props = {
  content: string
}

/** 助手消息 Markdown 渲染（Agent 长文、计划列表等）。 */
export function MarkdownMessageBody({ content }: Props) {
  return (
    <div className="chat-md-root text-[14px] leading-relaxed text-[var(--color-text-1)]">
      <ReactMarkdown
        components={{
          p: ({ children }) => <p className="mb-2 last:mb-0">{children}</p>,
          ul: ({ children }) => <ul className="mb-2 list-disc pl-5 last:mb-0">{children}</ul>,
          ol: ({ children }) => <ol className="mb-2 list-decimal pl-5 last:mb-0">{children}</ol>,
          li: ({ children }) => <li className="mb-0.5">{children}</li>,
          h1: ({ children }) => <h1 className="mb-2 mt-3 text-[15px] font-semibold first:mt-0">{children}</h1>,
          h2: ({ children }) => <h2 className="mb-2 mt-3 text-[14px] font-semibold first:mt-0">{children}</h2>,
          h3: ({ children }) => <h3 className="mb-1 mt-2 text-[13px] font-semibold first:mt-0">{children}</h3>,
          code: ({ className, children, ...props }) => {
            const isBlock = String(className ?? '').includes('language-')
            if (isBlock) {
              return (
                <pre className="my-2 max-h-64 overflow-auto rounded border border-[var(--color-border-2)] bg-[var(--color-fill-2)] p-2 font-mono text-[12px]">
                  <code className={className} {...props}>
                    {children}
                  </code>
                </pre>
              )
            }
            return (
              <code
                className="rounded bg-[var(--color-fill-3)] px-1 py-0.5 font-mono text-[12px]"
                {...props}
              >
                {children}
              </code>
            )
          },
          pre: ({ children }) => <>{children}</>,
          hr: () => <hr className="my-3 border-[var(--color-border-2)]" />,
          a: ({ href, children }) => (
            <a href={href} className="text-[rgb(var(--primary-6))] underline-offset-2 hover:underline" target="_blank" rel="noreferrer">
              {children}
            </a>
          ),
          blockquote: ({ children }) => (
            <blockquote className="my-2 border-l-2 border-[var(--color-border-3)] pl-3 text-[var(--color-text-2)]">
              {children}
            </blockquote>
          ),
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  )
}
