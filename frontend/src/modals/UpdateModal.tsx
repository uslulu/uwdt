import { useEffect, useState } from 'react';
import { IconX, IconDownload, IconRocket, IconExternalLink } from '@tabler/icons-react';
import { BrowserOpenURL, EventsOn } from '../../wailsjs/runtime/runtime';
import { InstallUpdate } from '../../wailsjs/go/backend/App';
import { renderMarkdown } from '../lib/utils/markdown';
import '../components/ui.css';
import './UpdateModal.css';

interface Props {
  version: string;
  body: string;
  url: string;
  page: string;
  onClose: () => void;
}

export default function UpdateModal({ version, body, url, page, onClose }: Props) {
  const sanitizedHtml = renderMarkdown(body);
  const [busy, setBusy] = useState(false);
  const [pct, setPct] = useState(0);
  const [error, setError] = useState('');

  useEffect(() => EventsOn('update_progress', (p: unknown) => setPct(Number(p) || 0)), []);

  const install = async () => {
    setBusy(true);
    setError('');
    setPct(0);
    try {
      await InstallUpdate(); // при успехе приложение перезапускается само
    } catch (e) {
      setError(String(e));
      setBusy(false);
    }
  };

  return (
    <div className="upd-overlay" onClick={busy ? undefined : onClose}>
      <div className="upd-modal" onClick={e => e.stopPropagation()}>
        <button type="button" className="upd-close" onClick={onClose} aria-label="Закрыть" disabled={busy}><IconX size={18} /></button>

        <div className="upd-header">
          <div className="upd-icon"><IconRocket size={20} /></div>
          <div>
            <div className="upd-title">Доступно обновление</div>
            <div className="upd-version">v{version}</div>
          </div>
        </div>

        {body && <div className="upd-body" dangerouslySetInnerHTML={{ __html: sanitizedHtml }} />}

        {busy && <div className="upd-progress"><div style={{ width: `${pct}%` }} /></div>}
        {error && <div className="upd-error">{error}</div>}
        {!busy && !error && <div className="upd-note">Туннель прервётся на несколько секунд — приложение перезапустится само.</div>}

        <div className="upd-actions">
          {error || !url ? (
            <button type="button" className="ui-btn ui-btn--secondary" onClick={() => BrowserOpenURL(page || url)}>
              <IconExternalLink size={16} />
              Страница релиза
            </button>
          ) : (
            <button type="button" className="ui-btn ui-btn--secondary" onClick={onClose} disabled={busy}>
              Позже
            </button>
          )}
          <button type="button" className="ui-btn ui-btn--primary" onClick={install} disabled={busy || !url}>
            <IconDownload size={16} />
            {busy ? (pct < 100 ? `Скачиваю… ${pct}%` : 'Устанавливаю…') : 'Обновить'}
          </button>
        </div>
      </div>
    </div>
  );
}
