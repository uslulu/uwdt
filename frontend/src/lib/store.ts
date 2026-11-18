import { LoadCatalog, SaveCatalog, ListProfiles } from '../../wailsjs/go/backend/App';
import type { Server, AppSettings } from './types';
import { DEFAULT_SETTINGS } from './types';

const SERVERS_KEY = 'wdtt_servers:v1';
const SETTINGS_KEY = 'wdtt_settings:v1';
const LAST_SERVER_KEY = 'wdtt_last_server:v1';

function parse<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback;
  }
}

let catalog: Server[] = [];
let queue = Promise.resolve();
type CatalogRow = Server & { profileRef?: string };

async function persist(servers: Server[]) {
  const profiles = await ListProfiles();
  const rows: CatalogRow[] = servers.map(server => {
    const profile = profiles?.[server.name];
    if (server.kind === 'xray' || !profile || profile.password !== server.password ||
        JSON.stringify(profile.hashes?.filter(Boolean) ?? []) !== JSON.stringify(server.hashes?.filter(Boolean) ?? [])) return server;
    return {...server, password:'', hashes:undefined, profileRef:profile.device_id || server.name};
  });
  const serialized=JSON.stringify(rows);await SaveCatalog(serialized);return serialized;
}
function mutate(change: (current: Server[]) => Server[]) {
  const task = queue.then(async () => {
    const next = change(catalog);
    await persist(next);
    catalog = next;
  });
  queue = task.catch(() => {});
  return task;
}
export async function loadServers() {
  const saved = await LoadCatalog();
  const legacy = localStorage.getItem(SERVERS_KEY);
  if (saved) {
    const rows: CatalogRow[] = JSON.parse(saved);
    const profiles = await ListProfiles();
    catalog = rows.flatMap(({profileRef,...row}):Server[] => {
      if (!profileRef) return [row];
      const match=Object.entries(profiles ?? {}).find(([name,p]) => p.device_id===profileRef || name===profileRef);
      if(!match)return [];
      const [name,profile]=match;
      const h = profile.hashes ?? [];
      return [{...row,name,host:profile.peer,power:profile.workers,password:profile.password,hashes:[h[0]??'',h[1]??'',h[2]??'',h[3]??'']}];
    });
  } else { catalog = []; }
  if (legacy) {
    const old: Server[] = JSON.parse(legacy);
    if (!Array.isArray(old)) throw new Error('Повреждён список серверов');
    const merged = [...catalog,...old.filter(s => !catalog.some(cur => cur.id === s.id))];
    const serialized=await persist(merged);
    const verified = await LoadCatalog();
    if (verified !== serialized) throw new Error('Не удалось проверить перенос серверов');
    catalog = merged;localStorage.removeItem(SERVERS_KEY);
  }
}
export const serverStore = {
  getAll: (): Server[] => catalog.map(s => ({...s})),
  save: (servers: Server[]) => mutate(() => servers),
  add: async (server: Omit<Server, 'id'>): Promise<Server> => {
    const s: Server = { ...server, id: crypto.randomUUID() };
    await mutate(all => [...all,s]);
    return s;
  },
  update: (server: Server) => mutate(all => all.map(s => s.id === server.id ? server : s)),
  remove: (id: string) => mutate(all => all.filter(s => s.id !== id)),
  getLastSelectedId: (): string | null => parse<string | null>(LAST_SERVER_KEY, null),
  setLastSelectedId: (id: string | null) => {
    if (id) localStorage.setItem(LAST_SERVER_KEY, JSON.stringify(id));
    else localStorage.removeItem(LAST_SERVER_KEY);
  },
};

export const settingsStore = {
  get: (): AppSettings => {
    const saved = parse<Partial<AppSettings>>(SETTINGS_KEY, {});
    const merged = { ...DEFAULT_SETTINGS, ...saved };
    if (!merged.obfsAccepted) merged.obfsMode = 'audio';
    return merged;
  },
  save: (settings: AppSettings) => localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings)),
};
