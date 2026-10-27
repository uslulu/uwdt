import { useState, useEffect, useRef, useMemo } from 'react';
import {
  IconArrowsSplit2, IconX, IconPlus, IconTrash, IconWorld, IconBuilding, IconNetwork, IconMapPin, IconSearch, IconCheck,
} from '@tabler/icons-react';
import type { ExcludeEntry } from '../lib/types';
import { GetExcludes, SetExcludes, CheckExclude, GetRussiaDirect, SetRussiaDirect, ASNames } from '../../wailsjs/go/backend/App';
import { toastStore } from '../lib/stores/toastStore';
import { PRESET_GROUPS, PRESET_LABEL, type Preset } from '../lib/presets';
import './Settings.css';
import './Excludes.css';

interface Props {
  onClose: () => void;
}

type Kind = 'as' | 'net' | 'addr' | 'domain';

const kindOf = (v: string): Kind =>
  /^AS\d+$/.test(v) ? 'as' : /^[\d./]+$/.test(v) ? (v.endsWith('/32') ? 'addr' : 'net') : 'domain';

const KIND: Record<Kind, { label: string; Icon: typeof IconWorld }> = {
  as: { label: 'Сеть компании (AS)', Icon: IconBuilding },
  net: { label: 'Подсеть', Icon: IconNetwork },
  addr: { label: 'Адрес', Icon: IconMapPin },
  domain: { label: 'Домен', Icon: IconWorld },
};

// Поиск появляется, когда своих исключений становится много
const SEARCH_FROM = 9;
const UNDO_MS = 5000;

export default function Excludes({ onClose }: Props) {
  const [items, setItems] = useState<ExcludeEntry[]>([]);
  const [input, setInput] = useState('');
  const [error, setError] = useState('');
  const [query, setQuery] = useState('');
  const [saving, setSaving] = useState(false);
  const [ru, setRu] = useState<{ enabled: boolean; count: number; updated: string } | null>(null);
  const [asNames, setAsNames] = useState<Record<string, string>>({});
  const inputRef = useRef<HTMLInputElement>(null);
  const checkTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Актуальный список для «Вернуть» в тосте: тост живёт дольше этого рендера
  const itemsRef = useRef<ExcludeEntry[]>([]);
  useEffect(() => { itemsRef.current = items; }, [items]);

  useEffect(() => {
    GetExcludes().then(list => setItems(list ?? [])).catch(() => toastStore.show('Не удалось загрузить исключения', 3000));
    GetRussiaDirect().then(setRu).catch(() => {});
    inputRef.current?.focus();
    return () => { if (checkTimer.current) clearTimeout(checkTimer.current); };
  }, []);

  // Записи готовых наборов показываются кнопками наборов, в списке — только свои
  const custom = useMemo(() => items.filter(i => !PRESET_LABEL[i.value]), [items]);

  // Названия компаний для номеров AS — из кэша или RIPEstat
  useEffect(() => {
    const missing = custom.map(i => i.value).filter(v => kindOf(v) === 'as' && !(v in asNames));
    if (missing.length === 0) return;
    ASNames(missing).then(res => setAsNames(prev => {
      const next = { ...prev };
      missing.forEach(a => { next[a] = res?.[a] ?? ''; }); // '' — не спрашиваем снова
      return next;
    })).catch(() => {});
  }, [custom, asNames]);

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return custom;
    return custom.filter(i => i.value.toLowerCase().includes(q) || (asNames[i.value] ?? '').toLowerCase().includes(q));
  }, [custom, query, asNames]);

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
      setError(PRESET_LABEL[value] ? `Уже есть в наборе «${PRESET_LABEL[value]}»` : 'Уже есть в списке');
      return;
    }
    if (await persist([...items, { value, enabled: true }])) {
      setInput('');
      setQuery('');
      inputRef.current?.focus();
    }
  };

  const toggle = (value: string) =>
    persist(items.map(it => (it.value === value ? { ...it, enabled: !it.enabled } : it)));

  // Удаление сразу, но с «Вернуть» в тосте: запись встаёт на прежнее место
  const remove = async (value: string) => {
    const idx = items.findIndex(i => i.value === value);
    if (idx < 0) return;
    const removed = items[idx];
    if (!(await persist(items.filter(i => i.value !== value)))) return;
    toastStore.show(`Удалено: ${displayValue(value)}`, UNDO_MS, {
      label: 'Вернуть',
      run: () => {
        const cur = itemsRef.current;
        if (cur.some(i => i.value === value)) return;
        const next = [...cur];
        next.splice(Math.min(idx, next.length), 0, removed);
        persist(next);
      },
    });
  };

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

  const presetsOn = PRESET_GROUPS.flatMap(g => g.presets).filter(presetOn).length;

  return (
    <div className="st-overlay" onClick={onClose}>
      <div className="st-modal ex-modal" onClick={e => e.stopPropagation()}>
        <div className="st-header">
          <IconArrowsSplit2 stroke={2} size={20} />
          <span className="st-title">Исключения из туннеля</span>
          <button type="button" className="st-close" onClick={onClose} aria-label="Закрыть"><IconX size={18} /></button>
        </div>

        <div className="ex-hint">
          Эти адреса идут напрямую, мимо туннеля. Изменения применяются сразу, переподключаться не нужно.
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

        <div className="ex-section">
          <span className="ex-section-title">Готовые наборы</span>
          {presetsOn > 0 && <span className="ex-section-count">включено {presetsOn}</span>}
        </div>
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
                      aria-pressed={on}
                      onClick={() => togglePreset(p)}
                      title={`${on ? 'Убрать из исключений' : 'Пустить мимо туннеля'}: ${p.values.join(', ')}`}
                    >
                      {on && <IconCheck size={13} stroke={2.5} />}
                      {p.label}
                      {p.values.length > 1 && <span className="ex-preset-count">{p.values.length}</span>}
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
        </div>

        <div className="ex-section">
          <span className="ex-section-title">Свои исключения</span>
          {custom.length > 0 && <span className="ex-section-count">{custom.length}</span>}
        </div>
        <form className="ex-add" onSubmit={e => { e.preventDefault(); add(); }}>
          <input
            ref={inputRef}
            className={`ex-input${error ? ' ex-input--error' : ''}`}
            value={input}
            onChange={e => onInput(e.target.value)}
            placeholder="example.ru, 10.0.0.0/8 или AS32590"
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
          />
          <button type="submit" className="ex-add-btn" disabled={!input.trim() || !!error || saving} aria-label="Добавить">
            <IconPlus size={18} />
          </button>
        </form>
        <div className="ex-error">{error}</div>

        {custom.length >= SEARCH_FROM && (
          <label className="ex-search">
            <IconSearch size={14} />
            <input
              value={query}
              onChange={e => setQuery(e.target.value)}
              placeholder="Поиск по списку"
              spellCheck={false}
              autoCapitalize="off"
              autoCorrect="off"
            />
            {query && (
              <button type="button" className="ex-search-clear" onClick={() => setQuery('')} aria-label="Очистить поиск">
                <IconX size={13} />
              </button>
            )}
          </label>
        )}

        <div className="ex-list">
          {custom.length === 0 && (
            <div className="ex-empty">Своих исключений нет. Домен, адрес, подсеть или номер сети компании (AS) — сюда.</div>
          )}
          {custom.length > 0 && shown.length === 0 && <div className="ex-empty">Ничего не найдено</div>}
          {shown.map(it => {
            const kind = kindOf(it.value);
            const { label, Icon } = KIND[kind];
            const sub = kind === 'as' ? asNames[it.value] : undefined;
            return (
              <div key={it.value} className={`ex-item${it.enabled ? '' : ' ex-item--off'}`}>
                <span className={`ex-icon ex-icon--${kind}`} title={label}><Icon size={16} stroke={1.8} /></span>
                <span className="ex-value" title={sub ? `${it.value} · ${sub}` : it.value}>
                  {displayValue(it.value)}
                  {sub && <small className="ex-value-sub">{sub}</small>}
                </span>
                <button
                  type="button"
                  className={`st-toggle ex-toggle st-toggle--${it.enabled ? 'on' : 'off'}`}
                  aria-label={it.enabled ? 'Выключить исключение' : 'Включить исключение'}
                  onClick={() => toggle(it.value)}
                  disabled={saving}
                />
                <button type="button" className="ex-del" onClick={() => remove(it.value)} aria-label="Удалить" disabled={saving}>
                  <IconTrash size={15} />
                </button>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

// Адрес показываем без «/32»
function displayValue(v: string) {
  return v.endsWith('/32') ? v.slice(0, -3) : v;
}
