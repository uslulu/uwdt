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

// Подпись записи списка, если она пришла из набора
export const PRESET_LABEL: Record<string, string> = Object.fromEntries(
  PRESET_GROUPS.flatMap(g => g.presets.flatMap(p => p.values.map(v => [v, p.label]))),
);
