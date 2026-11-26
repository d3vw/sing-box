package quota

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
)

// The outbound tab manages a member's nodes under profilesPath; every
// endpoint answers JSON with the member's nodes as they now stand.
const (
	profilesPath   = "/outbound/profiles"
	maxProfileName = 40
)

type profilesView struct {
	OK       bool              `json:"ok"`
	Active   string            `json:"active"`
	Profiles []outboundProfile `json:"profiles"`
}

func newProfilesView(p *memberProfiles) profilesView {
	view := profilesView{OK: true, Active: p.Active, Profiles: p.Profiles}
	if view.Profiles == nil {
		view.Profiles = []outboundProfile{}
	}
	return view
}

// OverrideOutbound sends a member's traffic through their node in use,
// whatever outbound the route rules picked for it. Rejected, DNS and portal
// traffic is left alone, and members without a node in use keep the
// server's routing untouched.
func (h *portalOutbound) OverrideOutbound(metadata *adapter.InboundContext, outbound adapter.Outbound) adapter.Outbound {
	switch outbound.Type() {
	case C.TypeBlock, C.TypeDNS, C.TypeQuotaPortal, C.TypeSpeedtest, C.TypeSpeedtestPortal:
		return outbound
	}
	if val, ok := h.liveOutbounds.Load(liveOutboundKey(metadata.Inbound, metadata.User)); ok {
		return val.(adapter.Outbound)
	}
	return outbound
}

func (h *portalOutbound) handleProfiles(conn net.Conn, req *http.Request, inboundTag, userName string) {
	if h.memberOutboundConfigDirectory == "" {
		h.writeJSON(conn, http.StatusForbidden, map[string]string{"error": "Forbidden"})
		return
	}
	if h.manager.userState(inboundTag, userName) == nil {
		h.writeJSON(conn, http.StatusNotFound, map[string]string{"error": "User not found"})
		return
	}
	if err := req.ParseForm(); err != nil {
		h.writeJSON(conn, http.StatusBadRequest, map[string]string{"error": "Bad request"})
		return
	}
	switch strings.TrimPrefix(req.URL.Path, profilesPath) {
	case "/save":
		h.saveProfile(conn, req, inboundTag, userName)
	case "/delete":
		h.deleteProfile(conn, req, inboundTag, userName)
	case "/activate":
		h.activateProfile(conn, req, inboundTag, userName)
	case "/test":
		h.testProfile(conn, req, inboundTag, userName)
	default:
		h.writeJSON(conn, http.StatusNotFound, map[string]string{"error": "Not found"})
	}
}

// parseProfile reads a node from the form, or says what is wrong with it.
func parseProfile(req *http.Request) (outboundProfile, string) {
	profile := outboundProfile{
		Name:     strings.TrimSpace(req.Form.Get("name")),
		Server:   strings.TrimSpace(req.Form.Get("server")),
		Method:   strings.TrimSpace(req.Form.Get("method")),
		Password: req.Form.Get("password"),
		Mux:      req.Form.Get("mux") == "true" || req.Form.Get("mux") == "on",
		Padding:  req.Form.Get("padding") == "true" || req.Form.Get("padding") == "on",
	}
	port, err := strconv.Atoi(strings.TrimSpace(req.Form.Get("server_port")))
	if err != nil || profile.Server == "" || profile.Method == "" || profile.Password == "" || port < 1 || port > 65535 {
		return profile, "Invalid Shadowsocks outbound"
	}
	profile.ServerPort = port
	if profile.Name == "" {
		profile.Name = profile.Server
	}
	if utf8.RuneCountInString(profile.Name) > maxProfileName {
		return profile, "Name is too long"
	}
	return profile, ""
}

// use makes outbound (nil for the server's routing) carry the member's
// traffic, closes the one it replaces, and closes the member's open
// connections so they reconnect through it.
func (h *portalOutbound) use(inboundTag, userName string, outbound adapter.Outbound) {
	key := liveOutboundKey(inboundTag, userName)
	var old any
	var loaded bool
	if outbound == nil {
		old, loaded = h.liveOutbounds.LoadAndDelete(key)
	} else {
		old, loaded = h.liveOutbounds.Swap(key, outbound)
	}
	if loaded {
		if closer, ok := old.(io.Closer); ok {
			_ = closer.Close()
		}
	}
	h.manager.CloseUserActiveConnections(inboundTag, userName)
}

func (h *portalOutbound) saveProfile(conn net.Conn, req *http.Request, inboundTag, userName string) {
	profile, invalid := parseProfile(req)
	if invalid != "" {
		h.writeJSON(conn, http.StatusBadRequest, map[string]string{"error": invalid})
		return
	}
	h.profilesAccess.Lock()
	defer h.profilesAccess.Unlock()
	profiles, err := readMemberProfiles(h.memberOutboundConfigDirectory, inboundTag, userName)
	if err != nil {
		h.writeJSON(conn, http.StatusInternalServerError, map[string]string{"error": "Failed to read nodes: " + err.Error()})
		return
	}
	id := req.Form.Get("id")
	index := -1
	if id != "" {
		if index, _ = profiles.find(id); index < 0 {
			h.writeJSON(conn, http.StatusNotFound, map[string]string{"error": "Node not found"})
			return
		}
		profile.ID = id
	} else {
		if len(profiles.Profiles) >= maxProfiles {
			h.writeJSON(conn, http.StatusBadRequest, map[string]string{"error": "Too many nodes"})
			return
		}
		profile.ID = newProfileID()
	}
	// Building the outbound checks the parameters, the cipher above all.
	outbound, err := h.createProfileOutbound(inboundTag, userName, &profile)
	if err != nil {
		h.writeJSON(conn, http.StatusBadRequest, map[string]string{"error": "Invalid Shadowsocks outbound: " + err.Error()})
		return
	}
	active := profile.ID == profiles.Active
	if !active {
		if closer, ok := outbound.(io.Closer); ok {
			_ = closer.Close()
		}
	}
	if index >= 0 {
		profiles.Profiles[index] = profile
	} else {
		profiles.Profiles = append(profiles.Profiles, profile)
	}
	if err := writeMemberProfiles(h.memberOutboundConfigDirectory, profiles); err != nil {
		if closer, ok := outbound.(io.Closer); ok && active {
			_ = closer.Close()
		}
		h.writeJSON(conn, http.StatusInternalServerError, map[string]string{"error": "Failed to save: " + err.Error()})
		return
	}
	if active {
		h.use(inboundTag, userName, outbound)
	}
	h.writeJSON(conn, http.StatusOK, struct {
		profilesView
		ID string `json:"id"`
	}{newProfilesView(profiles), profile.ID})
}

func (h *portalOutbound) deleteProfile(conn net.Conn, req *http.Request, inboundTag, userName string) {
	h.profilesAccess.Lock()
	defer h.profilesAccess.Unlock()
	profiles, err := readMemberProfiles(h.memberOutboundConfigDirectory, inboundTag, userName)
	if err != nil {
		h.writeJSON(conn, http.StatusInternalServerError, map[string]string{"error": "Failed to read nodes: " + err.Error()})
		return
	}
	id := req.Form.Get("id")
	index, _ := profiles.find(id)
	if index < 0 {
		h.writeJSON(conn, http.StatusNotFound, map[string]string{"error": "Node not found"})
		return
	}
	profiles.Profiles = append(profiles.Profiles[:index], profiles.Profiles[index+1:]...)
	wasActive := profiles.Active == id
	if wasActive {
		profiles.Active = ""
	}
	if err := writeMemberProfiles(h.memberOutboundConfigDirectory, profiles); err != nil {
		h.writeJSON(conn, http.StatusInternalServerError, map[string]string{"error": "Failed to save: " + err.Error()})
		return
	}
	if wasActive {
		h.use(inboundTag, userName, nil)
	}
	h.writeJSON(conn, http.StatusOK, newProfilesView(profiles))
}

// activateProfile switches the member to a node, or back to the server's
// routing when id is empty.
func (h *portalOutbound) activateProfile(conn net.Conn, req *http.Request, inboundTag, userName string) {
	h.profilesAccess.Lock()
	defer h.profilesAccess.Unlock()
	profiles, err := readMemberProfiles(h.memberOutboundConfigDirectory, inboundTag, userName)
	if err != nil {
		h.writeJSON(conn, http.StatusInternalServerError, map[string]string{"error": "Failed to read nodes: " + err.Error()})
		return
	}
	id := req.Form.Get("id")
	var outbound adapter.Outbound
	if id != "" {
		_, profile := profiles.find(id)
		if profile == nil {
			h.writeJSON(conn, http.StatusNotFound, map[string]string{"error": "Node not found"})
			return
		}
		outbound, err = h.createProfileOutbound(inboundTag, userName, profile)
		if err != nil {
			h.writeJSON(conn, http.StatusBadRequest, map[string]string{"error": "Invalid Shadowsocks outbound: " + err.Error()})
			return
		}
	}
	profiles.Active = id
	if err := writeMemberProfiles(h.memberOutboundConfigDirectory, profiles); err != nil {
		if closer, ok := outbound.(io.Closer); ok {
			_ = closer.Close()
		}
		h.writeJSON(conn, http.StatusInternalServerError, map[string]string{"error": "Failed to save: " + err.Error()})
		return
	}
	// Switch before answering, so the page sees the switch done; the
	// request is the portal's own connection, which switching leaves open.
	h.use(inboundTag, userName, outbound)
	h.writeJSON(conn, http.StatusOK, newProfilesView(profiles))
}

// testProfile measures a node's delay: the one in the form when it names a
// server, else the saved node id.
func (h *portalOutbound) testProfile(conn net.Conn, req *http.Request, inboundTag, userName string) {
	var profile outboundProfile
	if req.Form.Get("server") != "" {
		var invalid string
		if profile, invalid = parseProfile(req); invalid != "" {
			h.writeJSON(conn, http.StatusBadRequest, map[string]string{"error": invalid})
			return
		}
	} else {
		h.profilesAccess.Lock()
		profiles, err := readMemberProfiles(h.memberOutboundConfigDirectory, inboundTag, userName)
		h.profilesAccess.Unlock()
		if err != nil {
			h.writeJSON(conn, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		_, saved := profiles.find(req.Form.Get("id"))
		if saved == nil {
			h.writeJSON(conn, http.StatusNotFound, map[string]string{"error": "Node not found"})
			return
		}
		profile = *saved
	}
	delay, err := h.testMemberShadowsocksOutbound(inboundTag, profile.Server, profile.ServerPort, profile.Method, profile.Password, profile.Mux, profile.Padding)
	if err != nil {
		h.writeJSON(conn, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.writeJSON(conn, http.StatusOK, map[string]any{"delay": delay})
}
