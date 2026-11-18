import { describe, it, expect, beforeEach, vi } from 'vitest';
import { loadServers, serverStore } from '../lib/store';

let catalog = '';
vi.mock('../../wailsjs/go/backend/App', () => ({
  ListProfiles:async () => ({}),SaveCatalog:async (data:string) => {catalog=data;},LoadCatalog:async () => catalog
}));
beforeEach(async () => {
  catalog='';
  localStorage.clear(); await loadServers();
});

describe('serverStore', async () => {
  it('getAll: пустой localStorage → []', async () => {
    expect(serverStore.getAll()).toEqual([]);
  });

  it('add: генерация UUID', async () => {
    const server = await serverStore.add({
      name: 'Test',
      host: '1.2.3.4:5555',
      password: 'secret',
    });

    expect(server.id).toBeTruthy();
    expect(server.id.length).toBeGreaterThan(0);
    expect(server.name).toBe('Test');
    expect(server.host).toBe('1.2.3.4:5555');
  });

  it('add: сохраняется в localStorage', async () => {
    await serverStore.add({
      name: 'A',
      host: '1.1.1.1:1111',
      password: 'pw',
    });

    const all = serverStore.getAll();
    expect(all.length).toBe(1);
    expect(all[0].name).toBe('A');
  });

  it('add: сохраняет мощность из профиля', async () => {
    await serverStore.add({ name: 'A', host: '1.1.1.1:1111', password: 'pw', power: 18 });

    expect(serverStore.getAll()[0].power).toBe(18);
  });

  it('add: несколько серверов', async () => {
    await serverStore.add({ name: 'A', host: 'a:1', password: 'p1' });
    await serverStore.add({ name: 'B', host: 'b:2', password: 'p2' });

    expect(serverStore.getAll().length).toBe(2);
  });

  it('update: обновление существующего сервера', async () => {
    const server = await serverStore.add({
      name: 'Old',
      host: '1.1.1.1:1111',
      password: 'pw',
    });

    await serverStore.update({ ...server, name: 'New', host: '2.2.2.2:2222' });

    const all = serverStore.getAll();
    expect(all.length).toBe(1);
    expect(all[0].name).toBe('New');
    expect(all[0].host).toBe('2.2.2.2:2222');
  });

  it('remove: удаление по id', async () => {
    const s1 = await serverStore.add({ name: 'A', host: 'a:1', password: 'p1' });
    const s2 = await serverStore.add({ name: 'B', host: 'b:2', password: 'p2' });

    await serverStore.remove(s1.id);

    const all = serverStore.getAll();
    expect(all.length).toBe(1);
    expect(all[0].id).toBe(s2.id);
  });

  it('remove: несуществующий id → ничего не меняется', async () => {
    await serverStore.add({ name: 'A', host: 'a:1', password: 'p1' });
    await serverStore.remove('nonexistent-id');

    expect(serverStore.getAll().length).toBe(1);
  });

  it('getLastSelectedId: пустой → null', async () => {
    expect(serverStore.getLastSelectedId()).toBeNull();
  });

  it('setLastSelectedId / getLastSelectedId', async () => {
    serverStore.setLastSelectedId('abc-123');
    expect(serverStore.getLastSelectedId()).toBe('abc-123');
  });

  it('setLastSelectedId(null) → удаление', async () => {
    serverStore.setLastSelectedId('abc-123');
    serverStore.setLastSelectedId(null);
    expect(serverStore.getLastSelectedId()).toBeNull();
  });

  it('parse с невалидным JSON → fallback', async () => {
    localStorage.setItem('wdtt_servers:v1', '{invalid json');
    expect(serverStore.getAll()).toEqual([]);

    localStorage.setItem('wdtt_settings:v1', 'not-json');
    // settingsStore.get() tested in settingsStore.test.ts
  });
});

it('migrates a synthetic subscription after verified readback', async () => {
  const old = [{id:'legacy',name:'old',host:'',password:'',kind:'xray',link:'vless://synthetic'}];
  localStorage.setItem('wdtt_servers:v1',JSON.stringify(old));
  await loadServers();await loadServers();
  expect(localStorage.getItem('wdtt_servers:v1')).toBeNull();
  expect(serverStore.getAll()).toEqual(old);
});
