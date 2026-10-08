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
