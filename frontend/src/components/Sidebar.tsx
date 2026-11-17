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
import './ui.css';
import './Sidebar.css';

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
              aria-current={pathname === path ? 'page' : undefined}
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
  );
}
