// Development-only mock of the Wails bridge. Imported by main.tsx only when
// import.meta.env.DEV is true and no real Wails runtime is injected, so the
// UI can be previewed in a plain browser. Never bundled into production:
// the dynamic import behind import.meta.env.DEV is dropped at build time.

type Handler = (data?: unknown) => void;

const listeners = new Map<string, Set<Handler>>();

function emit(event: string, ...data: unknown[]) {
  listeners.get(event)?.forEach(fn => fn(...data));
}

let statsTimer: ReturnType<typeof setInterval> | null = null;

const mockRuntime = {
  EventsOnMultiple(eventName: string, callback: Handler) {
    let set = listeners.get(eventName);
    if (!set) { set = new Set(); listeners.set(eventName, set); }
    set.add(callback);
    return () => { set.delete(callback); };
  },
  EventsEmit(eventName: string, ...data: unknown[]) { emit(eventName, ...data); },
  EventsOn(eventName: string, callback: Handler) {
    return mockRuntime.EventsOnMultiple(eventName, callback);
  },
  BrowserOpenURL(url: string) { window.open(url, '_blank'); },
  LogPrint() {},
  WindowReload() {},
  Quit() {},
};

let connected = false;
let excludes: { value: string; enabled: boolean }[] = [
  { value: 'example.ru', enabled: true },
  { value: '10.0.0.0/8', enabled: false },
];
let russia = { enabled: true, count: 15234, updated: '2026-10-28' };

let cumUp = 0;
let cumDown = 0;

function startStats() {
  stopStats();
  cumUp = 0;
  cumDown = 0;
  statsTimer = setInterval(() => {
    cumUp += 12000 + Math.random() * 40000;
    cumDown += 400000 + Math.random() * 600000;
    emit('stats', { active: 4, bytes_up: Math.round(cumUp), bytes_down: Math.round(cumDown) });
  }, 1000);
}
function stopStats() { if (statsTimer) { clearInterval(statsTimer); statsTimer = null; } }

const mockApp: Record<string, (...args: unknown[]) => Promise<unknown>> = {
  ListProfiles: async () => ({}),
  SaveProfile: async () => {},
  DeleteProfile: async () => {},
  GetProfile: async () => null,
  Connect: async () => {
    emit('state_changed', 'connecting');
    emit('stage', 'Резолвлю адрес сервера…');
    setTimeout(() => emit('stage', 'Создаю TURN-сессию…'), 500);
    setTimeout(() => {
      connected = true;
      emit('state_changed', 'connected');
      startStats();
    }, 1400);
  },
  ConnectXray: async () => {
    emit('state_changed', 'connecting');
    emit('stage', 'Запускаю Xray…');
    setTimeout(() => {
      connected = true;
      emit('state_changed', 'connected');
      startStats();
    }, 1100);
  },
  Disconnect: async () => {
    connected = false;
    stopStats();
    emit('state_changed', 'stopped');
  },
  IsRunning: async () => connected,
  GetAutoStart: async () => true,
  SetAutoStart: async () => {},
  GetObfsAccepted: async () => true,
  SetObfsAccepted: async () => {},
  GetObfsMode: async () => 'audio',
  SetObfsMode: async () => {},
  GetVersion: async () => '0.0.0-dev',
  GetExcludes: async () => excludes,
  SetExcludes: async (list) => { excludes = list as typeof excludes; return excludes; },
  CheckExclude: async (v) => String(v),
  GetRussiaDirect: async () => russia,
  SetRussiaDirect: async (on) => { russia = { ...russia, enabled: Boolean(on) }; return russia; },
  ASNames: async () => ({ AS32590: 'Example Networks' }),
  PingServers: async (links) => Object.fromEntries((links as string[]).map(l => [l, Math.floor(40 + Math.random() * 700)])),
  FetchSubscription: async () => { throw new Error('В dev-режиме подписки не загружаются'); },
  SaveLogs: async () => '/tmp/uwdt-dev.log',
  GenerateReport: async () => '# Отчёт UWDT (dev)\n\nТестовый отчёт.',
  CheckUpdate: async () => ({ available: false, version: '', body: '', url: '', page: '' }),
  InstallUpdate: async () => {},
};

function seedServers() {
  if (localStorage.getItem('wdtt_servers:v1')) return;
  localStorage.setItem('wdtt_servers:v1', JSON.stringify([
    { id: 'dev-1', name: 'Сервер 1', host: '203.0.113.10:56000', password: '', icon: 'flag-de', hashes: ['a', 'b', '', ''], power: 18 },
    { id: 'dev-2', name: 'Рабочий сервер с длинным названием', host: '198.51.100.7:56000', password: '', icon: 'clover', hashes: ['a', '', '', ''], power: 9 },
    { id: 'dev-3', name: '🇳🇱 Amsterdam 2', host: '', password: '', icon: 'flag-nl', kind: 'xray', link: 'vless://dev-ams', transport: 'xhttp', group: 'My Subscription', subUrl: 'https://example.com/sub', subUpdated: Date.now() },
    { id: 'dev-4', name: '🇫🇮 Helsinki', host: '', password: '', icon: 'flag-fi', kind: 'xray', link: 'vless://dev-hel', transport: 'grpc', group: 'My Subscription', subUrl: 'https://example.com/sub', subUpdated: Date.now() },
  ]));
}

export function installDevMock() {
  seedServers();
  (window as unknown as { runtime: unknown }).runtime = mockRuntime;
  (window as unknown as { go: unknown }).go = { backend: { App: mockApp } };
}
