package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"flag"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
	"github.com/microcosm-cc/bluemonday"
)

func playtestChallenge(mgr *cmgr.Manager, args []string) int {
	parser := flag.NewFlagSet("playtest", flag.ExitOnError)
	updateUsage(parser, "<challenge>")
	port := parser.Int("port", 4242, "the `port` from which to serve the challenge")
	seed := parser.Int("seed", time.Now().Nanosecond(), "the random `seed` for the challenge")
	parser.Lookup("seed").DefValue = "random"
	flagFormat := parser.String("flag-format", "flag{%s}", "the `format-string` to use for the flag")
	parser.Parse(args)

	if parser.NArg() != 1 {
		parser.Usage()
		return USAGE_ERROR
	}

	cid := cmgr.ChallengeId(parser.Arg(0))

	iface, ok := os.LookupEnv(cmgr.IFACE_ENV)
	if !ok {
		iface = "0.0.0.0"
	}
	if iface == "0.0.0.0" {
		iface = "localhost" // Force the server to use a single interface
	}

	builds, err := mgr.Build(cid, []int{*seed}, *flagFormat)
	if err != nil {
		fmt.Printf("error creating build: %s\n", err)
		return RUNTIME_ERROR
	}
	bid := builds[0].Id
	defer mgr.Destroy(bid)

	iid, err := mgr.Start(bid)
	if err != nil {
		fmt.Printf("error creating instance: %s\n", err)
		return RUNTIME_ERROR
	}
	defer mgr.Stop(iid)

	fmt.Printf("challenge information available at: http://%s:%d/\n", iface, *port)
	return launchPortal(mgr, iface, *port, cid, bid, iid)
}

type playtestPage struct {
	Name        string
	Description template.HTML
	Details     template.HTML
	Hints       []template.HTML
}

var playtestPageTemplate = template.Must(template.New("playtest").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>cmgr playtest</title>
</head>
<body>
  <h1>{{.Name}}</h1>
  <h2>Description</h2>
  <div>{{.Description}}</div>
  <h2>Details</h2>
  <div>{{.Details}}</div>
  {{if .Hints}}
  <h2>Hints</h2>
  <ul>{{range .Hints}}<li>{{.}}</li>{{end}}</ul>
  {{end}}
  <h2>Submit Flag</h2>
  <form action="/submit" method="get">
    <label for="flag">Flag:</label>
    <input type="text" id="flag" name="flag">
    <input type="submit" value="Submit">
  </form>
</body>
</html>`))

func expandPlaytestText(
	value string,
	iface string,
	port int,
	build *cmgr.BuildMetadata,
	instance *cmgr.InstanceMetadata,
) template.HTML {
	artifactURL := fmt.Sprintf("http://%s:%d/artifact/$1", iface, port)
	value = urlRe.ReplaceAllString(value, artifactURL)
	value = serverRe.ReplaceAllString(value, iface)
	value = httpBaseRe.ReplaceAllString(value, fmt.Sprintf("http://%s", iface))
	for portRe.MatchString(value) {
		match := portRe.FindStringSubmatch(value)
		mappedPort, ok := instance.Ports[match[1]]
		replacement := ""
		if ok {
			replacement = fmt.Sprintf("%d", mappedPort)
		}
		value = strings.ReplaceAll(value, match[0], replacement)
	}
	for lookupRe.MatchString(value) {
		match := lookupRe.FindStringSubmatch(value)
		value = strings.ReplaceAll(value, match[0], build.LookupData[match[1]])
	}
	policy := bluemonday.UGCPolicy()
	return template.HTML(policy.Sanitize(value))
}

func launchPortal(mgr *cmgr.Manager, iface string, port int, cid cmgr.ChallengeId, bid cmgr.BuildId, iid cmgr.InstanceId) int {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		cMeta, err := mgr.GetChallengeMetadata(cid)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		bMeta, err := mgr.GetBuildMetadata(bid)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		iMeta, err := mgr.GetInstanceMetadata(iid)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		page := playtestPage{
			Name: cMeta.Name,
			Description: expandPlaytestText(
				cMeta.Description,
				iface,
				port,
				bMeta,
				iMeta,
			),
			Details: expandPlaytestText(cMeta.Details, iface, port, bMeta, iMeta),
		}
		for _, hint := range cMeta.Hints {
			page.Hints = append(
				page.Hints,
				expandPlaytestText(hint, iface, port, bMeta, iMeta),
			)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := playtestPageTemplate.Execute(w, page); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("/artifact/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		filename := strings.TrimPrefix(r.URL.Path, "/artifact/")
		if filename == "" || strings.Contains(filename, "/") ||
			!regexp.MustCompile("^"+filenamePattern+"$").MatchString(filename) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		artifactDir, ok := os.LookupEnv(cmgr.ARTIFACT_DIR_ENV)
		if !ok {
			artifactDir = "."
		}

		archiveFilename := fmt.Sprintf("%d.tar.gz", bid)
		artifactArchive := filepath.Join(artifactDir, archiveFilename)

		artifactsFile, err := os.Open(artifactArchive)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer artifactsFile.Close()

		artifactTar, err := gzip.NewReader(artifactsFile)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer artifactTar.Close()

		artifacts := tar.NewReader(artifactTar)

		var hdr *tar.Header
		for hdr, err = artifacts.Next(); err == nil; hdr, err = artifacts.Next() {
			if hdr.Name == filename {
				contentType := mime.TypeByExtension(filepath.Ext(filename))
				if contentType == "" {
					contentType = "application/octet-stream"
				}
				w.Header().Set("Content-Type", contentType)
				_, err = io.Copy(w, artifacts)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})

	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		submittedFlag := strings.TrimSpace(r.URL.Query().Get("flag"))
		if submittedFlag == "" {
			http.Error(w, "missing flag", http.StatusBadRequest)
			return
		}
		bMeta, err := mgr.GetBuildMetadata(bid)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var body []byte
		if submittedFlag == bMeta.Flag {
			body = []byte("Correct")
		} else {
			body = []byte("That is not the correct flag")
		}
		w.Write(body)
	})

	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", iface, port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 * 1024,
	}
	signalContext, stopSignals := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stopSignals()
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case <-signalContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			fmt.Printf("error shutting down playtest portal: %s\n", err)
			return RUNTIME_ERROR
		}
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			fmt.Printf("error serving playtest portal: %s\n", err)
			return RUNTIME_ERROR
		}
	}
	if err := server.Close(); err != nil && err != http.ErrServerClosed {
		return RUNTIME_ERROR
	}
	return NO_ERROR
}

const filenamePattern string = "[a-zA-Z0-9_.-]+"
const displayTextPattern string = `[^<>'"]+`

// {{url("file")}}
const urlRePattern string = `\{\{\s*url\(["'](` + filenamePattern + `)["']\)\s*\}\}`

var urlRe *regexp.Regexp = regexp.MustCompile(urlRePattern)

// {{http_base("port_name")}}
var httpBaseRe *regexp.Regexp = regexp.MustCompile(`\{\{\s*http_base\(["'](\w+)["']\)\s*\}\}`)

// {{port("port_name")}}
var portRe *regexp.Regexp = regexp.MustCompile(`\{\{\s*port\(["'](\w+)["']\)\s*\}\}`)

// {{server("port_name")}}
var serverRe *regexp.Regexp = regexp.MustCompile(`\{\{\s*server\(["'](\w+)["']\)\s*\}\}`)

// {{lookup("key")}}
var lookupRe *regexp.Regexp = regexp.MustCompile(`\{\{\s*lookup\(["'](\w+)["']\)\s*\}\}`)
