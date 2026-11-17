import { useState } from 'react';
import { IconHash, IconX } from '@tabler/icons-react';
import { toastStore } from '../lib/stores/toastStore';
import '../components/ui.css';
import './server-form.css';

const normalize = (v: string) => {
  const idx = v.toLowerCase().indexOf('/call/join/');
  if (idx !== -1) v = v.slice(idx + '/call/join/'.length);
  return v.split(/[?#/]/)[0].trim();
};

interface Props {
  hashes: [string, string, string, string];
  onClose: () => void;
  onSave: (hashes: [string, string, string, string]) => void;
}

export default function Hash({ hashes, onClose, onSave }: Props) {
  const [values, setValues] = useState<[string, string, string, string]>([...hashes] as [string, string, string, string]);

  const set = (i: number, v: string) => {
    const next = [...values] as [string, string, string, string];
    next[i] = v;
    setValues(next);
  };

  const save = () => {
    const normalized = values.map(normalize) as [string, string, string, string];
    const nonEmpty = normalized.filter(v => v !== '');
    if (new Set(nonEmpty).size !== nonEmpty.length) {
      toastStore.show('Обнаружены дублирующиеся хеши');
      return;
    }
    onSave(normalized);
    onClose();
  };

  return (
    <div className="ui-overlay" style={{ zIndex: 200 }}>
      <div className="ui-modal" onClick={e => e.stopPropagation()}>
        <div className="ui-modal-head">
          <IconHash stroke={2} size={20} />
          <span className="ui-modal-title">Хеши</span>
          <button type="button" className="ui-icon-btn" onClick={onClose} aria-label="Закрыть"><IconX size={18} /></button>
        </div>
        <div className="sf-form">
          {values.map((v, i) => (
            <input key={['hash-a', 'hash-b', 'hash-c', 'hash-d'][i]} className="ui-input" placeholder={`Хеш ${i + 1}`} value={v} onChange={e => set(i, e.target.value)} />
          ))}
          <button type="button" className="ui-btn ui-btn--primary ui-btn--block" onClick={save}>Сохранить</button>
        </div>
      </div>
    </div>
  );
}
