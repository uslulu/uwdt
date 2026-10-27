import { useEffect, useState } from 'react';
import { AnimatePresence, motion } from 'motion/react';
import { toastStore } from '../lib/stores/toastStore';
import './Toast.css';

export default function Toast() {
  const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => toastStore.subscribe(setMsg), []);
  const action = msg ? toastStore.action() : null;
  // Обёртка центрирует тост, а transform самого тоста отдан анимации появления и ухода
  return (
    <div className="toast-wrap">
      <AnimatePresence>
        {msg && (
          <motion.div
            key={msg}
            className={`toast${action ? ' toast--action' : ''}`}
            initial={{ opacity: 0, y: 12, scale: 0.96 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 8, scale: 0.98 }}
            transition={{ type: 'spring', stiffness: 460, damping: 32 }}
          >
            {msg}
            {action && (
              <button type="button" className="toast-btn" onClick={() => toastStore.runAction()}>
                {action.label}
              </button>
            )}
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
