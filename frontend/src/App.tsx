import { useEffect, useState } from 'react';
import { MotionConfig } from 'motion/react';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import Connect from './pages/Connect';
import Logs from './pages/Logs';
import Toast from './components/Toast';
import UpdateModal from './modals/UpdateModal';
import { wdttLinkStore, parseWdttUrl } from './lib/utils/wdttLink';
import { toastStore } from './lib/stores/toastStore';
import { logStore } from './lib/stores/logStore';
import { tunnelStore } from './lib/stores/tunnelStore';
import type { LogLevel } from './lib/stores/logStore';
import { EventsOn } from '../wailsjs/runtime/runtime';
import { statsStore, sessionClock } from './lib/stores/statsStore';
import { stageStore, degradedStore } from './lib/stores/stageStore';

function useWdttPaste() {
  useEffect(() => {
    const handler = (e: ClipboardEvent) => {
      const text = e.clipboardData?.getData('text') ?? '';
      const trimmed = text.trim();
      if (!trimmed.startsWith('qwdtt://') && !trimmed.startsWith('wdtt://')) return;
      const tag = (document.activeElement as HTMLElement)?.tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA') return;
      e.preventDefault();
      const link = parseWdttUrl(text.trim());
      if (!link) { toastStore.show('Неверный формат ссылки'); return; }
      wdttLinkStore.set(link);
    };
    document.addEventListener('paste', handler);
    document.body.tabIndex = 0;
    return () => document.removeEventListener('paste', handler);
  }, []);
}

function useWailsEvents() {
  useEffect(() => {
    const offs = [
      EventsOn('log', (level: unknown, msg: unknown) => {
        logStore.push((level as LogLevel) ?? 'INFO', String(msg ?? ''));
      }),
      EventsOn('error', (msg: unknown) => {
        const s = String(msg ?? '');
        logStore.push('ERROR', s);
        toastStore.show(s, 5000);
      }),
      EventsOn('state_changed', (status: unknown) => {
        const s = String(status ?? '');
        if (s === 'connected' || s === 'running') { sessionClock.start(); tunnelStore.set('connected'); stageStore.set(''); logStore.push('INFO', '✓ Туннель активен'); }
        else if (s === 'connecting') { sessionClock.stop(); tunnelStore.set('connecting'); statsStore.reset(); stageStore.set('Запускаю подключение…'); logStore.clear(); logStore.push('INFO', '⟳ Подключение...'); }
        else if (s === 'stopped' || s === 'error' || s === 'disconnected') { sessionClock.stop(); degradedStore.set(false); tunnelStore.set('idle'); statsStore.reset(); stageStore.set(''); logStore.push('INFO', '— Отключено'); }
      }),
      EventsOn('event', (name: unknown) => {
        if (name === 'wg_config') { sessionClock.start(); tunnelStore.set('connected'); }
      }),
      EventsOn('degraded', (v: unknown) => {
        degradedStore.set(v === true);
        if (v === true) toastStore.show('Нет связи с серверами ВК — интернет пока напрямую, восстанавливаю', 6000);
      }),
      EventsOn('stage', (text: unknown) => {
        if (tunnelStore.get() === 'connecting') stageStore.set(String(text ?? ''));
      }),
      EventsOn('captcha_window', (state: unknown) => {
        if (state === 'open') toastStore.show('ВКонтакте просит капчу — решите её в открывшемся окне', 6000);
      }),
      EventsOn('stats', (raw: unknown) => {
        statsStore.push((raw ?? {}) as { active?: number; bytes_up?: number; bytes_down?: number });
      }),
    ];
    return () => offs.forEach(off => off());
  }, []);
}

// Обновления ищет Go (раз в час и после сна) и присылает событие update_available:
// таймеры страницы macOS замораживает, пока окно свёрнуто в трей
type UpdateView = { version: string; body: string; url: string; page: string };

function useUpdateChecker() {
  const [updateInfo, setUpdateInfo] = useState<UpdateView | null>(null);

  useEffect(() => {
    const show = (info: { available: boolean; version: string; body: string; url: string; page: string }) => {
      if (info?.available) setUpdateInfo({ version: info.version, body: info.body, url: info.url, page: info.page });
    };
    const off = EventsOn('update_available', (info: unknown) => show(info as Parameters<typeof show>[0]));
    return () => off();
  }, []);

  return { updateInfo, closeUpdate: () => setUpdateInfo(null) };
}

export default function App() {
  useWailsEvents();
  useWdttPaste();
  const { updateInfo, closeUpdate } = useUpdateChecker();

  // reducedMotion="user": при «Уменьшить движение» в системе анимации Motion выключаются
  return (
    <MotionConfig reducedMotion="user">
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<Connect />} />
          <Route path="/logs" element={<Logs />} />
        </Route>
      </Routes>
      <Toast />
      {updateInfo && (
        <UpdateModal
          version={updateInfo.version}
          body={updateInfo.body}
          url={updateInfo.url}
          page={updateInfo.page}
          onClose={closeUpdate}
        />
      )}
    </BrowserRouter>
    </MotionConfig>
  );
}
