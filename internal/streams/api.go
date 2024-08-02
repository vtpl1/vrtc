package streams

import (
	"encoding/json"
	"net/http"

	"github.com/vtpl1/vrtc/internal/api"
	"github.com/vtpl1/vrtc/pkg/probe"
	"github.com/vtpl1/vrtc/utils"
)

func apiStreams(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	src := query.Get("src")

	// without source - return all streams list
	if src == "" && r.Method != http.MethodPost {
		w.Header().Set("Content-Type", api.MimeJSON)
		body, err := json.Marshal(streams)
		if err != nil {
			log.Error().Err(err).Send()
			http.Error(w, "Unable to marshal", http.StatusInternalServerError)
		}
		_, err = w.Write(body)
		if err != nil {
			log.Error().Err(err).Send()
		}
		return
	}

	// Not sure about all this API. Should be rewrited...
	switch r.Method {
	case "GET":
		stream := Get(src)
		if stream == nil {
			http.Error(w, "", http.StatusNotFound)
			return
		}

		cons := probe.NewProbe(query)
		if len(cons.Medias) != 0 {
			cons.WithRequest(r)
			if err := stream.AddConsumer(cons); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", api.MimeJSON)
			body, err := json.Marshal(stream)
			if err != nil {
				log.Error().Err(err).Send()
				http.Error(w, "Unable to marshal", http.StatusInternalServerError)
			}
			_, err = w.Write(body)
			if err != nil {
				log.Error().Err(err).Send()
			}
			// api.ResponsePrettyJSON(w, stream)

			stream.RemoveConsumer(cons)
		} else {
			w.Header().Set("Content-Type", api.MimeJSON)
			body, err := json.Marshal(streams[src])
			if err != nil {
				log.Error().Err(err).Send()
				http.Error(w, "Unable to marshal", http.StatusInternalServerError)
			}
			_, err = w.Write(body)
			if err != nil {
				log.Error().Err(err).Send()
			}

			// api.ResponsePrettyJSON(w, streams[src])
		}

	case "PUT":
		name := query.Get("name")
		if name == "" {
			name = src
		}

		if New(name, src) == nil {
			http.Error(w, "", http.StatusBadRequest)
			return
		}

		if err := utils.PatchConfig(name, src, "streams"); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}

	case "PATCH":
		name := query.Get("name")
		if name == "" {
			http.Error(w, "", http.StatusBadRequest)
			return
		}

		// support {input} templates: https://github.com/AlexxIT/go2rtc#module-hass
		if Patch(name, src) == nil {
			http.Error(w, "", http.StatusBadRequest)
		}

	case "POST":
		// with dst - redirect source to dst
		if dst := query.Get("dst"); dst != "" {
			if stream := Get(dst); stream != nil {
				if err := Validate(src); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
				} else if err = stream.Play(src); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				} else {
					w.Header().Set("Content-Type", api.MimeJSON)
					body, err := json.Marshal(stream)
					if err != nil {
						log.Error().Err(err).Send()
						http.Error(w, "Unable to marshal", http.StatusInternalServerError)
					}
					_, err = w.Write(body)
					if err != nil {
						log.Error().Err(err).Send()
					}

				}
			} else if stream = Get(src); stream != nil {
				if err := Validate(dst); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
				} else if err = stream.Publish(dst); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			} else {
				http.Error(w, "", http.StatusNotFound)
			}
		} else {
			http.Error(w, "", http.StatusBadRequest)
		}

	case "DELETE":
		delete(streams, src)

		if err := utils.PatchConfig(src, nil, "streams"); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
	}
}

func apiStreamsDOT(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	dot := make([]byte, 0, 1024)
	dot = append(dot, "digraph {\n"...)
	if query.Has("src") {
		for _, name := range query["src"] {
			if stream := streams[name]; stream != nil {
				dot = AppendDOT(dot, stream)
			}
		}
	} else {
		for _, stream := range streams {
			dot = AppendDOT(dot, stream)
		}
	}
	dot = append(dot, '}')

	api.Response(w, dot, "text/vnd.graphviz")
}
