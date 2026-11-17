import { useState } from 'react';
import { IconCircleHalf2, IconEye, IconEyeOff, IconX, IconHash } from '@tabler/icons-react';
import type { Server } from '../lib/types';
import { SaveProfile, FetchSubscription } from '../../wailsjs/go/backend/App';
import type { backend } from '../../wailsjs/go/models';
import { parseWdttUrl } from '../lib/utils/wdttLink';
import { toastStore } from '../lib/stores/toastStore';
import Hash from './Hash';
import '../components/ui.css';
import './server-form.css';

interface Props {
  onClose: () => void;
  onAdd: (server: Omit<Server, 'id'>) => void;
  onAddSubscription: (sub: backend.Subscription) => void;
}

// Имя сервера из ссылки vless://…#Имя
const vlessName = (link: string) => {
  const i = link.indexOf('#');
  if (i < 0) return '';
  try { return decodeURIComponent(link.slice(i + 1)).trim(); } catch { return link.slice(i + 1).trim(); }
};

export default function AddServer({ onClose, onAdd, onAddSubscription }: Props) {
  const [link, setLink] = useState('');
  const [name, setName] = useState('');
  const [ip, setIp] = useState('');
  const [port, setPort] = useState('56000');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [hashes, setHashes] = useState<[string,string,string,string]>(['', '', '', '']);
  const [hashOpen, setHashOpen] = useState(false);

  const filledHashes = hashes.filter(h => h.trim()).length;
  const powerMax = Math.max(9, filledHashes * 27);
  const [power, setPower] = useState(9);

  const applyLink = (raw: string) => {
    setLink(raw);
    const parsed = parseWdttUrl(raw.trim());
    if (!parsed) return;
    // host может быть "IP:PORT" — разделяем
    const lastColon = parsed.host.lastIndexOf(':');
    if (lastColon > 0 && lastColon < parsed.host.length - 1) {
      setIp(parsed.host.slice(0, lastColon));
      setPort(parsed.host.slice(lastColon + 1));
    } else {
      setIp(parsed.host);
    }
    setPassword(parsed.password);
    if (parsed.name !== 'Server') setName(parsed.name);
    if (parsed.hashes.length > 0) {
      const h4: [string,string,string,string] = [parsed.hashes[0]??'', parsed.hashes[1]??'', parsed.hashes[2]??'', parsed.hashes[3]??''];
      setHashes(h4);
      const filled = h4.filter(x => x.trim()).length;
      setPower(parsed.workers ?? Math.max(9, filled * 9));
    }
  };

  const handleHashSave = (h: [string, string, string, string]) => {
    setHashes(h);
    const filled = h.filter(x => x.trim()).length;
    const newMax = Math.max(9, filled * 27);
    setPower(p => Math.min(p, newMax) || Math.max(9, filled * 9));
  };

  const [saving, setSaving] = useState(false);

  // Вставили подписку https://… или одиночный сервер vless://… — ручные поля не нужны
  const raw = link.trim();
  const isSub = /^https?:\/\//i.test(raw);
  const isVless = raw.toLowerCase().startsWith('vless://');

  const handleSubscription = async () => {
    if (saving) return;
    setSaving(true);
    try {
      const sub = await FetchSubscription(raw);
      onAddSubscription(sub);
      onClose();
    } catch (e) {
      toastStore.show(String(e), 5000);
      setSaving(false);
    }
  };

  const handleVless = () => {
    const n = name.trim() || vlessName(raw) || 'Сервер VLESS';
    const m = raw.match(/[?&]type=([^&#]+)/);
    onAdd({ name: n, host: '', password: '', kind: 'xray', link: raw, transport: m ? m[1] : 'tcp', icon: 'globe' });
    onClose();
  };

  const handleAdd = async () => {
    if (!name.trim() || !ip.trim() || saving) return;
    setSaving(true);
    const host = `${ip.trim()}:${port.trim() || '56000'}`;

    try {
      await SaveProfile(name.trim(), {
        peer: host,
        password,
        hashes: hashes.filter(h => h.trim()),
        workers: power,
        turn: '',
        port: '',
        device_id: '',
        listen: '127.0.0.1:9000',
        turn_tcp: false,
      });
    } catch (e) {
      console.warn('SaveProfile failed:', e);
      toastStore.show('Не удалось сохранить профиль на диск', 3000);
      setSaving(false);
      return; // профиль в бэкенде — источник истины; без него сервер "потеряет" хеши
    }

    onAdd({ name: name.trim(), host, password, hashes, power });
    setSaving(false);
    onClose();
  };

  return (
    <>
      <div className="ui-overlay">
        <div className="ui-modal" onClick={e => e.stopPropagation()}>
          <div className="ui-modal-head">
            <IconCircleHalf2 stroke={2} size={20} />
            <span className="ui-modal-title">Добавление сервера</span>
            <button type="button" className="ui-icon-btn" onClick={onClose} aria-label="Закрыть"><IconX size={18} /></button>
          </div>

          <div className="sf-form">
          <input
            className="ui-input"
            placeholder="Ссылка wdtt://, vless:// или подписка https://…"
            value={link}
            onChange={e => applyLink(e.target.value)}
          />

          {isSub && (
            <>
              <div className="sf-note">Подписка: UWDT скачает список серверов (VLESS) и добавит их отдельной группой. Подключение к ним — через движок Xray.</div>
              <button type="button" className="ui-btn ui-btn--primary ui-btn--block" onClick={handleSubscription} disabled={saving}>
                {saving ? 'Загрузка подписки…' : 'Загрузить подписку'}
              </button>
            </>
          )}

          {isVless && (
            <>
              <input className="ui-input" placeholder={vlessName(raw) || 'Название сервера'} value={name} onChange={e => setName(e.target.value)} />
              <button type="button" className="ui-btn ui-btn--primary ui-btn--block" onClick={handleVless}>Добавить сервер</button>
            </>
          )}

          {!isSub && !isVless && (<>
          <div className="sf-divider">или вручную</div>

          <input className="ui-input" placeholder="Название сервера" value={name} onChange={e => setName(e.target.value)} />
          <div className="sf-row">
            <input className="ui-input" placeholder="IP сервера" value={ip} onChange={e => setIp(e.target.value)} />
            <input className="ui-input sf-port" placeholder="Порт" value={port} onChange={e => setPort(e.target.value)} />
          </div>
          <div className="sf-pw-wrap">
            <input className="ui-input" placeholder="Пароль туннеля" type={showPassword ? 'text' : 'password'} value={password} onChange={e => setPassword(e.target.value)} />
            <button type="button" className="sf-pw-toggle" onClick={() => setShowPassword(v => !v)} aria-label={showPassword ? 'Скрыть пароль' : 'Показать пароль'}>
              {showPassword ? <IconEyeOff size={18} /> : <IconEye size={18} />}
            </button>
          </div>

          <div className="sf-slider-wrap">
            <div className="sf-slider-label">
              <span>Мощность (всего потоков)</span>
              <span>{filledHashes === 0 ? 'нет хешей' : power}</span>
            </div>
            <input
              type="range" min={9} max={powerMax} step={9}
              value={Math.min(power, powerMax)}
              className="sf-slider"
              disabled={filledHashes === 0}
              style={{ '--v': filledHashes > 0 ? Math.round((Math.min(power, powerMax) - 9) / Math.max(powerMax - 9, 1) * 100) : 0 } as React.CSSProperties}
              onChange={e => setPower(+e.target.value)}
              aria-label="Мощность — всего потоков, шаг 9"
            />
          </div>

          <button type="button" className="sf-hash-btn" onClick={() => setHashOpen(true)}>
            <IconHash stroke={2} size={16} />
            Хеши ({filledHashes}/4)
          </button>

          <button type="button" className="ui-btn ui-btn--primary ui-btn--block" onClick={handleAdd} disabled={!name.trim() || !ip.trim() || saving}>
            {saving ? 'Сохранение…' : 'Добавить сервер'}
          </button>
          </>)}
          </div>
        </div>
      </div>
      {hashOpen && (
        <Hash
          hashes={hashes}
          onClose={() => setHashOpen(false)}
          onSave={handleHashSave}
        />
      )}
    </>
  );
}
