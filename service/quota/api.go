package quota

import (
	"net/http"
	"strconv"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/logger"
	sHTTP "github.com/sagernet/sing/protocol/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

type APIServer struct {
	logger  logger.Logger
	manager *Manager
	history *HistoryStore
}

func NewAPIServer(logger logger.Logger, manager *Manager, history *HistoryStore) *APIServer {
	return &APIServer{logger: logger, manager: manager, history: history}
}

func (s *APIServer) Route(r chi.Router) {
	r.Route("/quota/v1", func(r chi.Router) {
		r.Use(func(handler http.Handler) http.Handler {
			return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				s.logger.Debug(request.Method, " ", request.RequestURI, " ", sHTTP.SourceAddress(request))
				handler.ServeHTTP(writer, request)
			})
		})
		r.Get("/", s.getInfo)
		r.Get("/users", s.listUsers)
		r.Get("/inbounds/{tag}/users/{name}", s.getUser)
		r.Post("/inbounds/{tag}/users/{name}/reset", s.resetUser)
		r.Get("/history", s.getHistory)
		r.Get("/history/top", s.getHistoryTop)
		r.Get("/history/forecast", s.getForecast)
	})
}

func (s *APIServer) getInfo(writer http.ResponseWriter, request *http.Request) {
	render.JSON(writer, request, render.M{
		"server":     "sing-box " + C.Version,
		"apiVersion": "v1",
	})
}

func (s *APIServer) listUsers(writer http.ResponseWriter, request *http.Request) {
	render.JSON(writer, request, render.M{
		"users": s.manager.Snapshots(),
	})
}

func (s *APIServer) getUser(writer http.ResponseWriter, request *http.Request) {
	tag := chi.URLParam(request, "tag")
	name := chi.URLParam(request, "name")
	snapshot, loaded := s.manager.Snapshot(tag, name)
	if !loaded {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	render.JSON(writer, request, snapshot)
}

func (s *APIServer) historyQuery(request *http.Request) (HistoryQuery, error) {
	now := time.Now().UTC()
	q := HistoryQuery{From: now.AddDate(0, 0, -30), To: now.Add(time.Hour), Granularity: request.URL.Query().Get("granularity")}
	values := request.URL.Query()
	if raw := values.Get("from"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return q, err
		}
		q.From = time.Unix(value, 0)
	}
	if raw := values.Get("to"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return q, err
		}
		q.To = time.Unix(value, 0)
	}
	q.InboundTag = values.Get("inbound_tag")
	q.UserName = values.Get("user_name")
	q.InboundType = values.Get("inbound_type")
	q.Protocol = values.Get("protocol")
	q.Target = values.Get("target")
	return q, nil
}

func (s *APIServer) getHistory(writer http.ResponseWriter, request *http.Request) {
	if s.history == nil {
		http.Error(writer, "history is disabled", http.StatusNotFound)
		return
	}
	q, err := s.historyQuery(request)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	points, err := s.history.Query(request.Context(), q)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	render.JSON(writer, request, render.M{"points": points})
}

func (s *APIServer) getHistoryTop(writer http.ResponseWriter, request *http.Request) {
	if s.history == nil {
		http.Error(writer, "history is disabled", http.StatusNotFound)
		return
	}
	q, err := s.historyQuery(request)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	points, err := s.history.Top(request.Context(), q, request.URL.Query().Get("dimension"), limit)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	render.JSON(writer, request, render.M{"items": points})
}

func (s *APIServer) getForecast(writer http.ResponseWriter, request *http.Request) {
	if s.history == nil {
		http.Error(writer, "history is disabled", http.StatusNotFound)
		return
	}
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -30)
	forecasts := make([]render.M, 0)
	for _, user := range s.manager.Snapshots() {
		points, err := s.history.Query(request.Context(), HistoryQuery{From: from, To: now.Add(time.Hour), InboundTag: user.InboundTag, UserName: user.UserName})
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		var used int64
		for _, p := range points {
			used += p.UsedBytes
		}
		days := now.Sub(from).Hours() / 24
		daily := float64(used) / days
		var exhaustedAt any
		if daily > 0 && user.RemainingBytes > 0 {
			exhaustedAt = now.Add(time.Duration(float64(user.RemainingBytes) / daily * 24 * float64(time.Hour))).Unix()
		}
		forecasts = append(forecasts, render.M{"inbound_tag": user.InboundTag, "user_name": user.UserName, "average_daily_bytes": int64(daily), "estimated_exhausted_at": exhaustedAt})
	}
	render.JSON(writer, request, render.M{"users": forecasts})
}

func (s *APIServer) resetUser(writer http.ResponseWriter, request *http.Request) {
	tag := chi.URLParam(request, "tag")
	name := chi.URLParam(request, "name")
	if !s.manager.Reset(tag, name) {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}
