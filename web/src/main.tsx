import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { setCreateRoot } from '@arco-design/web-react/es/_util/react-dom'
import '@arco-design/web-react/dist/css/arco.css'
import './index.css'
import '@/locale/i18n'
import { AppLocaleRoot } from '@/locale/AppLocaleRoot'
import '@/stores/colorMode'
import App from '@/App.tsx'

// Arco 浮层在 React 19 下需使用 react-dom/client 的 createRoot（见 arco _util/react-dom setCreateRoot）。
setCreateRoot(createRoot)

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AppLocaleRoot>
      <App />
    </AppLocaleRoot>
  </StrictMode>,
)
