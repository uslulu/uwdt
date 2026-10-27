export interface Server {
  id: string;
  name: string;
  host: string;
  password: string;
  deviceId?: string;
  ping?: number;
  icon?: string;
  hashes?: [string, string, string, string];
  power?: number;
  // Сервер из подписки (движок Xray): ссылка vless://, название подписки и её адрес
  kind?: 'xray';
  link?: string;
  group?: string;
  subUrl?: string;
  subUpdated?: number; // когда подписку последний раз скачивали (мс)
  transport?: string;
}

export interface AppSettings {
  autoStart: boolean;
  obfsMode: 'audio' | 'video';
  obfsAccepted: boolean;
  turnTcp: boolean;
}

export interface ExcludeEntry {
  value: string;
  enabled: boolean;
}

export type TunnelState = 'idle' | 'connecting' | 'connected' | 'disconnecting';

export const DEFAULT_SETTINGS: AppSettings = {
  autoStart: true,
  obfsMode: 'audio',
  obfsAccepted: false,
  turnTcp: false,
};
