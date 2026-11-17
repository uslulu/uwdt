import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import './lib/stores/themeStore'
import App from './App.tsx'

async function bootstrap() {
  // В браузере вне Wails (vite dev) подставляем мок моста; в продакшен-сборке
  // ветка с import.meta.env.DEV вырезается вместе с самим моком
  if (import.meta.env.DEV && !('runtime' in window)) {
    const { installDevMock } = await import('./lib/devMock')
    installDevMock()
  }
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
}

bootstrap()
