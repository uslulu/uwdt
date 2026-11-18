import { useState } from 'react';
import type React from 'react';
import { IconCircleHalf2, IconHash, IconEye, IconEyeOff, IconX } from '@tabler/icons-react';
import type { Server } from '../lib/types';
import Hash from './Hash';
import { EditProfile, DeleteProfile } from '../../wailsjs/go/backend/App';
import { toastStore } from '../lib/stores/toastStore';
import '../components/ui.css';
import './server-form.css';

interface Props {
  server: Server;
  onClose: () => void;
  onSave: (server: Server) => void;
  onDelete: (id: string) => void;
}

export default function EditServer({ server, onClose, onSave, onDelete }: Props) {
  const [name, setName] = useState(server.name);
  const lastColon = server.host.lastIndexOf(':');
  const ip = lastColon !== -1 ? server.host.slice(0, lastColon) : server.host;
  const port0 = lastColon !== -1 ? server.host.slice(lastColon + 1) : '56000';
  const [serverIp, setServerIp] = useState(ip);
  const [serverPort, setServerPort] = useState(port0);
  const [password, setPassword] = useState(server.password);
  const [showPassword, setShowPassword] = useState(false);
  const [hashes, setHashes] = useState<[string,string,string,string]>(
    server.hashes ?? ['', '', '', '']
  );
  const [hashOpen, setHashOpen] = useState(false);

  const filledHashes = hashes.filter(h => h.trim()).length;
  const powerMax = Math.max(9, filledHashes * 27);
  const [power, setPower] = useState<number>(server.power ?? Math.max(9, filledHashes * 9));

  const handleHashSave = (h: [string, string, string, string]) => {
    setHashes(h);
    const filled = h.filter(x => x.trim()).length;
    const newMax = Math.max(9, filled * 27);
    setPower(p => Math.min(p, newMax) || Math.max(9, filled * 9));
  };

  const [saving, setSaving] = useState(false);

  const handleSave = async () => {
    if (!name.trim() || !serverIp.trim() || saving) return;
    setSaving(true);
    const updated: Server = {
      ...server,
      name: name.trim(),
      host: `${serverIp.trim()}:${serverPort.trim() || '56000'}`,
      password,
      hashes,
      power,
    };
    try {
      await EditProfile(server.name, updated.name, {
        peer: updated.host,
        password: updated.password,
        hashes,
        workers: power,
        turn: '', port: '', device_id: '', listen: '',
        turn_tcp: false,
      });
      await onSave(updated);
    } catch (e) {
      toastStore.show(String(e), 3000);
      setSaving(false);
      return;
    }

    setSaving(false);
    onClose();
  };

  const handleDelete = async () => {
    try { await DeleteProfile(server.name); await onDelete(server.id); } catch (e) { toastStore.show(String(e), 3000); return; }
    onClose();
  };

  return (
    <>
      <div className="ui-overlay">
        <div className="ui-modal" onClick={e => e.stopPropagation()}>
          <div className="ui-modal-head">
            <IconCircleHalf2 size={20} />
            <span className="ui-modal-title">Редактирование сервера</span>
            <button type="button" className="ui-icon-btn" onClick={onClose} aria-label="Закрыть"><IconX size={18} /></button>
          </div>

          <div className="sf-form">
          <input className="ui-input" placeholder="Название сервера" value={name} onChange={e => setName(e.target.value)} />
          <div className="sf-row">
            <input className="ui-input" placeholder="IP сервера" value={serverIp} onChange={e => setServerIp(e.target.value)} />
            <input className="ui-input sf-port" placeholder="Порт" value={serverPort} onChange={e => setServerPort(e.target.value)} />
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

          <button
            type="button"
            className="sf-hash-btn"
            onClick={() => setHashOpen(true)}
          >
            <IconHash stroke={2} size={16} />
            Хеши ({filledHashes}/4)
          </button>

          <div className="sf-actions">
            <button type="button" className="ui-btn ui-btn--primary" onClick={handleSave} disabled={!name.trim() || !serverIp.trim() || saving}>
              {saving ? 'Сохранение…' : 'Сохранить'}
            </button>
            <button type="button" className="ui-btn ui-btn--danger" onClick={handleDelete}>Удалить</button>
          </div>
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
