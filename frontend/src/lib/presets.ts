import type { ExcludeEntry } from './types';
// Готовые наборы исключений: сервис → его сети (номер AS по реестру RIPE) и домены.
// Номера сверены через RIPEstat (as-overview) 08.10.2026.
export interface Preset {
  label: string;
  values: string[];
}

export const PRESET_GROUPS: { title: string; presets: Preset[] }[] = [
  {
    title: 'Игры',
    presets: [
      { label: 'Steam / Dota 2', values: ['AS32590'] },
      { label: 'Battle.net', values: ['AS57976', 'AS32163'] },
    ],
  },
  {
    title: 'Сервисы',
    presets: [
      { label: 'ВКонтакте', values: ['AS47541', 'AS47764'] },
      { label: 'Яндекс', values: ['AS13238', 'AS210656'] },
      { label: 'Госуслуги', values: ['AS196747', 'gosuslugi.ru'] },
      { label: 'Ozon', values: ['AS44386'] },
      { label: 'Wildberries', values: ['AS57073'] },
      { label: 'Авито', values: ['AS201012'] },
      { label: 'Okko', values: ['AS211609'] },
      { label: 'ivi', values: ['AS57629'] },
      // Apple сверяет страну аккаунта с адресом: через туннель песни не включаются («Invalid store»)
      {
        label: 'Apple Music',
        values: [
          'AS714', 'music.apple.com', 'play.itunes.apple.com', 'init.itunes.apple.com', 'bag.itunes.apple.com',
          'amp-api.music.apple.com', 'api.music.apple.com', 'audio-ssl.itunes.apple.com', 'se.itunes.apple.com',
          'su.itunes.apple.com',
        ],
      },
    ],
  },
  {
    title: 'Банки',
    presets: [
      { label: 'Сбер', values: ['AS35237'] },
      { label: 'Т-Банк', values: ['AS205638'] },
      { label: 'Альфа-Банк', values: ['AS15632'] },
      { label: 'ВТБ', values: ['AS24823', 'AS41551'] },
      { label: 'ЮMoney', values: ['AS43247'] },
    ],
  },
];

export function directCount(entries: ExcludeEntry[]) {
  return new Set(entries.filter(e => e.enabled).map(e => e.source ? 'set:' + e.source : 'manual:' + e.value)).size;
}

export function presetCount(entries: ExcludeEntry[], preset: Preset) {
  return preset.values.filter(value => entries.some(e => e.enabled && e.source === preset.label && e.value === value)).length;
}

export function changePreset(entries: ExcludeEntry[], preset: Preset) {
  const others = entries.filter(e => e.source !== preset.label);
  return presetCount(entries, preset) === preset.values.length ? others :
    [...others, ...preset.values.map(value => ({ value, enabled: true, source: preset.label }))];
}

function quantity(n: number, forms: [string, string, string]) {
  const unit = new Intl.PluralRules('ru').select(n);
  return n + ' ' + forms[unit === 'one' ? 0 : unit === 'few' ? 1 : 2];
}
export function directSummary(entries: ExcludeEntry[], russia: boolean) {
  const enabled = entries.filter(e => e.enabled);
  const sets = new Set(enabled.map(e => e.source).filter(Boolean)).size;
  const manual = new Set(enabled.filter(e => !e.source).map(e => e.value)).size;
  const parts = [
    russia && 'российские сети',
    sets > 0 && quantity(sets, ['набор', 'набора', 'наборов']),
    manual > 0 && quantity(manual, ['правило', 'правила', 'правил']),
  ].filter((part): part is string => typeof part === 'string');
  return 'Напрямую: ' + (new Intl.ListFormat('ru').format(parts) || 'нет правил');
}
