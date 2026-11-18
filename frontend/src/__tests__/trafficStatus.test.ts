import { afterEach, expect, it, vi } from 'vitest';
import { statsStore } from '../lib/stores/statsStore';

afterEach(() => { statsStore.reset(); vi.useRealTimers(); });
it('requires received bytes in this session and preserves the last observation across idle samples', () => {
  vi.useFakeTimers();
  vi.setSystemTime(10000);
  const observations: (number | undefined)[] = [];
  const unsubscribe = statsStore.subscribe(sample => observations.push(sample?.receivedAt));
  statsStore.push({ active: 1, bytes_up: 50 });
  statsStore.push({ active: 1, bytes_down: 10 });
  vi.setSystemTime(20000);
  statsStore.push({ active: 1, bytes_down: 10 });
  statsStore.reset();
  statsStore.push({ active: 1 });
  unsubscribe();
  expect(observations).toEqual([undefined, 0, 10000, 10000, undefined, 0]);
});
