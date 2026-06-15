package quota

import (
	"net/http"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/logger"
	sHTTP "github.com/sagernet/sing/protocol/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

type APIServer struct {
	logger  logger.Logger
	manager *Manager
}

func NewAPIServer(logger logger.Logger, manager *Manager) *APIServer {
	return &APIServer{logger: logger, manager: manager}
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

func (s *APIServer) resetUser(writer http.ResponseWriter, request *http.Request) {
	tag := chi.URLParam(request, "tag")
	name := chi.URLParam(request, "name")
	if !s.manager.Reset(tag, name) {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}
