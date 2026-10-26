import { useEffect, useState } from 'react';
import { toastStore } from '../lib/stores/toastStore';
import './Toast.css';

export default function Toast() {
  const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => toastStore.subscribe(setMsg), []);
  if (!msg) return null;
  const action = toastStore.action();
  return (
    <div className={`toast${action ? ' toast--action' : ''}`}>
      {msg}
      {action && (
        <button type="button" className="toast-btn" onClick={() => toastStore.runAction()}>
          {action.label}
        </button>
      )}
    </div>
  );
}
