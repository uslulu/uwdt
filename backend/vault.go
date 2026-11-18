package backend

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/zalando/go-keyring"
)

// Only the data key lives in the OS credential store. A subscription can
// exceed the Windows credential size limit even for a single server group.
const vaultService = "UWDT profile storage"

type sealedData struct {
	Key  string `json:"key"`
	Data []byte `json:"data"`
}
type vault struct {
	mu      sync.Mutex
	current string
}

func (v *vault) cipher(id string) (cipher.AEAD, error) {
	encoded, err := keyring.Get(vaultService, id)
	if err != nil {
		return nil, fmt.Errorf("системное хранилище: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}
func (v *vault) open(blob sealedData, purpose string) ([]byte, error) {
	a, err := v.cipher(blob.Key)
	if err != nil {
		return nil, err
	}
	plain, err := a.Open(nil, nil, blob.Data, []byte(purpose))
	if err == nil {
		v.mu.Lock()
		v.current = blob.Key
		v.mu.Unlock()
	}
	return plain, err
}
func (v *vault) seal(data []byte, purpose string) (sealedData, error) {
	v.mu.Lock()
	id := v.current
	if id == "" {
		key := make([]byte, 32)
		rand.Read(key)
		id = uuid.NewString()
		if err := keyring.Set(vaultService, id, base64.StdEncoding.EncodeToString(key)); err != nil {
			v.mu.Unlock()
			return sealedData{}, fmt.Errorf("системное хранилище: %w", err)
		}
		v.current = id
	}
	v.mu.Unlock()
	a, err := v.cipher(id)
	if err != nil {
		return sealedData{}, err
	}
	blob := sealedData{id, a.Seal(nil, nil, data, []byte(purpose))}
	verified, err := v.open(blob, purpose)
	if err != nil {
		return sealedData{}, err
	}
	if !bytes.Equal(verified, data) {
		return sealedData{}, fmt.Errorf("ошибка проверки защищённой записи")
	}
	return blob, nil
}

func (a *App) LoadCatalog() (string, error) {
	file, err := os.ReadFile(filepath.Join(a.store.baseDir, "catalog.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var blob sealedData
	if err = json.Unmarshal(file, &blob); err != nil {
		return "", err
	}
	data, err := a.store.vault.open(blob, "catalog")
	return string(data), err
}
func (a *App) SaveCatalog(data string) error {
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &rows); err != nil {
		return err
	}
	blob, err := a.store.vault.seal([]byte(data), "catalog")
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(blob)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(a.store.baseDir, "catalog.json"), encoded)
}
