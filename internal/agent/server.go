package agent

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/luna-page/luna/pkg/sysinfo"
)

var cachedSystemInfo struct {
	json       []byte
	mu         sync.Mutex
	lastUpdate time.Time
}

func serve(config *config) error {
	authorizationValue := []byte("Bearer " + config.Server.Token)

	isAuthorized := func(r *http.Request, w http.ResponseWriter) bool {
		if config.Server.Token != "" &&
			subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), authorizationValue) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return false
		}

		return true
	}

	mux := http.NewServeMux()

	// Unversioned, no backwards compatibility guarantees for now
	mux.HandleFunc("/api/sysinfo/all", func(w http.ResponseWriter, r *http.Request) {
		if !isAuthorized(r, w) {
			return
		}

		cachedSystemInfo.mu.Lock()
		defer cachedSystemInfo.mu.Unlock()

		if time.Since(cachedSystemInfo.lastUpdate) > 1*time.Second {
			info, errs := sysinfo.Collect(config.SystemInfoRequest)
			// Behind logDebug in the event that this gets called every second
			// and there are a lot of errors, it could get very spammy
			if logDebug {
				for _, err := range errs {
					slog.Debug("Error while collecting system info", "error", err)
				}
			}

			infoAsJson, err := json.Marshal(info)
			if err != nil {
				slog.Error("Could not marshal sysinfo/all request", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			cachedSystemInfo.json = infoAsJson
			cachedSystemInfo.lastUpdate = time.Now()
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(cachedSystemInfo.json)
	})

	mux.HandleFunc("/api/healthz", func(w http.ResponseWriter, r *http.Request) {
		if !isAuthorized(r, w) {
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	server := http.Server{
		Addr:    fmt.Sprintf("%s:%d", config.Server.Host, config.Server.Port),
		Handler: mux,
	}

	slog.Info("Starting server", "host", config.Server.Host, "port", config.Server.Port)
	return server.ListenAndServe()
}
