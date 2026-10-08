import { useState, useEffect, useRef } from 'react';
import { IconArrowsSplit2, IconX, IconPlus, IconTrash } from '@tabler/icons-react';
import type { ExcludeEntry } from '../lib/types';
import { GetExcludes, SetExcludes, CheckExclude, GetRussiaDirect, SetRussiaDirect } from '../../wailsjs/go/backend/App';
import { toastStore } from '../lib/stores/toastStore';
import { PRESET_GROUPS, PRESET_LABEL, type Preset } from '../lib/presets';
import './Settings.css';
import './Excludes.css';

interface Props {
  onClose: () => void;
}

const kindLabel = (v: string) =>
  /^AS\d+$/.test(v) ? 'компания' : /^[\d./]+$/.test(v) ? (v.endsWith('/32') ? 'адрес' : 'сеть') : 'домен';


export default function Excludes({ onClose }: Props) {
  const [items, setItems] = useState<ExcludeEntry[]>([]);
  const [input, setInput] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [ru, setRu] = useState<{ enabled: boolean; count: number; updated: string } | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const checkTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    GetExcludes().then(list => setItems(list ?? [])).catch(() => toastStore.show('Не удалось загрузить исключения', 3000));
    GetRussiaDirect().then(setRu).catch(() => {});
    inputRef.current?.focus();
    return () => { if (checkTimer.current) clearTimeout(checkTimer.current); };
  }, []);

  // Сохраняем сразу: бэкенд нормализует список и применяет его к поднятому туннелю
  const persist = async (next: ExcludeEntry[]) => {
    setSaving(true);
    try {
      const clean = await SetExcludes(next);
      setItems(clean ?? []);
      return true;
    } catch (e) {
      toastStore.show(String(e), 3000);
      return false;
    } finally {
      setSaving(false);
    }
  };

  const onInput = (v: string) => {
    setInput(v);
    setError('');
    if (checkTimer.current) clearTimeout(checkTimer.current);
    if (!v.trim()) return;
    checkTimer.current = setTimeout(() => {
      CheckExclude(v).catch(e => setError(String(e)));
    }, 400);
  };

  const add = async () => {
    const raw = input.trim();
    if (!raw) return;
    let value: string;
    try {
      value = await CheckExclude(raw);
    } catch (e) {
      setError(String(e));
      return;
    }
    if (items.some(i => i.value === value)) {
      setError('Уже есть в списке');
      return;
    }
    if (await persist([...items, { value, enabled: true }])) {
      setInput('');
      inputRef.current?.focus();
    }
  };

  const toggle = (idx: number) =>
    persist(items.map((it, i) => (i === idx ? { ...it, enabled: !it.enabled } : it)));

  const remove = (idx: number) => persist(items.filter((_, i) => i !== idx));

  const presetOn = (p: Preset) => p.values.every(v => items.some(i => i.value === v && i.enabled));

  // Набор включён — убираем его записи; выключен — добавляем недостающие и включаем выключенные
  const togglePreset = (p: Preset) => {
    if (presetOn(p)) {
      persist(items.filter(i => !p.values.includes(i.value)));
      return;
    }
    const next = items.map(i => (p.values.includes(i.value) ? { ...i, enabled: true } : i));
    p.values.forEach(v => { if (!next.some(i => i.value === v)) next.push({ value: v, enabled: true }); });
    persist(next);
  };

  const toggleRussia = async () => {
    if (!ru) return;
    setSaving(true);
    try {
      setRu(await SetRussiaDirect(!ru.enabled));
    } catch (e) {
      toastStore.show(String(e), 3000);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="st-overlay" onClick={onClose}>
      <div className="st-modal ex-modal" onClick={e => e.stopPropagation()}>
        <div className="st-header">
          <IconArrowsSplit2 stroke={2} size={20} />
          <span className="st-title">Исключения из туннеля</span>
          <button type="button" className="st-close" onClick={onClose} aria-label="Закрыть"><IconX size={18} /></button>
        </div>

        <div className="ex-hint">
          Эти адреса идут напрямую, мимо туннеля. Подсеть, адрес, домен или номер сети компании (AS) — изменения применяются сразу.
        </div>

        <div className={`ex-russia${ru?.enabled ? ' ex-russia--on' : ''}`}>
          <div className="ex-russia-text">
            <span className="ex-russia-title">Россия напрямую</span>
            <span className="ex-russia-sub">
              {ru ? `Все российские сети мимо туннеля · ${ru.count.toLocaleString('ru-RU')} сетей${ru.updated ? `, список от ${ru.updated}` : ''}` : 'Загрузка…'}
            </span>
          </div>
          <button
            type="button"
            className={`st-toggle st-toggle--${ru?.enabled ? 'on' : 'off'}`}
            aria-label={ru?.enabled ? 'Выключить «Россия напрямую»' : 'Включить «Россия напрямую»'}
            onClick={toggleRussia}
            disabled={!ru || saving}
          />
        </div>

        <form className="ex-add" onSubmit={e => { e.preventDefault(); add(); }}>
          <input
            ref={inputRef}
            className={`ex-input${error ? ' ex-input--error' : ''}`}
            value={input}
            onChange={e => onInput(e.target.value)}
            placeholder="10.0.0.0/8, example.ru или AS32590"
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
          />
          <button type="submit" className="ex-add-btn" disabled={!input.trim() || !!error || saving} aria-label="Добавить">
            <IconPlus size={18} />
          </button>
        </form>
        <div className="ex-error">{error}</div>

        <div className="ex-presets">
          {PRESET_GROUPS.map(g => (
            <div key={g.title} className="ex-preset-group">
              <span className="ex-preset-title">{g.title}</span>
              <div className="ex-preset-row">
                {g.presets.map(p => {
                  const on = presetOn(p);
                  return (
                    <button
                      key={p.label}
                      type="button"
                      className={`ex-preset${on ? ' ex-preset--on' : ''}`}
                      disabled={saving}
                      onClick={() => togglePreset(p)}
                      title={on ? 'Убрать из исключений' : `Пустить мимо туннеля: ${p.values.join(', ')}`}
                    >
                      {p.label}
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
        </div>

        <div className="ex-list">
          {items.length === 0 && <div className="ex-empty">Список пуст — весь трафик идёт через туннель</div>}
          {items.map((it, idx) => (
            <div key={it.value} className={`ex-item${it.enabled ? '' : ' ex-item--off'}`}>
              <span className="ex-kind">{PRESET_LABEL[it.value] ? 'сервис' : kindLabel(it.value)}</span>
              <span className="ex-value" title={it.value}>
                {PRESET_LABEL[it.value]
                  ? <><span className="ex-value-label">{PRESET_LABEL[it.value]}</span> <small className="ex-value-sub">{it.value}</small></>
                  : it.value.endsWith('/32') ? it.value.slice(0, -3) : it.value}
              </span>
              <button
                type="button"
                className={`st-toggle ex-toggle st-toggle--${it.enabled ? 'on' : 'off'}`}
                aria-label={it.enabled ? 'Выключить исключение' : 'Включить исключение'}
                onClick={() => toggle(idx)}
                disabled={saving}
              />
              <button type="button" className="ex-del" onClick={() => remove(idx)} aria-label="Удалить" disabled={saving}>
                <IconTrash size={15} />
              </button>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
