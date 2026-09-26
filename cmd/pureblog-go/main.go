// Command pureblog-go runs a PureBlog-Go site.
//
//	pureblog-go -dir /path/to/site -addr :8080
//
// The site directory holds content/, config/, data/ and cache/. Pointing it
// at an existing PHP Pure Blog installation works: config/config.php is
// imported into config/config.json on first start.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/johanvanrhyn/pureblog-go/internal/blog"
	"github.com/johanvanrhyn/pureblog-go/internal/server"
	"github.com/johanvanrhyn/pureblog-go/web"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = "v0.1.0"

func main() {
	addr := flag.String("addr", envOr("PUREBLOG_GO_ADDR", ":8080"), "listen address")
	dir := flag.String("dir", envOr("PUREBLOG_GO_DIR", "."), "site directory (contains content/, config/, data/)")
	releaseRepo := flag.String("release-repo", envOr("PUREBLOG_GO_RELEASE_REPO", ""), "GitHub owner/repo to check for new releases (empty disables)")
	trustProxy := flag.Bool("trust-proxy", os.Getenv("PUREBLOG_GO_TRUST_PROXY") == "1", "trust X-Forwarded-* headers from a reverse proxy")
	maxUpload := flag.Int64("max-upload", 20, "maximum upload size in MB")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		os.Stdout.WriteString(version + "\n")
		return
	}

	site, err := blog.Open(*dir, web.FS, version)
	if err != nil {
		log.Fatalf("opening site: %v", err)
	}
	srv, err := server.New(site, web.FS, server.Options{
		ReleaseRepo: *releaseRepo,
		TrustProxy:  *trustProxy,
		MaxUploadMB: *maxUpload,
	})
	if err != nil {
		log.Fatalf("starting server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv.StartScheduler(ctx)

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()

	log.Printf("PureBlog-Go %s serving %s on %s", version, site.Dir, *addr)
	if !site.Config.Installed() {
		host := *addr
		if strings.HasPrefix(host, ":") {
			host = "localhost" + host
		}
		log.Printf("no admin account yet: open http://%s/setup to finish setup", host)
	}
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
