// Текущий этап подключения — строка под пузырём, пока туннель поднимается.
type Listener = (s: string) => void;

let current = '';
const listeners = new Set<Listener>();

export const stageStore = {
  set: (s: string) => {
    current = s;
    listeners.forEach(fn => fn(current));
  },
  subscribe: (fn: Listener) => {
    listeners.add(fn);
    fn(current);
    return () => { listeners.delete(fn); };
  },
};

// Туннель поднят, но каналов к серверу нет — трафик временно идёт напрямую.
type DegradedListener = (v: boolean) => void;
let degraded = false;
const degradedListeners = new Set<DegradedListener>();

export const degradedStore = {
  set: (v: boolean) => {
    degraded = v;
    degradedListeners.forEach(fn => fn(degraded));
  },
  subscribe: (fn: DegradedListener) => {
    degradedListeners.add(fn);
    fn(degraded);
    return () => { degradedListeners.delete(fn); };
  },
};

// Текущий этап подключения — строка под пузырём, пока туннель поднимается.
type HealthListener = (s: string) => void;

let healthCurrent = '';
const healthHealthListeners = new Set<HealthListener>();

export const healthStore = {
  set: (s: string) => {
    healthCurrent = s;
    healthHealthListeners.forEach(fn => fn(healthCurrent));
  },
  subscribe: (fn: HealthListener) => {
    healthHealthListeners.add(fn);
    fn(healthCurrent);
    return () => { healthHealthListeners.delete(fn); };
  },
};
