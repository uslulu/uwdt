import { useState } from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import {
  IconPlugConnected,
  IconTerminal2,
  IconSettings2,
  IconSun,
  IconMoon,
} from '@tabler/icons-react';
import { themeStore } from '../lib/stores/themeStore';
import Bubble from './Bubble';

const NAV = [
  { path: '/', icon: <IconPlugConnected stroke={2} size={22} />, label: 'Подключение' },
  { path: '/logs', icon: <IconTerminal2 stroke={2} size={22} />, label: 'Логи' },
];

interface Props {
  onSettings?: () => void;
  pathname?: string;
}

export default function Sidebar({ onSettings, pathname: pathnameProp }: Props) {
  const navigate = useNavigate();
  const location = useLocation();
  const pathname = pathnameProp ?? location.pathname;
  const [theme, setTheme] = useState(() => themeStore.get());

  const toggleTheme = () => {
    themeStore.toggle();
    setTheme(themeStore.get());
  };

  return (
    <>
      <style>{`
        .sidebar { width: 68px; background: var(--surface); border-right: 1px solid var(--border-2); display: flex; flex-direction: column; justify-content: space-between; padding: 14px 0 16px; flex-shrink: 0; }
        .sidebar-top, .sidebar-bottom { display: flex; flex-direction: column; align-items: center; gap: 6px; }
        .sidebar-logo { margin-bottom: 14px; display: flex; flex-direction: column; align-items: center; gap: 2px; }
        .sidebar-logo span { font-size: 9px; font-weight: 700; letter-spacing: 1.2px; color: var(--text-3); }
        .nav-btn { width: 44px; height: 44px; border: none; border-radius: 12px; background: transparent; color: var(--sidebar-text); cursor: pointer; display: flex; align-items: center; justify-content: center; transition: background 0.2s, color 0.2s; }
        .nav-btn:hover { background: var(--bg-2); color: var(--text); }
        .nav-btn--active, .nav-btn--active:hover { background: var(--sidebar-btn-active); color: var(--accent); }
        .theme-toggle { width: 36px; height: 36px; background: none; border: none; cursor: pointer; display: flex; align-items: center; justify-content: center; color: var(--sidebar-text); border-radius: 10px; transition: background 0.2s, color 0.2s; }
        .theme-toggle:hover { background: var(--bg-2); color: var(--text); }
        @keyframes icon-swap { 0% { transform: rotate(-90deg) scale(0.5); opacity: 0; } 100% { transform: rotate(0deg) scale(1); opacity: 1; } }
        .theme-toggle svg { animation: icon-swap 0.3s ease-out; }
      `}</style>
      <aside className="sidebar">
        <div className="sidebar-top">
          <div className="sidebar-logo" title="UWDT">
            <Bubble state="logo" size={34} />
            <span>UWDT</span>
          </div>
          {NAV.map(({ path, icon, label }) => (
            <button
              type="button"
              key={path}
              className={`nav-btn${pathname === path ? ' nav-btn--active' : ''}`}
              onClick={() => navigate(path)}
              title={label}
              aria-label={label}
            >
              {icon}
            </button>
          ))}
        </div>
        <div className="sidebar-bottom">
          <button type="button" className="theme-toggle" onClick={toggleTheme} title="Тема" aria-label="Переключить тему">
            {theme === 'light' ? <IconMoon size={17} stroke={2} /> : <IconSun size={17} stroke={2} />}
          </button>
          <button type="button" className="nav-btn" onClick={onSettings} aria-label="Настройки" title="Настройки">
            <IconSettings2 stroke={2} size={22} />
          </button>
        </div>
      </aside>
    </>
  );
}
