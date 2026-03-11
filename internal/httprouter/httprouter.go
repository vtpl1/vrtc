package httprouter

import "github.com/go-chi/chi/v5"

func NewRouter() *chi.Mux {
	r := chi.NewRouter()

	return r
}
