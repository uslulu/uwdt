<p align="center">
  <img src="assets/icons/icon.png" width="96" />
</p>

<h1 align="center">UWDT</h1>

<p align="center">
  Десктопный VPN-клиент для macOS и Windows: трафик идёт через TURN-серверы VK<br>
  и выглядит как зашифрованный медиатрафик звонка.<br>
  <sub>Сборка на основе <a href="https://github.com/luminescq/PWDTT">PWDTT</a> (luminescq), GPL-3.0</sub>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/Wails-v2-red?style=for-the-badge&logo=wails&logoColor=white" alt="Wails">
  <img src="https://img.shields.io/badge/Windows-amd64-0078D4?style=for-the-badge&logo=windows&logoColor=white" alt="Windows">
  <img src="https://img.shields.io/badge/macOS-arm64-000000?style=for-the-badge&logo=apple&logoColor=white" alt="macOS">
</p>

## Чем UWDT отличается от PWDTT

- **Исключения из туннеля.** В настройках — список подсетей, адресов и доменов, которые идут напрямую, мимо туннеля. Изменения применяются сразу, без переподключения.
- **«Россия напрямую».** Одним переключателем все российские IP-сети (по реестру RIPE, ~8,6 тыс. сетей) идут мимо туннеля. Список вшит и обновляется раз в неделю.
- **Совместимость с корпоративным VPN.** Маршрут Cisco к его серверу и маршруты, которые другой VPN ведёт через свой интерфейс, не перехватываются; DNS, обслуживаемый другим VPN, не уводится мимо него.
- **Ручная капча.** Если ВК просит проверку, открывается окно со страницей капчи; после решения подключение продолжается.
- **Пинг серверов** в списке, **скорость туннеля** (скачивание, отдача, каналы) на главном экране.
- **Новый интерфейс:** минималистичный, «пузырь» вместо кнопки, светлая и тёмная тема.

## Установка

- **macOS (Apple Silicon):** скачайте `UWDT-macos-arm64.zip` из [релизов](../../releases), распакуйте и перенесите `UWDT.app` в «Программы». Сборка подписана локально — при первом запуске откройте через правый клик → «Открыть».
- **Windows 10/11 (x64):** скачайте `UWDT-windows-amd64.zip`, распакуйте и запустите `UWDT.exe` (попросит права администратора — без них туннель не поднять).

Обновления приходят сами: приложение проверяет релизы этого репозитория и ставит новую версию по кнопке «Обновить», сверив контрольную сумму файла.

Сервер — тот же `wdtt-server`, что и для PWDTT.

---

## Как это работает

Приложение поднимает локальный WireGuard-интерфейс и передаёт его трафик через TURN/DTLS серверы VK, оборачивая пакеты в RTP с шифрованием ChaCha20-Poly1305. С точки зрения провайдера — это обычный зашифрованный VK-звонок.

```
Приложение → WireGuard → ChaCha20/RTP → VK TURN/DTLS → wdtt-server (VPS) → интернет
```

---

## Запуск

### Linux

**1. Установите зависимости:**

```bash
# Ubuntu/Debian
sudo apt install wireguard-tools libwebkit2gtk-4.1-dev

# Arch
sudo pacman -S wireguard-tools webkit2gtk-4.1
```

**2. Настройте права для WireGuard:**

Приложению нужны права root для управления сетевым интерфейсом. Установите sudoers-правило:

```bash
# Одной командой (скачает и запустит):
sudo bash <(curl -s https://raw.githubusercontent.com/luminescq/PWDTT/main/assets/install-sudoers.sh)
```

Или вручную через `visudo` — добавьте в `/etc/sudoers`:
```
your_user ALL=(ALL) NOPASSWD: /usr/bin/ip, /usr/bin/wg
```

Скрипт автоматически определит текущего пользователя и создаст файл `/etc/sudoers.d/pwdtt`.

**3. Запустите:**

```bash
chmod +x pwdtt-linux-amd64
./pwdtt-linux-amd64
```

### Windows

Скачайте `pwdtt-windows-amd64.exe` из [Releases](https://github.com/luminescq/PWDTT/releases) и запустите. Драйвер WireGuard (wintun) встроен.

### macOS

Скачайте `PWDTT-macos.zip` из [Releases](https://github.com/luminescq/PWDTT/releases), распакуйте и запустите `PWDTT.app`. При первом запуске macOS запросит разрешение на создание сетевого интерфейса — введите пароль администратора.

> Universal бинарник: работает на Intel и Apple Silicon (M1/M2/M3/M4).

---

## Быстрый старт

1. **Добавьте сервер** — кнопка `+` → вставьте `wdtt://`-ссылку или введите вручную
2. **VK-хеши** — Настройки → вставьте хеши из `vk.com/call/join/<hash>`
3. **Подключение** — кнопка питания

---

## Ссылки

```
wdtt://<IP>:<DTLS_PORT>:<WG_PORT>:<PROXY_PORT>:<PASSWORD>[:<HASH1>,<HASH2>,...][#название]
```

- Поля 1–5 обязательны
- Хеши — опциональны, через запятую, до 4 штук
- `#название` — опциональный псевдоним сервера

Пример:
```
wdtt://1.2.3.4:56000:56001:0:mypassword:AbCdEfGh,XyZ12345#Мой сервер
```

Вставить ссылку можно через кнопку `+` или просто **Ctrl+V** в любом месте окна.

> Также принимаються ссылки qwdtt.

---

## Сборка из исходников

**Зависимости:** Go 1.26+, Node.js 22+, [Wails v2](https://wails.io)

```bash
# Linux
sudo apt install libayatana-appindicator3-dev pkg-config gcc libwebkit2gtk-4.1-dev
go install github.com/wailsapp/wails/v2/cmd/wails@latest

git clone https://github.com/luminescq/PWDTT
cd PWDTT
wails build -platform linux/amd64 -tags webkit2_41 -o pwdtt-linux-amd64
# → build/bin/pwdtt-linux-amd64
```

```bash
# Windows (кросс-компиляция с Linux)
wails build -platform windows/amd64
# → build/bin/pwdtt.exe
```

```bash
# macOS (только на macOS)
wails build -platform darwin/universal
# → build/bin/pwdtt-macos
```

---

## Отчёт об ошибках

Если приложение работает некорректно, вы можете собрать отчёт прямо из интерфейса:

1. Откройте **Настройки** (шестерёнка вверху)
2. Нажмите **Отчёт** — информация о системе и логах сессии скопируется в буфер обмена
3. Создайте [Issue](https://github.com/luminescq/PWDTT/issues/new) и вставьте отчёт

Отчёт содержит: версию ОС, версию Go, версию приложения, имя хоста и.filtered логи текущей сессии.

---

> [!IMPORTANT]
> Приложение является техническим инструментом для защищённого туннелирования собственного трафика через ваш сервер. Автор не призывает использовать PWDTT для противоправных целей или нарушения правил сторонних сервисов.

---

## Лицензия

Этот проект распространяется под лицензией GNU General Public License v3.0.
