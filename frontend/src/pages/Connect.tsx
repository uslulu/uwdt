import { useState, useEffect, useRef } from 'react';
import type React from 'react';
import {
  IconCloverFilled, IconPlus, IconChevronUp, IconPencil,
  IconFlameFilled, IconShieldFilled, IconLayoutGridFilled, IconCloudFilled, IconBrandSpeedtest,
  IconStarFilled, IconHeartFilled, IconBoltFilled, IconRocket,
  IconCrownFilled, IconDiamondFilled, IconLeafFilled, IconSnowflake,
  IconServer, IconGlobe, IconLockFilled, IconWifi, IconTrash, IconRefresh, IconBrandVk, IconListDetails,
} from '@tabler/icons-react';
import AddServer from '../modals/Add-server';
import EditServer from '../modals/Edit-server';
import { serverStore, settingsStore } from '../lib/store';
import { tunnelStore } from '../lib/stores/tunnelStore';
import { toastStore } from '../lib/stores/toastStore';
import { logStore } from '../lib/stores/logStore';
import { wdttLinkStore } from '../lib/utils/wdttLink';
import { SaveProfile } from '../../wailsjs/go/backend/App';
import type { Server, TunnelState } from '../lib/types';
import { Connect as WailsConnect, Disconnect as WailsDisconnect, ListProfiles, ConnectXray, FetchSubscription, PingServers, GetRussiaDirect } from '../../wailsjs/go/backend/App';
import type { backend } from '../../wailsjs/go/models';
import Bubble from '../components/Bubble';
import { statsStore, formatRate, formatDuration, sessionClock, type TunnelStats } from '../lib/stores/statsStore';
import { stageStore, degradedStore } from '../lib/stores/stageStore';
import { IconArrowDown, IconArrowUp, IconPlugConnected, IconClock } from '@tabler/icons-react';
import './Connect.css';

const SERVER_ICONS: { key: string; render: (size: number) => React.ReactNode }[] = [
  { key: 'clover',     render: s => <IconCloverFilled size={s} /> },
  { key: 'flame',      render: s => <IconFlameFilled size={s} /> },
  { key: 'shield',     render: s => <IconShieldFilled size={s} /> },
  { key: 'grid',       render: s => <IconLayoutGridFilled size={s} /> },
  { key: 'cloud',      render: s => <IconCloudFilled size={s} /> },
  { key: 'speed',      render: s => <IconBrandSpeedtest size={s} stroke={2} /> },
  { key: 'star',       render: s => <IconStarFilled size={s} /> },
  { key: 'heart',      render: s => <IconHeartFilled size={s} /> },
  { key: 'bolt',       render: s => <IconBoltFilled size={s} /> },
  { key: 'rocket',     render: s => <IconRocket size={s} stroke={2} /> },
  { key: 'crown',      render: s => <IconCrownFilled size={s} /> },
  { key: 'diamond',    render: s => <IconDiamondFilled size={s} /> },
  { key: 'leaf',       render: s => <IconLeafFilled size={s} /> },
  { key: 'snowflake',  render: s => <IconSnowflake size={s} stroke={2} /> },
  { key: 'server',     render: s => <IconServer size={s} stroke={2} /> },
  { key: 'globe',      render: s => <IconGlobe size={s} stroke={2} /> },
  { key: 'lock',       render: s => <IconLockFilled size={s} /> },
  { key: 'wifi',       render: s => <IconWifi size={s} stroke={2} /> },
  { key: 'flag-ru',    render: s => <img src="/flags/ru.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-us',    render: s => <img src="/flags/us.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-de',    render: s => <img src="/flags/de.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-nl',    render: s => <img src="/flags/nl.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-fi',    render: s => <img src="/flags/fi.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-fr',    render: s => <img src="/flags/fr.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-gb',    render: s => <img src="/flags/gb.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-jp',    render: s => <img src="/flags/jp.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-pl',    render: s => <img src="/flags/pl.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-se',    render: s => <img src="/flags/se.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-ch',    render: s => <img src="/flags/ch.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-lt',    render: s => <img src="/flags/lt.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-lv',    render: s => <img src="/flags/lv.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-ee',    render: s => <img src="/flags/ee.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-cz',    render: s => <img src="/flags/cz.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-at',    render: s => <img src="/flags/at.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-ca',    render: s => <img src="/flags/ca.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-au',    render: s => <img src="/flags/au.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-sg',    render: s => <img src="/flags/sg.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-hk',    render: s => <img src="/flags/hk.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-tr',    render: s => <img src="/flags/tr.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
  { key: 'flag-kz',    render: s => <img src="/flags/kz.svg" alt="" width={s} height={s * 0.67} style={{ objectFit: 'cover', borderRadius: 2 }} /> },
];

function ServerIcon({ iconKey, size }: { iconKey?: string; size: number }) {
  const entry = SERVER_ICONS.find(i => i.key === (iconKey ?? 'clover')) ?? SERVER_ICONS[0];
  return <>{entry.render(size)}</>;
}

const TUNNEL_LABEL: Record<TunnelState, string> = {
  idle: 'Подключить',
  connecting: 'Подключение...',
  connected: 'Отключить',
  disconnecting: 'Отключение...',
};

const STATUS_TITLE: Record<TunnelState, string> = {
  idle: 'Не подключено',
  connecting: 'Подключение…',
  connected: 'Подключено',
  disconnecting: 'Отключение…',
};

function PowerButton({ tunnelState, linkFlash, selected, onTunnel }: {
  tunnelState: TunnelState; linkFlash: boolean; selected: Server | null; onTunnel: () => void;
}) {
  return (
    <button
      type="button"
      className={`power-btn${linkFlash ? ' power-btn--flash' : ''}`}
      onClick={onTunnel}
      disabled={!selected}
      title={selected ? TUNNEL_LABEL[tunnelState] : 'Добавьте сервер'}
      aria-label={selected ? TUNNEL_LABEL[tunnelState] : 'Добавьте сервер'}
    >
      <Bubble state={tunnelState} />
    </button>
  );
}

// Часы подключения обновляются раз в секунду, пока карточка на экране
function useSessionDuration(active: boolean) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [active]);
  const since = sessionClock.since();
  return active && since != null ? formatDuration(now - since) : '0:00';
}

const TRANSPORT_LABEL: Record<string, string> = { tcp: 'TCP', raw: 'TCP', xhttp: 'xHTTP', splithttp: 'xHTTP', grpc: 'gRPC', ws: 'WS' };

function StatsCard({ stats, active, transport }: { stats: TunnelStats | null; active: boolean; transport?: string }) {
  const down = formatRate(stats?.downBps ?? 0);
  const up = formatRate(stats?.upBps ?? 0);
  const duration = useSessionDuration(active);
  return (
    <div className="stats-card">
      <div className="stat">
        <span className="stat-label"><IconClock size={14} stroke={2.2} />В сети</span>
        <span className="stat-value">{duration}</span>
      </div>
      <div className="stat">
        <span className="stat-label"><IconArrowDown size={14} stroke={2.4} />Скачивание</span>
        <span className="stat-value">{down.value} <small>{down.unit}</small></span>
      </div>
      <div className="stat">
        <span className="stat-label"><IconArrowUp size={14} stroke={2.4} />Отдача</span>
        <span className="stat-value">{up.value} <small>{up.unit}</small></span>
      </div>
      <div className="stat">
        {transport ? (
          <>
            <span className="stat-label"><IconPlugConnected size={14} stroke={2.2} />Протокол</span>
            <span className="stat-value">{TRANSPORT_LABEL[transport] ?? transport}</span>
          </>
        ) : (
          <>
            <span className="stat-label"><IconPlugConnected size={14} stroke={2.2} />Каналы</span>
            <span className="stat-value">{stats?.active ?? '—'}</span>
          </>
        )}
      </div>
    </div>
  );
}

// Список серверов по разделам: VK TURN отдельно, каждая подписка — своим разделом
type ServerGroup = { key: string; kind: 'turn' | 'sub'; title: string; url?: string; updated?: number; items: Server[] };

function groupServers(servers: Server[]): ServerGroup[] {
  const groups: ServerGroup[] = [];
  const turn = servers.filter(s => s.kind !== 'xray');
  if (turn.length > 0) groups.push({ key: 'turn', kind: 'turn', title: 'VK TURN', items: turn });
  for (const s of servers) {
    if (s.kind !== 'xray') continue;
    const key = s.subUrl ?? 'vless';
    let g = groups.find(x => x.key === key);
    if (!g) {
      g = { key, kind: 'sub', title: s.group || 'Серверы VLESS', url: s.subUrl, updated: s.subUpdated, items: [] };
      groups.push(g);
    }
    g.items.push(s);
  }
  return groups;
}

const plural = (n: number, one: string, few: string, many: string) => {
  const m10 = n % 10, m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
  return many;
};
const serversCount = (n: number) => `${n} ${plural(n, 'сервер', 'сервера', 'серверов')}`;

// Сегодня — время, раньше — дата: подпись раздела должна помещаться в строку
const fmtDate = (t?: number) => {
  if (!t) return '';
  const d = new Date(t);
  return d.toDateString() === new Date().toDateString()
    ? 'в ' + d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })
    : d.toLocaleDateString('ru-RU', { day: 'numeric', month: 'short' });
};

// Задержка до сервера подписки: зелёная/жёлтая/красная пилюля
function PingBadge({ ms, pinging }: { ms?: number; pinging: boolean }) {
  if (ms === undefined) return pinging ? <span className="ping ping--wait">…</span> : null;
  if (ms < 0) return <span className="ping ping--dead" title="Сервер не ответил">нет ответа</span>;
  const cls = ms < 400 ? 'good' : ms < 900 ? 'mid' : 'slow';
  return <span className={`ping ping--${cls}`} title="Задержка запроса через сервер">{ms} мс</span>;
}

// Метка способа подключения на нижней панели
const modeLabel = (s: Server) => (s.kind === 'xray' ? s.group || 'VLESS' : 'VK TURN');

function ServerSelector({ servers, selected, listOpen, onToggleList, onSelect, onIconClick, onEdit, onRemove, pings, pinging, busySub, onRefreshSub, onDeleteSub }: {
  servers: Server[]; selected: Server | null; listOpen: boolean;
  onToggleList: () => void; onSelect: (s: Server) => void;
  onIconClick: (e: React.MouseEvent, s: Server) => void; onEdit: (s: Server) => void;
  onRemove: (s: Server) => void;
  pings: Record<string, number>; pinging: boolean; busySub: string | null;
  onRefreshSub: (url: string) => void; onDeleteSub: (g: ServerGroup) => void;
}) {
  return (
    <div className="status-bar">
      {listOpen && servers.length > 0 && (
        <div className="server-list">
          {groupServers(servers).map(g => (
            <section key={g.key} className={`server-section server-section--${g.kind}`}>
              <div className="ss-head">
                <span className="ss-icon">{g.kind === 'turn' ? <IconBrandVk size={16} stroke={2} /> : <IconListDetails size={16} stroke={2} />}</span>
                <span className="ss-text">
                  <span className="ss-title">{g.title}</span>
                  <span className="ss-sub">
                    {g.kind === 'turn' ? `Звонки ВК · ${serversCount(g.items.length)}` : `Подписка · ${serversCount(g.items.length)}${g.updated ? ` · обновлена ${fmtDate(g.updated)}` : ''}`}
                  </span>
                </span>
                {g.kind === 'sub' && g.url && (
                  <>
                    <button type="button" className={`ss-btn${busySub === g.url ? ' ss-btn--spin' : ''}`} onClick={() => onRefreshSub(g.url!)} disabled={busySub === g.url} title="Обновить подписку и пинг" aria-label="Обновить подписку">
                      <IconRefresh size={15} stroke={2} />
                    </button>
                    <button type="button" className="ss-btn ss-btn--danger" onClick={() => onDeleteSub(g)} title="Удалить подписку" aria-label="Удалить подписку">
                      <IconTrash size={15} stroke={2} />
                    </button>
                  </>
                )}
              </div>
              {g.items.map(s => (
                <div
                  key={s.id}
                  className={`server-item${s.id === selected?.id ? ' server-item--active' : ''}`}
                  style={{ cursor: 'pointer' }}
                  role="button"
                  tabIndex={0}
                  onClick={() => onSelect(s)}
                  onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') onSelect(s); }}
                >
                  <button type="button" className="server-icon-btn" onClick={(e) => { e.stopPropagation(); onIconClick(e, s); }} aria-label="Выбрать иконку">
                    <ServerIcon iconKey={s.icon} size={20} />
                  </button>
                  <span className="status-name">{s.name}</span>
                  {s.kind === 'xray' && <PingBadge ms={s.link ? pings[s.link] : undefined} pinging={pinging} />}
                  {s.kind === 'xray' ? (
                    <button type="button" className="server-edit-btn" onClick={(e) => { e.stopPropagation(); onRemove(s); }} aria-label="Удалить сервер">
                      <IconTrash size={15} stroke={2} />
                    </button>
                  ) : (
                    <button type="button" className="server-edit-btn" onClick={(e) => { e.stopPropagation(); onEdit(s); }} aria-label="Редактировать">
                      <IconPencil size={15} stroke={2} />
                    </button>
                  )}
                </div>
              ))}
            </section>
          ))}
        </div>
      )}

      <button type="button" className={`status-server${!selected ? ' status-server--empty' : ''}`} onClick={onToggleList}>
        <ServerIcon iconKey={selected?.icon} size={20} />
        <span className="status-name">{selected ? selected.name : 'Нет серверов'}</span>
        {selected && <span className={`mode-chip mode-chip--${selected.kind === 'xray' ? 'sub' : 'turn'}`}>{modeLabel(selected)}</span>}
        <IconChevronUp
          size={16}
          style={{ transform: listOpen ? 'rotate(0deg)' : 'rotate(180deg)', transition: 'transform 0.2s' }}
        />
      </button>
    </div>
  );
}

function IconPicker({ iconMenu, onPick, onClose }: {
  iconMenu: { server: Server; x: number; y: number };
  onPick: (key: string) => void; onClose: () => void;
}) {
  return (
    <>
      <button type="button" aria-label="Закрыть" className="icon-picker-backdrop" onClick={onClose} />
      <div
        className="icon-picker"
        style={{
          left: Math.min(iconMenu.x, window.innerWidth - 256),
          top: iconMenu.y - 4 - (Math.ceil(SERVER_ICONS.length / 6) * 40 + 20),
        }}
      >
        {SERVER_ICONS.map(ic => (
          <button
            type="button"
            key={ic.key}
            className={`icon-picker-btn${(iconMenu.server.icon ?? 'clover') === ic.key ? ' icon-picker-btn--active' : ''}`}
            onClick={() => onPick(ic.key)}
            title={ic.key}
          >
            {ic.render(18)}
          </button>
        ))}
      </div>
    </>
  );
}

export default function Connect() {
  const [servers, setServers] = useState<Server[]>(() => serverStore.getAll());
  const [selected, setSelected] = useState<Server | null>(() => {
    const all = serverStore.getAll();
    if (all.length === 0) return null;
    const lastId = serverStore.getLastSelectedId();
    return all.find(s => s.id === lastId) ?? all[0];
  });
  const [listOpen, setListOpen] = useState(false);

  useEffect(() => {
    ListProfiles().then(profiles => {
      if (!profiles) return;
      const existing = serverStore.getAll();
      let changed = false;
      for (const [name, p] of Object.entries(profiles)) {
        const host = p.peer || '';
        if (!host) continue;
        const stored = existing.find(s => s.name === name || s.host === host);
        if (stored) {
          if (p.workers && stored.power !== p.workers) {
            serverStore.update({ ...stored, power: p.workers });
            changed = true;
          }
          continue;
        }
        const h4: [string,string,string,string] = [p.hashes?.[0]??'', p.hashes?.[1]??'', p.hashes?.[2]??'', p.hashes?.[3]??''];
        serverStore.add({ name, host, password: p.password ?? '', hashes: h4, power: p.workers });
        changed = true;
      }
      if (changed) {
        setServers(serverStore.getAll());
        const all = serverStore.getAll();
        if (all.length > 0) setSelected(prev => prev ?? all[0]);
      }

    }).catch(() => {});
  }, []);

  // tunnelState из глобального store — переживает смену роута
  const [tunnelState, setTunnelState] = useState<TunnelState>(() => tunnelStore.get());
  useEffect(() => tunnelStore.subscribe(setTunnelState), []);
  const [stats, setStats] = useState<TunnelStats | null>(null);
  useEffect(() => statsStore.subscribe(setStats), []);
  const [stage, setStage] = useState('');
  useEffect(() => stageStore.subscribe(setStage), []);
  const [degraded, setDegraded] = useState(false);
  useEffect(() => degradedStore.subscribe(setDegraded), []);
  const shownState: TunnelState = tunnelState === 'connected' && degraded ? 'connecting' : tunnelState;


  const selectedRef = useRef(selected);
  const tunnelStateRef = useRef(tunnelState);

  useEffect(() => {
    selectedRef.current = selected;
    tunnelStateRef.current = tunnelState;
  });

  useEffect(() => {
    serverStore.setLastSelectedId(selected?.id ?? null);
  }, [selected?.id]);

  const [addServerOpen, setAddServerOpen] = useState(false);
  const [editServer, setEditServer] = useState<Server | null>(null);

  const [linkFlash, setLinkFlash] = useState(false);
  const linkFlashTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => { if (linkFlashTimerRef.current) clearTimeout(linkFlashTimerRef.current); };
  }, []);

  useEffect(() => {
    return wdttLinkStore.subscribe((link) => {
      if (!link) return;
      const consumed = wdttLinkStore.consume();
      if (!consumed) return;

      const applyLink = async () => {
        const h4 = consumed.hashes.slice(0, 4);
        const padded: [string,string,string,string] = [h4[0]??'', h4[1]??'', h4[2]??'', h4[3]??''];

        // Генерируем уникальное имя: "Сервер 1", "Сервер 2", ...
        const existingNames = serverStore.getAll().map(s => s.name);
        let autoName = consumed.name || 'Сервер';
        if (autoName === 'Server') autoName = 'Сервер';
        let counter = 1;
        while (existingNames.includes(`${autoName} ${counter}`)) counter++;
        const name = `${autoName} ${counter}`;

        await SaveProfile(name, {
          peer: consumed.host,
          password: consumed.password,
          hashes: h4 as unknown as string[],
          workers: consumed.workers,
          turn: '', port: consumed.port || '', device_id: '', listen: '', turn_tcp: false,
        });
        const s = serverStore.add({
          name,
          host: consumed.host,
          password: consumed.password,
          hashes: padded,
          power: consumed.workers,
        });
        setServers(serverStore.getAll());
        setSelected({ ...s });
        setLinkFlash(true);
        if (linkFlashTimerRef.current) clearTimeout(linkFlashTimerRef.current);
        linkFlashTimerRef.current = setTimeout(() => setLinkFlash(false), 800);
        toastStore.show(`Профиль добавлен: ${name}`, 3000);
      };
      applyLink().catch(e => {
        console.warn('applyLink failed:', e);
        toastStore.show('Не удалось добавить сервер по ссылке', 3000);
      });
    });
  }, []);

  const connectingRef = useRef(false);

  const doConnect = async () => {
    const cur = selectedRef.current;
    if (!cur) return;
    if (tunnelState !== 'idle') return;
    if (connectingRef.current) return; // guard от двойного клика
    connectingRef.current = true;
    if (cur.kind === 'xray') {
      // Российские сервисы сообщают IP VPN-серверов в РКН — без «Россия напрямую»
      // они увидят адрес твоего сервера
      GetRussiaDirect().then(r => {
        if (r && !r.enabled) toastStore.show('«Россия напрямую» выключена — российские сайты увидят IP сервера подписки. Включите в «Исключениях».', 8000);
      }).catch(() => {});
      tunnelStore.set('connecting');
      logStore.clear();
      try {
        await ConnectXray(cur.link ?? '', cur.name);
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        logStore.push('ERROR', msg);
        toastStore.show(msg, 6000);
        tunnelStore.set('idle');
      } finally {
        connectingRef.current = false;
      }
      return;
    }
    const hashes = (cur.hashes ?? []).filter(h => h.trim());
    if (hashes.length === 0) {
      toastStore.show('Добавьте хеши в профиле сервера');
      connectingRef.current = false;
      return;
    }
    tunnelStore.set('connecting');
    logStore.clear();
    try {
      const workers = cur.power || Math.max(9, hashes.length * 9);
      await WailsConnect({
        peerAddr: cur.host,
        password: cur.password,
        hashes,
        deviceId: cur.deviceId,
        workers,
        captchaMode: 'auto',
        obfsMode: settingsStore.get().obfsMode || 'audio',
        turnTcp: settingsStore.get().turnTcp || false,
      });
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      logStore.push('ERROR', msg);
      toastStore.show(msg, 6000);
      tunnelStore.set('idle');
    } finally {
      connectingRef.current = false;
    }
  };

  const reconnectAtRef = useRef(0);

  const handleTunnel = async () => {
    if (!selectedRef.current) return;
    if (tunnelState === 'idle') {
      if (Date.now() < reconnectAtRef.current) {
        const secs = Math.ceil((reconnectAtRef.current - Date.now()) / 1000);
        toastStore.show(`Подождите ${secs} сек.`, 2000);
        return;
      }
      toastStore.show('Убедитесь что другие VPN отключены', 4000);
      await doConnect();
    } else if (tunnelState === 'connected' || tunnelState === 'connecting') {
      tunnelStore.set('disconnecting');
      try {
        await WailsDisconnect();
      } catch {
        // игнорируем ошибку disconnect
      } finally {
        tunnelStore.set('idle');
        reconnectAtRef.current = Date.now() + 4000;
      }
    }
  };

  const handleAdd = (data: Omit<Server, 'id'>) => {
    const s = serverStore.add(data);
    setServers(serverStore.getAll());
    setSelected(s);
  };

  // Подписку добавляем целиком; повторная загрузка той же ссылки заменяет её серверы
  // (иконки и выбранный сервер сохраняются по имени)
  const applySubscription = (sub: backend.Subscription, silent = false) => {
    const all = serverStore.getAll();
    const old = all.filter(s => s.subUrl === sub.url);
    const firstIdx = all.findIndex(s => s.subUrl === sub.url);
    const others = all.filter(s => s.subUrl !== sub.url);
    const now = Date.now();
    const fresh: Server[] = sub.servers.map(x => {
      const prev = old.find(o => o.name === x.name);
      return {
        id: prev?.id ?? crypto.randomUUID(), name: x.name, host: x.host, password: '', icon: prev?.icon ?? 'globe',
        kind: 'xray', link: x.link, transport: x.transport, group: sub.title, subUrl: sub.url, subUpdated: now,
      };
    });
    const at = firstIdx < 0 ? others.length : Math.min(firstIdx, others.length);
    serverStore.save([...others.slice(0, at), ...fresh, ...others.slice(at)]);
    setServers(serverStore.getAll());
    const cur = selectedRef.current;
    if (!silent) {
      setSelected(fresh.find(a => a.id === cur?.id) ?? fresh[0] ?? cur);
      toastStore.show(`«${sub.title}»: ${serversCount(fresh.length)}${sub.skipped ? `, пропущено ${sub.skipped} (протокол пока не поддерживается)` : ''}`, 4000);
    } else if (cur?.subUrl === sub.url) {
      setSelected(fresh.find(a => a.id === cur.id) ?? cur);
    }
  };
  const handleAddSubscription = (sub: backend.Subscription) => applySubscription(sub);

  const [pings, setPings] = useState<Record<string, number>>({});
  const [pinging, setPinging] = useState(false);
  const pingedAtRef = useRef(0);
  const pingAll = async () => {
    const links = serverStore.getAll().filter(s => s.kind === 'xray' && s.link).map(s => s.link!);
    if (links.length === 0 || pinging) return;
    setPinging(true);
    setPings({});
    try {
      setPings((await PingServers(links)) ?? {});
      pingedAtRef.current = Date.now();
    } catch { /* без пинга список всё равно работает */ } finally {
      setPinging(false);
    }
  };

  const [busySub, setBusySub] = useState<string | null>(null);
  const refreshSub = async (url: string, silent = false) => {
    setBusySub(url);
    try {
      applySubscription(await FetchSubscription(url), silent);
    } catch (e) {
      if (!silent) toastStore.show(`Подписка не обновилась: ${String(e)}`, 5000);
    } finally {
      setBusySub(null);
    }
  };

  // Подписки обновляются сами раз в сутки — при открытии приложения и списка
  const SUB_TTL = 24 * 60 * 60 * 1000;
  const refreshStaleSubs = async () => {
    const stale = new Set(serverStore.getAll().filter(s => s.subUrl && Date.now() - (s.subUpdated ?? 0) > SUB_TTL).map(s => s.subUrl!));
    for (const url of stale) await refreshSub(url, true);
  };
  useEffect(() => { refreshStaleSubs(); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Пинг — при открытии списка, если прошлый старше двух минут
  useEffect(() => {
    if (!listOpen) return;
    refreshStaleSubs();
    if (Date.now() - pingedAtRef.current > 2 * 60 * 1000) pingAll();
  }, [listOpen]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleRefreshSub = async (url: string) => {
    await refreshSub(url);
    pingAll();
  };

  // Подписку удаляем целиком, с «Вернуть»
  const handleDeleteSub = (g: ServerGroup) => {
    if (tunnelState !== 'idle' && selected?.subUrl === g.url) {
      toastStore.show('Сначала отключитесь от сервера этой подписки', 4000);
      return;
    }
    const before = serverStore.getAll();
    serverStore.save(before.filter(s => s.subUrl !== g.url));
    const all = serverStore.getAll();
    setServers(all);
    if (selected?.subUrl === g.url) setSelected(all[0] ?? null);
    toastStore.show(`Подписка «${g.title}» удалена`, 6000, {
      label: 'Вернуть',
      run: () => {
        if (serverStore.getAll().some(s => s.subUrl === g.url)) return;
        serverStore.save(before);
        setServers(serverStore.getAll());
      },
    });
  };

  // Сервер подписки удаляем сразу, с «Вернуть»
  const handleRemoveXray = (server: Server) => {
    const before = serverStore.getAll();
    const idx = before.findIndex(s => s.id === server.id);
    handleDelete(server.id);
    toastStore.show(`Удалён: ${server.name}`, 5000, {
      label: 'Вернуть',
      run: () => {
        const cur = serverStore.getAll();
        if (cur.some(s => s.id === server.id)) return;
        cur.splice(Math.min(idx, cur.length), 0, server);
        serverStore.save(cur);
        setServers(serverStore.getAll());
      },
    });
  };

  const handleSave = (server: Server) => {
    serverStore.update(server);
    const all = serverStore.getAll();
    setServers(all);
    if (selected?.id === server.id) setSelected(server);
  };

  const handleDelete = (id: string) => {
    serverStore.remove(id);
    const all = serverStore.getAll();
    setServers(all);
    if (selected?.id === id) setSelected(all[0] ?? null);
  };

  const [iconMenu, setIconMenu] = useState<{ server: Server; x: number; y: number } | null>(null);

  const handleIconClick = (e: React.MouseEvent, server: Server) => {
    e.stopPropagation();
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setIconMenu({ server, x: rect.left, y: rect.top });
  };

  const handlePickIcon = (key: string) => {
    if (!iconMenu) return;
    const updated = { ...iconMenu.server, icon: key };
    serverStore.update(updated);
    const all = serverStore.getAll();
    setServers(all);
    if (selected?.id === iconMenu.server.id) setSelected(updated);
    setIconMenu(null);
  };

  return (
    <>
      <main className="main">
        <button type="button" className="btn-add" onClick={() => setAddServerOpen(true)} aria-label="Добавить сервер">
          <IconPlus stroke={2} size={22} />
        </button>

        <div className="hero">
          <PowerButton
            tunnelState={shownState}
            linkFlash={linkFlash}
            selected={selected}
            onTunnel={handleTunnel}
          />
          <div className="hero-title">{!selected ? 'Нет серверов' : degraded && tunnelState === 'connected' ? 'Восстанавливаю связь…' : STATUS_TITLE[tunnelState]}</div>
          <div className="hero-sub">
            {!selected ? 'Добавьте сервер кнопкой «+»'
              : tunnelState === 'idle' ? 'Нажмите на пузырь, чтобы подключиться'
              : degraded && tunnelState === 'connected' && selected.kind !== 'xray' ? <span className="hero-stage">Нет связи с серверами ВК — интернет пока напрямую</span>
              : tunnelState === 'connecting' && stage ? <span key={stage} className="hero-stage">{stage}</span>
              : selected.name}
          </div>
          <div className={`hero-stats${tunnelState === 'connected' ? ' hero-stats--on' : ''}`}>
            <StatsCard stats={stats} active={tunnelState === 'connected'} transport={selected?.kind === 'xray' ? selected.transport : undefined} />
          </div>
        </div>

        <ServerSelector
          servers={servers}
          selected={selected}
          listOpen={listOpen}
          onToggleList={() => setListOpen(o => !o)}
          onSelect={(s) => { setSelected({ ...s }); setListOpen(false); }}
          onIconClick={handleIconClick}
          onEdit={(s) => setEditServer(s)}
          onRemove={handleRemoveXray}
          pings={pings}
          pinging={pinging}
          busySub={busySub}
          onRefreshSub={handleRefreshSub}
          onDeleteSub={handleDeleteSub}
        />

        {addServerOpen && <AddServer onClose={() => setAddServerOpen(false)} onAdd={handleAdd} onAddSubscription={handleAddSubscription} />}
        {editServer && (
          <EditServer
            server={editServer}
            onClose={() => setEditServer(null)}
            onSave={handleSave}
            onDelete={handleDelete}
          />
        )}

        {iconMenu && (
          <IconPicker
            iconMenu={iconMenu}
            onPick={handlePickIcon}
            onClose={() => setIconMenu(null)}
          />
        )}
      </main>

    </>
  );
}
