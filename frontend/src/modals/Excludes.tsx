import { useState, useEffect, useRef } from 'react';
import { IconX, IconTrash } from '@tabler/icons-react';
import type { ExcludeEntry } from '../lib/types';
import { GetExcludes, SetExcludes, CheckExclude, GetRussiaDirect, SetRussiaDirect, InspectAddress, RuleStatus, RetryRules } from '../../wailsjs/go/backend/App';
import Toast from '../components/Toast';
import { toastStore } from '../lib/stores/toastStore';
import { PRESET_GROUPS, changePreset, presetCount, type Preset } from '../lib/presets';
import { tunnelStore } from '../lib/stores/tunnelStore';
import type { backend } from '../../wailsjs/go/models';
import './Settings.css';
import './Excludes.css';

const presets = PRESET_GROUPS.flatMap(group => group.presets);
function scope(p: Preset) {
  if (p.label === 'Steam / Dota 2') return 'Сети Valve';
  if (p.label === 'Apple Music') return '9 доменов Apple и вся сеть Apple AS714';
  return p.values.some(v => !v.startsWith('AS')) ? 'Домены и сети организации' : 'Сети организации';
}

export default function Excludes({ onClose }: { onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [items, setItems] = useState<ExcludeEntry[]>([]);
  const [ru, setRu] = useState<backend.RussiaInfo>();
  const [adding, setAdding] = useState(false);
  const [query, setQuery] = useState('');
  const [address, setAddress] = useState('');
  const [rows, setRows] = useState<backend.AddressRoute[]>([]);
  const [inspection, setInspection] = useState('');
  const request = useRef(0);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const locked = useRef(false);
  const addButton = useRef<HTMLButtonElement>(null);
  const [status,setStatus]=useState('Проверяем состояние…');
  const [state, setState] = useState(tunnelStore.get);
  useEffect(() => tunnelStore.subscribe(setState), []);
  useEffect(() => {
    const element = dialog.current;
    element?.showModal();
    let live = true;
    Promise.all([GetExcludes(), GetRussiaDirect()]).then(([list, info]) => {
      if (live) {setItems(list ?? []); setRu(info);}
    }).catch(e => {if (live) setError(String(e));});
    return () => { live = false; element?.close(); };
  }, []);
  useEffect(() => {
    let live=true;
    let timer:ReturnType<typeof setTimeout>;
    const poll=async () => {try {const result=await RuleStatus();if(live)setStatus(result);} catch {if(live)setStatus('Состояние неизвестно');}
      if(live)timer=setTimeout(poll,1000);
    };
    void poll();return () => {live=false;clearTimeout(timer);};
  },[state]);
  async function save(operation: () => Promise<void>) {
    if (locked.current) return;
    locked.current = true;
    setBusy(true); setError('');
    try { await operation(); setStatus(await RuleStatus()); window.dispatchEvent(new Event('direct-rules-changed')); }
    catch (e) { setError(String(e)); }
    finally {locked.current = false; setBusy(false);}
  }
  async function write(next: ExcludeEntry[]) {
    setItems((await SetExcludes(next)) ?? []);
  }
  function togglePreset(p: Preset) {
    void save(() => write(changePreset(items, p)));
  }
  function addManual() {
    void save(async () => {
      const value = await CheckExclude(query.trim());
      if (items.some(e => !e.source && e.value === value)) throw new Error('Уже есть в своих правилах');
      await write([...items, {value, enabled:true}]);
      setQuery(''); setAdding(false); addButton.current?.focus();
    });
  }
  function remove(entry: ExcludeEntry) {
    void save(async () => {
      await write(items.filter(e => e !== entry));
      toastStore.show('Удалено ' + entry.value, 5000, {label:'Вернуть', run: () => {
        void save(async () => {
          const current = (await GetExcludes()) ?? [];
          if (!current.some(e => !e.source && e.value === entry.value)) await write([...current,entry]);
        });
      }});
    });
  }
  async function inspect() {
    const id = ++request.current;
    setInspection('Проверяем…'); setRows([]);
    try {
      const result = await InspectAddress(address.trim());
      if (request.current === id) {setRows(result ?? []); setInspection(result?.length ? '' : 'DNS не вернул адресов');}
    } catch(e) {if (request.current === id) setInspection(String(e));}
  }
  function presetRow(p: Preset) {
    const count = presetCount(items, p);
    return <div className="ex-rule" key={p.label}><button type="button" className="ex-preset" disabled={busy || !ru}
      aria-pressed={count === p.values.length ? true : count ? 'mixed' : false} onClick={() => togglePreset(p)}>
      {p.label} · {scope(p)} · {count === p.values.length ? 'Включён' : count ? `Частично (${count}/${p.values.length})` : 'Выключен'}
    </button><details><summary>Состав</summary>{p.values.map(value => {
      const entry = items.find(e => e.source === p.label && e.value === value);
      return <label key={value}><input type="checkbox" checked={!!entry?.enabled} disabled={busy || !ru} onChange={() => save(() => write(
        entry ? items.map(e => e === entry ? {...e, enabled:!e.enabled} : e) : [...items,{source:p.label,value,enabled:true}]
      ))}/>{value}</label>;
    })}</details></div>;
  }
  return <dialog ref={dialog} className="st-modal ex-modal direct-dialog" aria-labelledby="direct-title" onCancel={onClose}>
    <div className="st-header"><strong id="direct-title">Что идёт напрямую</strong><button className="st-close" onClick={onClose} aria-label="Закрыть"><IconX size={18}/></button></div>
    <p className="direct-status" role="status">{busy ? 'Сохранение…' : status}</p>
    {(status.startsWith('Ошибка') || status.startsWith('Нет IPv4')) && <button onClick={() => save(async () => {await RetryRules();setStatus(await RuleStatus());})}>Повторить применение</button>}
    <label><input type="checkbox" checked={!!ru?.enabled} disabled={!ru || busy} onChange={() => save(async () => {setRu(await SetRussiaDirect(!ru?.enabled));})}/>
      Россия напрямую · набор российских IPv4-сетей{ru && ` (${ru.count.toLocaleString('ru-RU')})`}</label>
    {!adding && presets.filter(p => items.some(e => e.source === p.label)).map(presetRow)}
    {items.filter(e => !e.source).map(entry => <div className="ex-item" key={entry.value}>
      <label><input type="checkbox" checked={entry.enabled} disabled={busy} onChange={() => save(() => write(items.map(e => e === entry ? {...e,enabled:!e.enabled} : e)))}/>{entry.value}</label>
      <button className="ex-del" aria-label={'Удалить '+entry.value} disabled={busy} onClick={() => remove(entry)}><IconTrash size={16}/></button>
    </div>)}
    <button ref={addButton} className="ex-preset ex-open-add" aria-expanded={adding} onClick={() => setAdding(!adding)}>Добавить</button>
    {adding && <section><form onSubmit={e => {e.preventDefault(); addManual();}}><input autoFocus className="ex-input" aria-label="Поиск сервиса или адрес" placeholder="Сервис, домен, URL, IPv4, подсеть или AS" value={query} onChange={e => setQuery(e.target.value)}/>
      <button className="ex-preset" disabled={!query.trim() || busy || !ru}>Добавить адрес</button></form>
      {presets.filter(p => (p.label+' '+scope(p)+' '+p.values.join(' ')).toLowerCase().includes(query.trim().toLowerCase())).map(presetRow)}
    </section>}
    {error && <p role="alert">{error}</p>}
    <details><summary>Как работает правило домена</summary><p>Напрямую идут разрешённые IPv4-адреса домена. Поддомены могут иметь другие адреса. Один IP CDN могут использовать несколько сайтов. Правило действует для всех приложений.</p></details>
    <form onSubmit={e => {e.preventDefault(); void inspect();}}><input className="ex-input" aria-label="Адрес для проверки" placeholder="Домен или IP" value={address} onChange={e => {++request.current;setAddress(e.target.value); setRows([]);setInspection('');}}/>
      <button className="ex-preset" disabled={!address.trim()}>Проверить адрес</button></form>
    <div aria-live="polite">{inspection}{rows.map(row => <p key={row.ip}><strong>{row.ip}</strong><br/>{row.ip.includes(':') ? 'Правила UWDT поддерживают только IPv4' : row.rules?.length ? 'Правило напрямую · '+row.rules.join(', ') : 'Совпадений по правилам и кэшу нет'}<br/>Маршрут ОС · {row.error || row.route}</p>)}</div>
  <Toast /></dialog>;
}
