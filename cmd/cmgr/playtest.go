package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func playtestChallenge(mgr *cmgr.Manager, args []string) int {
	if len(args) != 1 {
		fmt.Println("error: too many arguments")
		return USAGE_ERROR
	}

	cid := cmgr.ChallengeId(args[0])

	portStr, ok := os.LookupEnv("PORT")
	if !ok {
		portStr = "4242"
	}

	seedStr, ok := os.LookupEnv("SEED")
	if !ok {
		seedStr = fmt.Sprintf("%d", time.Now().Nanosecond())
	}

	flagFormat, ok := os.LookupEnv("FLAG_FORMAT")
	if !ok {
		flagFormat = "flag{%s}"
	}

	iface, ok := os.LookupEnv(cmgr.IFACE_ENV)
	if !ok {
		iface = "0.0.0.0"
	}
	if iface == "0.0.0.0" {
		iface = "localhost" // Force the server to use a single interface
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		fmt.Printf("error: cannot convert PORT variable to int (%s)\n", portStr)
		return RUNTIME_ERROR
	}

	seed, err := strconv.Atoi(seedStr)
	if err != nil {
		fmt.Printf("error: cannot convert SEED variable to int (%s)\n", seedStr)
		return RUNTIME_ERROR
	}

	builds, err := mgr.Build(cid, []int{seed}, flagFormat)
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

	sigs := make(chan os.Signal)
	signal.Notify(sigs, os.Interrupt, os.Kill)
	go func() {
		_ = <-sigs
		mgr.Stop(iid)
		mgr.Destroy(bid)
		os.Exit(0)
	}()

	fmt.Printf("challenge information available at: http://%s:%d/\n", iface, port)
	return launchPortal(mgr, iface, port, cid, bid, iid)
}

func launchPortal(mgr *cmgr.Manager, iface string, port int, cid cmgr.ChallengeId, bid cmgr.BuildId, iid cmgr.InstanceId) int {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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

		w.Write([]byte(`<!DOCTYPE html>
			<html lang="en">
				<head>
					<meta charset="utf-8">
					<title>cmgr playtest</title>
				</head>
				<body>
			`))

		w.Write([]byte(fmt.Sprintf(`<h1>%s</h1>`, cMeta.Name)))

		w.Write([]byte(fmt.Sprintf(`<h2>Description</h2><p>%s</p>`, cMeta.Description)))

		details := cMeta.Details
		artifactUrl := fmt.Sprintf("http://%s:%d/artifact/$1", iface, port)
		details = urlRe.ReplaceAllString(details, artifactUrl)
		details = serverRe.ReplaceAllString(details, iface)
		details = httpBaseRe.ReplaceAllString(details, fmt.Sprintf("http://%s", iface))

		for portRe.MatchString(details) {
			match := portRe.FindStringSubmatch(details)
			details = strings.ReplaceAll(
				details,
				match[0],
				fmt.Sprintf("%d", iMeta.Ports[match[1]]))
		}

		for lookupRe.MatchString(details) {
			match := lookupRe.FindStringSubmatch(details)
			details = strings.ReplaceAll(
				details,
				match[0],
				fmt.Sprintf("%s", bMeta.LookupData[match[1]]))
		}

		w.Write([]byte(fmt.Sprintf(`<h2>Details</h2><p>%s</p>`, details)))

		if len(cMeta.Hints) > 0 {
			w.Write([]byte(`<h2>Hints</h2><ul>`))
			for _, hint := range cMeta.Hints {
				hint = urlRe.ReplaceAllString(hint, artifactUrl)
				hint = serverRe.ReplaceAllString(hint, iface)
				hint = httpBaseRe.ReplaceAllString(hint, fmt.Sprintf("http://%s", iface))

				for portRe.MatchString(hint) {
					match := portRe.FindStringSubmatch(hint)
					hint = strings.ReplaceAll(
						hint,
						match[0],
						fmt.Sprintf("%d", iMeta.Ports[match[1]]))
				}

				for lookupRe.MatchString(hint) {
					match := lookupRe.FindStringSubmatch(hint)
					hint = strings.ReplaceAll(
						hint,
						match[0],
						fmt.Sprintf("%s", bMeta.LookupData[match[1]]))
				}

				w.Write([]byte(fmt.Sprintf(`<li>%s</li>`, hint)))
			}
			w.Write([]byte(`</ul>`))
		}
		w.Write([]byte(`<h2>Submit Flag</h2>
			<form action="/submit" method="get">
				<label for="flag">Flag:</label>
				<input type="text" id="flag" name="flag">
				<input type="submit" value="Submit">
			</form>`))

		w.Write([]byte(`</body></html>`))
	})

	http.HandleFunc("/artifact/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.Split(r.URL.Path, "/")
		filename := path[len(path)-1]
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
				w.Header()["Content-Type"] = []string{mime.TypeByExtension(filename)}
				_, err = io.Copy(w, artifacts)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})

	http.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		submittedFlag := query["flag"][0]
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

	err := http.ListenAndServe(fmt.Sprintf("%s:%d", iface, port), nil)
	if err != nil {
		return RUNTIME_ERROR
	}
	return NO_ERROR
}

const filenamePattern string = "[a-zA-Z0-9_.-]+"
const displayTextPattern string = `[^<>'"]+`
const urlPathPattern string = `[a-zA-Z0-9_%/=?+&#!.,-]+`

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
