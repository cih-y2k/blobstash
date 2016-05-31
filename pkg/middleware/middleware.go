package middleware

import (
	"net/http"
	"os"
	"strconv"

	_ "github.com/carbocation/interpose/middleware"
	"github.com/tsileo/blobstash/httputil"
	"github.com/unrolled/secure"
)

func Secure(h http.Handler) http.Handler {
	// FIXME allowedorigins from config
	isDevelopment, _ := strconv.ParseBool(os.Getenv("BLOBSTASH_DEV_MODE"))
	// if isDevelopment {
	// 	s.Log.Info("Server started in development mode")
	// }
	secureOptions := secure.Options{
		FrameDeny:             true,
		ContentTypeNosniff:    true,
		BrowserXssFilter:      true,
		ContentSecurityPolicy: "default-src 'self'",
		IsDevelopment:         isDevelopment,
	}
	// var tlsHostname string
	// if tlsHost, ok := s.conf["tls-hostname"]; ok {
	// 	tlsHostname = tlsHost.(string)
	// 	secureOptions.AllowedHosts = []string{tlsHostname}
	// }
	return secure.New(secureOptions).Handler(h)
}

func BasicAuth(h http.Handler) http.Handler {
	// FIXME(tsileo): clean this, and load passfrom config
	authFunc := httputil.BasicAuthFunc("", "123")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if authFunc(r) {
				next.ServeHTTP(w, r)
				return
			}
			httputil.WriteJSONError(w, http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized))
		})
	}(h)
}