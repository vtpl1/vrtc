// Package api holds utility functions
package api

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/vtpl1/vrtc/utils"
)

var errListenPortNotDefined = errors.New("listen port not defined")

// Init is the entrypoint for the api
func Init() error {
	var cfg struct {
		Mod struct {
			Listen     string `yaml:"listen"`
			Username   string `yaml:"username"`
			Password   string `yaml:"password"`
			BasePath   string `yaml:"basePath"`
			StaticDir  string `yaml:"staticDir"`
			Origin     string `yaml:"origin"`
			TLSListen  string `yaml:"tlsListen"`
			TLSCert    string `yaml:"tlsCert"`
			TLSKey     string `yaml:"tlsKey"`
			UnixListen string `yaml:"unixListen"`
		} `yaml:"api"`
	}

	// default config
	cfg.Mod.Listen = ":1984"

	// load config from YAML
	utils.LoadConfig(&cfg)

	if cfg.Mod.Listen == "" && cfg.Mod.UnixListen == "" && cfg.Mod.TLSListen == "" {
		return errListenPortNotDefined
	}

	basePath = cfg.Mod.BasePath
	log = utils.GetLogger("api")

	err := initStatic(cfg.Mod.StaticDir)
	if err != nil {
		return err
	}

	HandleFunc("api", apiHandler)
	HandleFunc("api/config", configHandler)
	HandleFunc("api/exit", exitHandler)
	HandleFunc("api/restart", restartHandler)
	HandleFunc("api/log", logHandler)

	Handler = http.DefaultServeMux // 4th

	if cfg.Mod.Origin == "*" {
		Handler = middlewareCORS(Handler) // 3rd
	}

	if cfg.Mod.Username != "" {
		Handler = middlewareAuth(cfg.Mod.Username, cfg.Mod.Password, Handler) // 2nd
	}

	if log.Trace().Enabled() {
		Handler = middlewareLog(Handler) // 1st
	}

	if cfg.Mod.Listen != "" {
		go listen("tcp", cfg.Mod.Listen)
	}

	if cfg.Mod.UnixListen != "" {
		_ = syscall.Unlink(cfg.Mod.UnixListen)
		go listen("unix", cfg.Mod.UnixListen)
	}

	// Initialize the HTTPS server
	if cfg.Mod.TLSListen != "" && cfg.Mod.TLSCert != "" && cfg.Mod.TLSKey != "" {
		go tlsListen("tcp", cfg.Mod.TLSListen, cfg.Mod.TLSCert, cfg.Mod.TLSKey)
	}
	return nil
}

func listen(network, address string) {
	ln, err := net.Listen(network, address)
	if err != nil {
		log.Error().Err(err).Msg("[api] listen")
		utils.InternalTerminationRequest <- 1
		return
	}

	log.Info().Str("addr", address).Msg("[api] listen")

	if network == "tcp" {
		tcpPort, ok := ln.Addr().(*net.TCPAddr)
		if ok {
			Port = tcpPort.Port
		}
	}

	server := http.Server{
		Handler:           Handler,
		ReadHeaderTimeout: 5 * time.Second, // Example: Set to 5 seconds
	}
	if err = server.Serve(ln); err != nil {
		log.Error().Err(err).Msg("[api] serve")
		utils.InternalTerminationRequest <- 1
		return
	}
}

func tlsListen(network, address, certFile, keyFile string) {
	var cert tls.Certificate
	var err error
	if strings.IndexByte(certFile, '\n') < 0 && strings.IndexByte(keyFile, '\n') < 0 {
		// check if file path
		cert, err = tls.LoadX509KeyPair(certFile, keyFile)
	} else {
		// if text file content
		cert, err = tls.X509KeyPair([]byte(certFile), []byte(keyFile))
	}
	if err != nil {
		log.Error().Err(err).Caller().Send()
		return
	}

	ln, err := net.Listen(network, address)
	if err != nil {
		log.Error().Err(err).Msg("[api] tls listen")
		return
	}

	log.Info().Str("addr", address).Msg("[api] tls listen")

	server := &http.Server{
		Handler:           Handler,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}},
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err = server.ServeTLS(ln, "", ""); err != nil {
		log.Error().Err(err).Msg("[api] tls serve")
		utils.InternalTerminationRequest <- 1
	}
}

// Port holds the http port number
var Port int

const (
	MimeJSON = "application/json" // MimeJSON global mime value
	MimeText = "text/plain"       // MimeText global mime value
)

// Handler hold global http handlers
var Handler http.Handler

// HandleFunc handle pattern with relative path:
// - "api/streams" => "{basepath}/api/streams"
// - "/streams"    => "/streams"
func HandleFunc(pattern string, handler http.HandlerFunc) {
	if len(pattern) == 0 || pattern[0] != '/' {
		pattern = basePath + "/" + pattern
	}
	log.Trace().Str("path", pattern).Msg("[api] register path")
	http.HandleFunc(pattern, handler)
}

// // ResponseJSON important always add Content-Type
// // so go won't need to call http.DetectContentType
// func ResponseJSON(w http.ResponseWriter, v map[string]string) {
// 	w.Header().Set("Content-Type", MimeJSON)
// 	body, _ := json.Marshal(v)
// 	// _ = json.NewEncoder(w).Encode(v)
// 	w.Write(body)
// }

// func ResponsePrettyJSON(w http.ResponseWriter, v any) {
// 	w.Header().Set("Content-Type", MimeJSON)
// 	enc := json.NewEncoder(w)
// 	enc.SetIndent("", "  ")
// 	_ = enc.Encode(v)
// }

// Response is a generic response writer
func Response(w http.ResponseWriter, body any, contentType string) {
	w.Header().Set("Content-Type", contentType)

	switch v := body.(type) {
	case []byte:
		_, _ = w.Write(v)
	case string:
		_, _ = w.Write([]byte(v))
	default:
		_, _ = fmt.Fprint(w, body)
	}
}

// StreamNotFound global string
const StreamNotFound = "stream not found"

var (
	basePath string
	log      zerolog.Logger
)

func middlewareLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Trace().Msgf("[api] %s %s %s", r.Method, r.URL, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

func middlewareAuth(username, password string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.RemoteAddr, "127.") && !strings.HasPrefix(r.RemoteAddr, "[::1]") && r.RemoteAddr != "@" {
			user, pass, ok := r.BasicAuth()
			if !ok || user != username || pass != password {
				w.Header().Set("Www-Authenticate", `Basic realm="vrtc"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func middlewareCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		next.ServeHTTP(w, r)
	})
}

var mu sync.Mutex

func apiHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	utils.Info["host"] = r.Host
	mu.Unlock()
	w.Header().Set("Content-Type", MimeJSON)
	body, err := json.Marshal(utils.Info)
	if err != nil {
		log.Error().Err(err).Send()
		http.Error(w, "Unable to marshal", http.StatusInternalServerError)
	}
	_, err = w.Write(body)
	if err != nil {
		log.Error().Err(err).Send()
	}

	// ResponseJSON(w, utils.Info)
}

func exitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	s := r.URL.Query().Get("code")
	code, err := strconv.Atoi(s)

	// https://pubs.opengroup.org/onlinepubs/9699919799/utilities/V3_chap02.html#tag_18_08_02
	if err != nil || code < 0 || code > 125 {
		http.Error(w, "Code must be in the range [0, 125]", http.StatusBadRequest)
		return
	}
	path, err := os.Executable()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Info().Msgf("[api] exit %s", path)
	utils.InternalTerminationRequest <- 1
	// os.Exit(code)
}

func restartHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	path, err := os.Executable()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Debug().Msgf("[api] restart %s", path)

	go func() {
		err := syscall.Exec(path, os.Args, os.Environ())
		log.Error().Err(err).Msg("Restart failed")
	}()
	utils.InternalTerminationRequest <- 1
}

func logHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		// Send current state of the log file immediately
		w.Header().Set("Content-Type", "application/jsonlines")
		_, _ = utils.MemoryLog.WriteTo(w)
	case "DELETE":
		utils.MemoryLog.Reset()
		Response(w, "OK", "text/plain")
	default:
		http.Error(w, "Method not allowed", http.StatusBadRequest)
	}
}

// Source structure
type Source struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Info     string `json:"info,omitempty"`
	URL      string `json:"url,omitempty"`
	Location string `json:"location,omitempty"`
}

// // ResponseSources writes sources over http
// func ResponseSources(w http.ResponseWriter, sources []*Source) {
// 	if len(sources) == 0 {
// 		http.Error(w, "no sources", http.StatusNotFound)
// 		return
// 	}

// 	response := struct {
// 		Sources []*Source `json:"sources"`
// 	}{
// 		Sources: sources,
// 	}
// 	body, _ := json.Marshal(response)
// 	w.Write(body)
// 	// ResponseJSON(w, response)
// }

// Error writes error over http
func Error(w http.ResponseWriter, err error) {
	log.Error().Err(err).Caller(1).Send()

	http.Error(w, err.Error(), http.StatusInsufficientStorage)
}
