package quota

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// A member picks how their traffic leaves the server: by default the
// server's own route rules decide (nothing stored, nothing overridden), or
// one of the Shadowsocks nodes they saved replaces whatever outbound those
// rules picked. The choice and the nodes live in one file per member.

// maxProfiles bounds how many nodes one member can save.
const maxProfiles = 20

type outboundProfile struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Method     string `json:"method"`
	Password   string `json:"password"`
	Mux        bool   `json:"mux,omitempty"`
	Padding    bool   `json:"padding,omitempty"`
}

// memberProfiles is a member's saved nodes; Active is the ID of the one in
// use, empty for the server's default routing.
type memberProfiles struct {
	InboundTag string            `json:"inbound_tag"`
	UserName   string            `json:"user_name"`
	Active     string            `json:"active"`
	Profiles   []outboundProfile `json:"profiles"`
}

func (p *memberProfiles) find(id string) (int, *outboundProfile) {
	if id == "" {
		return -1, nil
	}
	for i := range p.Profiles {
		if p.Profiles[i].ID == id {
			return i, &p.Profiles[i]
		}
	}
	return -1, nil
}

func (p *memberProfiles) active() *outboundProfile {
	_, profile := p.find(p.Active)
	return profile
}

func newProfileID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// memberProfilesName must not end in .json: the member directory is
// usually the config directory, where sing-box reads every .json file as
// part of its config.
func memberProfilesName(inboundTag, userName string) string {
	return "quota-member-" + safeConfigName(inboundTag) + "-" + safeConfigName(userName) + ".profiles"
}

// readMemberProfiles returns the member's nodes, an empty set when they
// have none yet.
func readMemberProfiles(directory, inboundTag, userName string) (*memberProfiles, error) {
	profiles := &memberProfiles{InboundTag: inboundTag, UserName: userName}
	data, err := os.ReadFile(filepath.Join(directory, memberProfilesName(inboundTag, userName)))
	if os.IsNotExist(err) {
		return profiles, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, profiles); err != nil {
		return nil, err
	}
	if profiles.active() == nil {
		profiles.Active = ""
	}
	return profiles, nil
}

func writeMemberProfiles(directory string, profiles *memberProfiles) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(directory, memberProfilesName(profiles.InboundTag, profiles.UserName))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// memberSSConfig is the single custom outbound a member could save before
// profiles, kept to migrate from.
type memberSSConfig struct {
	InboundTag string `json:"inbound_tag"`
	UserName   string `json:"user_name"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Method     string `json:"method"`
	Password   string `json:"password"`
	Mux        bool   `json:"mux,omitempty"`
	Padding    bool   `json:"padding,omitempty"`
}

// migrateMemberOutbounds turns each old single custom outbound (a .quota
// file routed by a 00-quota-member-*.json rule) into a member with one node,
// in use, and removes the old files. The running config may still hold the
// old route rule until the next reload; the portal keeps serving such
// connections through the member's node.
func migrateMemberOutbounds(directory string) (migrated []string, err error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	// An early build named the node files .profiles.json, which sing-box
	// then failed to read as config.
	for _, entry := range entries {
		if name := entry.Name(); !entry.IsDir() && strings.HasPrefix(name, "quota-member-") && strings.HasSuffix(name, ".profiles.json") {
			_ = os.Rename(filepath.Join(directory, name), filepath.Join(directory, strings.TrimSuffix(name, ".json")))
		}
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "quota-member-") || !strings.HasSuffix(entry.Name(), ".quota") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg memberSSConfig
		if json.Unmarshal(data, &cfg) != nil || cfg.InboundTag == "" || cfg.Server == "" {
			continue
		}
		profiles, err := readMemberProfiles(directory, cfg.InboundTag, cfg.UserName)
		if err != nil {
			continue
		}
		if len(profiles.Profiles) == 0 {
			profile := outboundProfile{
				ID: newProfileID(), Name: cfg.Server,
				Server: cfg.Server, ServerPort: cfg.ServerPort, Method: cfg.Method, Password: cfg.Password,
				Mux: cfg.Mux, Padding: cfg.Padding,
			}
			profiles.Profiles = append(profiles.Profiles, profile)
			profiles.Active = profile.ID
			if err := writeMemberProfiles(directory, profiles); err != nil {
				return migrated, err
			}
		}
		_ = os.Remove(path)
		for _, name := range []string{
			"00-quota-member-" + safeConfigName(cfg.InboundTag) + "-" + safeConfigName(cfg.UserName) + ".json",
			"zz-quota-member-" + safeConfigName(cfg.InboundTag) + "-" + safeConfigName(cfg.UserName) + ".json",
		} {
			_ = os.Remove(filepath.Join(directory, name))
		}
		migrated = append(migrated, cfg.InboundTag+"/"+cfg.UserName)
	}
	return migrated, nil
}
