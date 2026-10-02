package quota

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"

	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badjson"
	"github.com/sagernet/sing/service/filemanager"
)

type Cache struct {
	Users *badjson.TypedMap[string, *UserCache] `json:"users"`
}

type UserCache struct {
	UplinkBytes   int64 `json:"uplink_bytes"`
	DownlinkBytes int64 `json:"downlink_bytes"`
}

func (s *Service) loadCache() error {
	if s.cachePath == "" {
		return nil
	}
	basePath := filemanager.BasePath(s.ctx, s.cachePath)
	cacheBinary, err := os.ReadFile(basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err = s.decodeCache(cacheBinary); err != nil {
		os.RemoveAll(basePath)
		return err
	}
	s.cacheMutex.Lock()
	s.lastSavedCache = cacheBinary
	s.cacheMutex.Unlock()
	return nil
}

func (s *Service) saveCache() error {
	if s.cachePath == "" {
		return nil
	}
	cacheBinary, err := s.encodeCache()
	if err != nil {
		return err
	}
	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()
	if bytes.Equal(s.lastSavedCache, cacheBinary) {
		return nil
	}
	basePath := filemanager.BasePath(s.ctx, s.cachePath)
	if err = os.MkdirAll(filepath.Dir(basePath), 0o777); err != nil {
		return err
	}
	if err = os.WriteFile(basePath, cacheBinary, 0o644); err != nil {
		return err
	}
	s.lastSavedCache = cacheBinary
	return nil
}

func (s *Service) decodeCache(cacheBinary []byte) error {
	if len(cacheBinary) == 0 {
		return nil
	}
	cache, err := json.UnmarshalExtended[*Cache](cacheBinary)
	if err != nil {
		return err
	}
	if cache.Users == nil {
		return nil
	}
	for _, entry := range cache.Users.Entries() {
		key, err := parseUserKey(entry.Key)
		if err != nil {
			continue
		}
		state := s.manager.userState(key.InboundTag, key.UserName)
		if state == nil {
			continue
		}
		state.Uplink.Store(entry.Value.UplinkBytes)
		state.Downlink.Store(entry.Value.DownlinkBytes)
	}
	return nil
}

func (s *Service) encodeCache() ([]byte, error) {
	users := new(badjson.TypedMap[string, *UserCache])
	s.manager.access.RLock()
	keys := make([]UserKey, 0, len(s.manager.users))
	for k := range s.manager.users {
		keys = append(keys, k)
	}
	s.manager.access.RUnlock()
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].InboundTag != keys[j].InboundTag {
			return keys[i].InboundTag < keys[j].InboundTag
		}
		return keys[i].UserName < keys[j].UserName
	})
	for _, k := range keys {
		state := s.manager.userState(k.InboundTag, k.UserName)
		if state == nil {
			continue
		}
		users.Put(k.String(), &UserCache{
			UplinkBytes:   state.Uplink.Load(),
			DownlinkBytes: state.Downlink.Load(),
		})
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(&Cache{Users: users}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func parseUserKey(s string) (UserKey, error) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return UserKey{InboundTag: s[:i], UserName: s[i+1:]}, nil
		}
	}
	return UserKey{}, ErrInvalidUserKey
}

var ErrInvalidUserKey = errInvalidUserKey{}

type errInvalidUserKey struct{}

func (errInvalidUserKey) Error() string { return "invalid user key" }
