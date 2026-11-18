package backend

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// maxProfileNameRunes — предел длины имени профиля (имя = имя файла)
const maxProfileNameRunes = 100

// Store — пассивный модуль хранения данных.
// Все записи атомарные (write to tmp → rename).
// Не инициирует ничего — только отвечает на запросы.
//
// Структура файлов:
//
//	~/.config/pwdtt/config.json        — AppSettings
//	~/.config/pwdtt/servers/<name>.json — ProfileData (серверы)
//	~/.config/pwdtt/logs/*.log          — логи сессий
type Store struct {
	vault      vault
	settingsMu sync.Mutex
	profilesMu sync.Mutex
	baseDir    string // ~/.config/pwdtt
}

// NewStore создаёт Store и создаёт директории если нужно.
func NewStore() *Store {
	dir := configDir()
	os.MkdirAll(filepath.Join(dir, "servers"), 0o755)
	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)
	return &Store{baseDir: dir}
}

// ═══════════════════════════════════════════════════
// SETTINGS — настройки приложения
// ═══════════════════════════════════════════════════

// LoadSettings загружает настройки из config.json.
// Если файла нет — возвращает дефолтные настройки.
// Если obfsAccepted = false — obfsMode принудительно 'audio'.
func (s *Store) LoadSettings() AppSettings {
	defaults := AppSettings{AutoStart: true, ObfsMode: "audio", ObfsAccepted: false}
	data, err := os.ReadFile(filepath.Join(s.baseDir, "config.json"))
	if err != nil {
		return defaults
	}
	var settings AppSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return defaults
	}
	if !settings.ObfsAccepted {
		settings.ObfsMode = "audio"
	}
	return settings
}

// SaveSettings сохраняет настройки в config.json (атомарно).
func (s *Store) SaveSettings(settings AppSettings) error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	return s.saveSettings(settings)
}
func (s *Store) saveSettings(settings AppSettings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.baseDir, "config.json"), data)
}

func (s *Store) updateSettings(change func(*AppSettings)) error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	settings := AppSettings{AutoStart: true, ObfsMode: "audio"}
	data, err := os.ReadFile(filepath.Join(s.baseDir, "config.json"))
	if err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	change(&settings)
	return s.saveSettings(settings)
}

// ═══════════════════════════════════════════════════
// PROFILES — CRUD для серверов
// ═══════════════════════════════════════════════════

// sanitizeProfileName валидирует имя профиля: буквы и цифры (включая кириллицу),
// дефисы, подчёркивания, пробелы. Символы, опасные для файловой системы
// (\/ : * ? " < > | и управляющие), вырезаются.
func sanitizeProfileName(name string) string {
	var out []rune
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_', r == ' ':
			out = append(out, r)
		}
	}
	if len(out) > maxProfileNameRunes {
		out = out[:maxProfileNameRunes]
	}
	return strings.TrimSpace(string(out))
}

// A legacy filename becomes an immutable storage key. New profiles use UUIDs.
type storedProfile struct {
	Secret *sealedData `json:"secret,omitempty"`
	ProfileData
	DisplayName string `json:"displayName,omitempty"`
}

func (s *Store) profiles() (map[string]string, error) {
	dir := filepath.Join(s.baseDir, "servers")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var p storedProfile
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		name := p.DisplayName
		if name == "" {
			name = strings.TrimSuffix(e.Name(), ".json")
		}
		if _, ok := paths[name]; ok {
			return nil, fmt.Errorf("повтор имени профиля %q", name)
		}
		paths[name] = path
	}
	return paths, nil
}
func (s *Store) readProfile(path string) (*ProfileData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p storedProfile
	if err = json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.Secret != nil {
		plain, err := s.vault.open(*p.Secret, filepath.Base(path))
		if err != nil {
			return nil, err
		}
		var creds profileCredentials
		if err = json.Unmarshal(plain, &creds); err != nil {
			return nil, err
		}
		p.Password, p.Hashes = creds.Password, creds.Hashes
	} else if p.Password != "" || len(p.Hashes) > 0 {
		// Preserve readable legacy data if the OS keychain is locked.
		_ = s.persistProfile(path, p)
	}
	return &p.ProfileData, nil
}

type profileCredentials struct {
	Password string
	Hashes   []string
}

func (s *Store) persistProfile(path string, p storedProfile) error {
	if p.Password != "" || len(p.Hashes) > 0 {
		data, err := json.Marshal(profileCredentials{p.Password, p.Hashes})
		if err != nil {
			return err
		}
		blob, err := s.vault.seal(data, filepath.Base(path))
		if err != nil {
			return err
		}
		p.Secret = &blob
		p.Password = ""
		p.Hashes = nil
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

// Older WebViews retained punctuation in the label while the legacy file
// used the sanitized name. Never treat a renamed record as a legacy alias.
func profilePath(paths map[string]string, name string) string {
	if path := paths[name]; path != "" {
		return path
	}
	alias := sanitizeProfileName(name)
	if alias == name || alias == "" {
		return ""
	}
	path := paths[alias]
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var record storedProfile
	if json.Unmarshal(data, &record) == nil && record.DisplayName == "" {
		return path
	}
	return ""
}
func (s *Store) LoadProfile(name string) (*ProfileData, error) {
	s.profilesMu.Lock()
	defer s.profilesMu.Unlock()
	paths, err := s.profiles()
	if err != nil {
		return nil, err
	}
	return s.readProfile(profilePath(paths, name))
}

// DeviceID — постоянный ID этой установки UWDT. Сервер выдаёт WireGuard-пир
// по device_id: без него все устройства приходили как «unknown» и делили
// один пир — ответы сервера уходили то на мак, то на Windows.
func (s *Store) DeviceID() string {
	s.profilesMu.Lock()
	defer s.profilesMu.Unlock()
	path := filepath.Join(s.baseDir, "device_id")
	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id
		}
	}
	id := uuid.New().String()
	if err := atomicWrite(path, []byte(id)); err != nil {
		return "unknown"
	}
	return id
}

// SaveProfile сохраняет профиль сервера (атомарно).
func (s *Store) SaveProfile(name string, p ProfileData) error {
	return s.writeProfile("", name, p)
}
func (s *Store) writeProfile(previous, name string, p ProfileData) error {
	return s.changeProfile(previous, name, p, false)
}
func (s *Store) changeProfile(previous, name string, p ProfileData, create bool) error {
	if sanitizeProfileName(name) == "" || len([]rune(name)) > maxProfileNameRunes {
		return fmt.Errorf("недопустимое имя")
	}
	s.profilesMu.Lock()
	defer s.profilesMu.Unlock()
	paths, err := s.profiles()
	if err != nil {
		return err
	}
	path := paths[name]
	if create && path != "" {
		return fmt.Errorf("имя %q занято", name)
	}
	if previous != "" {
		path = profilePath(paths, previous)
		if path == "" {
			return fmt.Errorf("профиль %q отсутствует", previous)
		}
	}
	for existing, other := range paths {
		if strings.EqualFold(existing, name) && other != path {
			return fmt.Errorf("имя %q занято", name)
		}
	}
	if path == "" {
		path = filepath.Join(s.baseDir, "servers", uuid.NewString()+".json")
	} else {
		old, err := s.readProfile(path)
		if err != nil {
			return err
		}
		if old.DeviceID != "" {
			p.DeviceID = old.DeviceID
		}
	}
	if p.DeviceID == "" {
		p.DeviceID = uuid.NewString()
	}
	return s.persistProfile(path, storedProfile{ProfileData: p, DisplayName: name})
}
func (s *Store) DeleteProfile(name string) error {
	s.profilesMu.Lock()
	defer s.profilesMu.Unlock()
	paths, err := s.profiles()
	if err != nil {
		return err
	}
	return os.Remove(profilePath(paths, name))
}
func (s *Store) ListProfiles() map[string]ProfileData {
	profiles, _ := s.listProfiles()
	return profiles
}
func (s *Store) listProfiles() (map[string]ProfileData, error) {
	s.profilesMu.Lock()
	defer s.profilesMu.Unlock()
	paths, err := s.profiles()
	if err != nil {
		return nil, err
	}
	out := map[string]ProfileData{}
	for name, path := range paths {
		if p, err := s.readProfile(path); err == nil {
			out[name] = *p
		} else {
			return nil, err
		}
	}
	return out, nil
}

// ═══════════════════════════════════════════════════
// LOG FILE — запись полных логов в файл
// ═══════════════════════════════════════════════════

const logSegmentBytes = 4 << 20

type logRecord struct {
	flush      chan error
	level, msg string
	at         time.Time
}
type LogFile struct {
	mu      sync.RWMutex
	queue   chan logRecord
	done    chan struct{}
	dropped atomic.Int64
	closed  bool
}

func (s *Store) OpenLogFile() *LogFile {
	dir := filepath.Join(s.baseDir, "logs")
	f, err := os.CreateTemp(dir, time.Now().Format("2006-01-02_15-04-05.000000000")+"-*.log")
	if err != nil {
		return nil
	}
	lf := &LogFile{queue: make(chan logRecord, 256), done: make(chan struct{})}
	pruneSessionLogs(dir)
	go lf.writeLoop(f)
	return lf
}

// Keep recent bounded sessions. Historical files with other names are untouched.
func pruneSessionLogs(dir string) {
	paths, _ := filepath.Glob(filepath.Join(dir, "????-??-??_??-??-??.?????????-*.log"))
	if len(paths) > 8 {
		for _, path := range paths[:len(paths)-8] {
			_ = os.Remove(path)
			_ = os.Remove(path + ".previous")
		}
	}
}
func (lf *LogFile) writeLoop(file *os.File) {
	defer close(lf.done)
	path := file.Name()
	buffer := bufio.NewWriterSize(file, 32<<10)
	size, pending := 0, 0
	for record := range lf.queue {
		if record.flush != nil {
			record.flush <- buffer.Flush()
			continue
		}
		if size >= logSegmentBytes {
			buffer.Flush()
			file.Sync()
			file.Close()
			if err := os.Rename(path, path+".previous"); err != nil {
				return
			}
			var err error
			file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return
			}
			buffer.Reset(file)
			size = 0
		}
		if skipped := lf.dropped.Swap(0); skipped > 0 {
			fmt.Fprintf(buffer, "[ЖУРНАЛ] Пропущено фоновых строк: %d\n", skipped)
		}
		n, _ := fmt.Fprintf(buffer, "[%s] [%s] %s\n", record.at.Format("15:04:05"), record.level, record.msg)
		size += n
		pending++
		if pending >= 50 || strings.Contains(record.msg, "[МАРШРУТЫ]") {
			buffer.Flush()
			file.Sync()
			pending = 0
		}
	}
	buffer.Flush()
	file.Sync()
	file.Close()
}
func (lf *LogFile) Write(level, msg string) {
	if lf == nil {
		return
	}
	lf.mu.RLock()
	defer lf.mu.RUnlock()
	if lf.closed {
		return
	}
	record := logRecord{level: level, msg: msg, at: time.Now()}
	if level == "SKIP" {
		select {
		case lf.queue <- record:
		default:
			lf.dropped.Add(1)
		}
		return
	}
	select {
	case lf.queue <- record:
	case <-lf.done:
	}
}
func (lf *LogFile) Flush() error {
	if lf == nil {
		return nil
	}
	lf.mu.RLock()
	defer lf.mu.RUnlock()
	if lf.closed {
		<-lf.done
		return nil
	}
	ack := make(chan error, 1)
	select {
	case lf.queue <- logRecord{flush: ack}:
	case <-lf.done:
		return fmt.Errorf("журнал закрыт")
	}
	select {
	case err := <-ack:
		return err
	case <-lf.done:
		return fmt.Errorf("журнал закрыт")
	}
}
func (lf *LogFile) Close() {
	if lf == nil {
		return
	}
	lf.mu.Lock()
	if !lf.closed {
		close(lf.queue)
		lf.closed = true
	}
	lf.mu.Unlock()
	<-lf.done
}

// ReadLatestLog читает содержимое последнего лог-файла.
func (s *Store) ReadLatestLog() string {
	dir := filepath.Join(s.baseDir, "logs")
	entries, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil || len(entries) == 0 {
		return ""
	}
	// берём последний (сортировка по имени = по времени)
	latest := entries[len(entries)-1]
	data, err := os.ReadFile(latest)
	if err != nil {
		return ""
	}
	return string(data)
}

// ═══════════════════════════════════════════════════
// HELPERS
// ═══════════════════════════════════════════════════

// configDir возвращает ~/.config/pwdtt
func configDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.Getenv("HOME")
	}
	return filepath.Join(base, "pwdtt")
}

// atomicWrite записывает файл атомарно: tmp → rename.
func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}

// NewTestStore создаёт Store с временной директорией (для тестов).
// Подменяет HOME и, на Windows, AppData — os.UserConfigDir() читает именно их,
// иначе тесты пишут в реальный конфиг пользователя.
func NewTestStore(t interface {
	TempDir() string
	Setenv(string, string)
}) *Store {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("AppData", filepath.Join(dir, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	return NewStore()
}
