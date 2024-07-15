package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
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
	"github.com/vtpl1/vrtc/internal/app"
)

var basePath string
var log zerolog.Logger
var Handler http.Handler
var Port int
var mu sync.Mutex

const (
	MimeJSON = "application/json"
	MimeText = "text/plain"
)

func Init(ctx *context.Context) {
	var cfg struct {
		Mod struct {
			Listen     string `yaml:"listen"`
			Username   string `yaml:"username"`
			Password   string `yaml:"password"`
			BasePath   string `yaml:"base_path"`
			StaticDir  string `yaml:"static_dir"`
			Origin     string `yaml:"origin"`
			TLSListen  string `yaml:"tls_listen"`
			TLSCert    string `yaml:"tls_cert"`
			TLSKey     string `yaml:"tls_key"`
			UnixListen string `yaml:"unix_listen"`
		} `yaml:"api"`
	}

	// default config
	cfg.Mod.Listen = ":1984"

	// load config from YAML
	app.LoadConfig(&cfg)

	if cfg.Mod.Listen == "" && cfg.Mod.UnixListen == "" && cfg.Mod.TLSListen == "" {
		return
	}

	basePath = cfg.Mod.BasePath
	log = app.GetLogger("api")

	initStatic(cfg.Mod.StaticDir)

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
		go listen(ctx, "tcp", cfg.Mod.Listen)
	}

	if cfg.Mod.UnixListen != "" {
		_ = syscall.Unlink(cfg.Mod.UnixListen)
		go listen(ctx, "unix", cfg.Mod.UnixListen)
	}

	// Initialize the HTTPS server
	if cfg.Mod.TLSListen != "" && cfg.Mod.TLSCert != "" && cfg.Mod.TLSKey != "" {
		go tlsListen(ctx, "tcp", cfg.Mod.TLSListen, cfg.Mod.TLSCert, cfg.Mod.TLSKey)
	}
}

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

func listen(ctx *context.Context, network, address string) {
	ln, err := net.Listen(network, address)
	if err != nil {
		log.Error().Err(err).Msg("[api] listen")
		return
	}

	log.Info().Str("addr", address).Msg("[api] listen")

	if network == "tcp" {
		Port = ln.Addr().(*net.TCPAddr).Port
	}

	server := &http.Server{
		BaseContext:       func(net.Listener) context.Context { return *ctx },
		Handler:           Handler,
		ReadHeaderTimeout: 5 * time.Second, // Example: Set to 5 seconds
	}
	if err = server.Serve(ln); err != nil {
		log.Fatal().Err(err).Msg("[api] serve")
	}
}

func tlsListen(ctx *context.Context, network, address, certFile, keyFile string) {
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
		BaseContext:       func(net.Listener) context.Context { return *ctx },
		Handler:           Handler,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}},
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err = server.ServeTLS(ln, "", ""); err != nil {
		log.Fatal().Err(err).Msg("[api] tls serve")
	}
}

func middlewareCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		next.ServeHTTP(w, r)
	})
}

func middlewareAuth(username, password string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.RemoteAddr, "127.") && !strings.HasPrefix(r.RemoteAddr, "[::1]") && r.RemoteAddr != "@" {
			user, pass, ok := r.BasicAuth()
			if !ok || user != username || pass != password {

				w.Header().Set("Www-Authenticate", fmt.Sprintf(`Basic realm="%s"`, app.AppName))
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func middlewareLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Trace().Msgf("[api] %s %s %s", r.Method, r.URL, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

// ResponseJSON important always add Content-Type
// so go won't need to call http.DetectContentType
func ResponseJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", MimeJSON)
	_ = json.NewEncoder(w).Encode(v)
}

func ResponsePrettyJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", MimeJSON)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

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

func apiHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	app.Info["host"] = r.Host
	mu.Unlock()

	ResponseJSON(w, app.Info)
}

func exitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
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
	_, cancel := context.WithCancel(r.Context())
	cancel()

	// os.Exit(code)
}

func restartHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	path, err := os.Executable()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Debug().Msgf("[api] restart %s", path)

	go syscall.Exec(path, os.Args, os.Environ())
}

func logHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		// Send current state of the log file immediately
		w.Header().Set("Content-Type", "application/jsonlines")
		_, _ = app.MemoryLog.WriteTo(w)
	case "DELETE":
		app.MemoryLog.Reset()
		Response(w, "OK", "text/plain")
	default:
		http.Error(w, "Method not allowed", http.StatusBadRequest)
	}
}
