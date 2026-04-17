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
	Inbounds *badjson.TypedMap[string, *InboundCache] `json:"inbounds"`
}

type InboundCache struct {
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
	if cache.Inbounds == nil {
		return nil
	}
	for _, entry := range cache.Inbounds.Entries() {
		state := s.manager.state(entry.Key)
		if state == nil {
			continue
		}
		state.Uplink.Store(entry.Value.UplinkBytes)
		state.Downlink.Store(entry.Value.DownlinkBytes)
	}
	return nil
}

func (s *Service) encodeCache() ([]byte, error) {
	inbounds := new(badjson.TypedMap[string, *InboundCache])
	s.manager.access.RLock()
	keys := make([]string, 0, len(s.manager.inbounds))
	for tag := range s.manager.inbounds {
		keys = append(keys, tag)
	}
	s.manager.access.RUnlock()
	sort.Strings(keys)
	for _, tag := range keys {
		state := s.manager.state(tag)
		if state == nil {
			continue
		}
		inbounds.Put(tag, &InboundCache{
			UplinkBytes:   state.Uplink.Load(),
			DownlinkBytes: state.Downlink.Load(),
		})
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(&Cache{Inbounds: inbounds}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
