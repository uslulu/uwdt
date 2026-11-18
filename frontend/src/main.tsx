import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import './lib/stores/themeStore'
import { loadServers } from './lib/store';
import App from './App.tsx'

async function bootstrap() {
  // В браузере вне Wails (vite dev) подставляем мок моста; в продакшен-сборке
  // ветка с import.meta.env.DEV вырезается вместе с самим моком
  if (import.meta.env.DEV && !('runtime' in window)) {
    const { installDevMock } = await import('./lib/devMock')
    installDevMock()
  }
  try { await loadServers(); } catch (e) {
    const root = document.getElementById('root')!;
    root.textContent = `Не удалось открыть профили. Разблокируйте системное хранилище и повторите. ${String(e)}`;
    const retry = document.createElement('button');retry.textContent='Повторить';retry.onclick=() => location.reload();root.append(retry);return;
  }
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
}

bootstrap()
