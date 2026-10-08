// Скорость туннеля: ядро раз в 3 с присылает накопленные байты, скорость — разница между замерами.
export interface TunnelStats {
  active: number;
  downBps: number; // бит/с
  upBps: number;
}

type Listener = (s: TunnelStats | null) => void;

let current: TunnelStats | null = null;
let prev: { up: number; down: number; t: number } | null = null;
const listeners = new Set<Listener>();

const emit = () => listeners.forEach(fn => fn(current));

export const statsStore = {
  push: (raw: { active?: number; bytes_up?: number; bytes_down?: number }) => {
    const now = performance.now();
    const up = raw.bytes_up ?? 0;
    const down = raw.bytes_down ?? 0;
    if (prev && now > prev.t && up >= prev.up && down >= prev.down) {
      const dt = (now - prev.t) / 1000;
      current = { active: raw.active ?? 0, upBps: ((up - prev.up) * 8) / dt, downBps: ((down - prev.down) * 8) / dt };
    } else {
      current = { active: raw.active ?? 0, upBps: 0, downBps: 0 };
    }
    prev = { up, down, t: now };
    emit();
  },
  reset: () => {
    current = null;
    prev = null;
    emit();
  },
  subscribe: (fn: Listener) => {
    listeners.add(fn);
    fn(current);
    return () => { listeners.delete(fn); };
  },
};

export function formatRate(bps: number): { value: string; unit: string } {
  if (bps >= 1e6) return { value: (bps / 1e6).toFixed(bps >= 1e7 ? 0 : 1), unit: 'Мбит/с' };
  if (bps >= 1e3) return { value: (bps / 1e3).toFixed(0), unit: 'Кбит/с' };
  return { value: bps > 0 ? '<1' : '0', unit: 'Кбит/с' };
}

// Время активного подключения: отсчёт с момента, когда туннель поднялся.
let connectedAt: number | null = null;

export const sessionClock = {
  start: () => { if (connectedAt == null) connectedAt = Date.now(); },
  stop: () => { connectedAt = null; },
  since: () => connectedAt,
};

export function formatDuration(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const mm = String(m).padStart(2, '0');
  const ss = String(s).padStart(2, '0');
  return h > 0 ? `${h}:${mm}:${ss}` : `${m}:${ss}`;
}
