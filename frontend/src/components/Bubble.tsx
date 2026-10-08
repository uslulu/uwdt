import { useId } from 'react';
import { IconPower } from '@tabler/icons-react';
import type { TunnelState } from '../lib/types';
import './Bubble.css';

// Пузырь с водой: уровень воды показывает состояние туннеля.
// idle — на донышке, connecting — поднимается и опускается, connected — наполнен и колышется.
export default function Bubble({ state, size = 176 }: { state: TunnelState | 'logo'; size?: number }) {
  const id = useId().replace(/:/g, '');
  const wave = 'M0 0 Q 25 -9 50 0 T 100 0 T 150 0 T 200 0 T 250 0 T 300 0 T 350 0 T 400 0 V 120 H 0 Z';
  return (
    <svg className={`bubble bubble--${state}`} width={size} height={size} viewBox="0 0 200 200" aria-hidden="true">
      <defs>
        <radialGradient id={`glass-${id}`} cx="38%" cy="32%" r="75%">
          <stop offset="0%" stopColor="var(--glass-hi)" stopOpacity="0.55" />
          <stop offset="55%" stopColor="var(--water-top)" stopOpacity="0.08" />
          <stop offset="100%" stopColor="var(--water-bottom)" stopOpacity="0.30" />
        </radialGradient>
        <linearGradient id={`water-${id}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="var(--water-top)" />
          <stop offset="100%" stopColor="var(--water-bottom)" />
        </linearGradient>
        <clipPath id={`clip-${id}`}>
          <circle cx="100" cy="100" r="88" />
        </clipPath>
      </defs>

      <circle cx="100" cy="100" r="88" fill={`url(#glass-${id})`} />

      <g clipPath={`url(#clip-${id})`}>
        <g className="bubble-level">
          <path className="bubble-wave bubble-wave--back" d={wave} fill={`url(#water-${id})`} />
          <path className="bubble-wave bubble-wave--front" d={wave} fill={`url(#water-${id})`} />
        </g>
      </g>

      <circle cx="100" cy="100" r="88" fill="none" stroke="var(--glass-edge)" strokeWidth="2" />
      <ellipse className="bubble-shine" cx="68" cy="52" rx="26" ry="13" transform="rotate(-32 68 52)" fill="var(--glass-hi)" />
      <circle className="bubble-shine" cx="146" cy="138" r="5" fill="var(--glass-hi)" opacity="0.5" />

      {state !== 'logo' && (
        <foreignObject x="70" y="70" width="60" height="60">
          <div className="bubble-icon"><IconPower size={52} stroke={2.2} /></div>
        </foreignObject>
      )}
    </svg>
  );
}
