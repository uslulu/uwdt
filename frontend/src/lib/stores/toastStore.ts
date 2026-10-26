type Listener = (msg: string | null) => void;

// Кнопка в тосте, например «Вернуть» после удаления
export interface ToastAction {
  label: string;
  run: () => void;
}

let current: string | null = null;
let currentAction: ToastAction | null = null;
let timer: ReturnType<typeof setTimeout> | null = null;
const listeners = new Set<Listener>();

function notify() { listeners.forEach(fn => fn(current)); }

function hide() {
  if (timer) clearTimeout(timer);
  timer = null;
  current = null;
  currentAction = null;
  notify();
}

export const toastStore = {
  subscribe: (fn: Listener) => {
    listeners.add(fn);
    fn(current);
    return () => { listeners.delete(fn); };
  },
  show: (msg: string, ms = 3000, action?: ToastAction) => {
    if (timer) clearTimeout(timer);
    current = msg;
    currentAction = action ?? null;
    notify();
    timer = setTimeout(hide, ms);
  },
  action: () => currentAction,
  // Нажатие на кнопку тоста: выполнить действие и спрятать тост
  runAction: () => {
    const a = currentAction;
    hide();
    a?.run();
  },
};
