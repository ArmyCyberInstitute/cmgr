package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

type state struct {
	mgr *cmgr.Manager
}

func main() {
	var iface string
	var port int
	var help bool
	flag.IntVar(&port, "port", 42000, "listening port for cmgrd")
	flag.StringVar(&iface, "address", "", "listening address for cmgrd")
	flag.BoolVar(&help, "help", false, "display usage information")
	flag.Parse()

	if help {
		printUsage()
		os.Exit(0)
	}

	mgr := cmgr.NewManager(cmgr.INFO)
	if mgr == nil {
		log.Fatal("failed to initialize cmgr library")
	}

	s := state{mgr: mgr}

	http.HandleFunc("/challenges", s.listHandler)
	http.HandleFunc("/challenges/", s.challengeHandler)
	http.HandleFunc("/builds/", s.buildHandler)
	http.HandleFunc("/instances/", s.instanceHandler)

	connStr := fmt.Sprintf("%s:%d", iface, port)
	log.Fatal(http.ListenAndServe(connStr, nil))
}

func printUsage() {
	fmt.Printf(`
Usage: %s [<options>]
  --address  the network address to listen on (default: 0.0.0.0)
  --port     the port to listen on (default: 42000)
  --help     display this message

Relevant environment variables:
  CMGR_DB - path to cmgr's database file (defaults to 'cmgr.db')

  CMGR_DIR - directory containing all challenges (defaults to '.')

  CMGR_ARTIFACT_DIR - directory for storing artifact bundles (defaults to '.')

  Note: The Docker client is configured via Docker's standard environment
      variables.  See https://docs.docker.com/engine/reference/commandline/cli/
      for specific details.

`, os.Args[0])
}

type ChallengeListElement struct {
	Id               cmgr.ChallengeId `json:"id"`
	SourceChecksum   uint32           `json:"source_checksum"`
	MetadataChecksum uint32           `json:"metadata_checksum"`
	SolveScript      bool             `json:"solve_script"`
}

func (s state) listHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}

	challenges := s.mgr.ListChallenges()

	respList := make([]ChallengeListElement, len(challenges))
	for i, challenge := range challenges {
		respList[i].Id = challenge.Id
		respList[i].SourceChecksum = challenge.SourceChecksum
		respList[i].MetadataChecksum = challenge.MetadataChecksum
		respList[i].SolveScript = challenge.SolveScript
	}
	body, err := json.Marshal(respList)
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
		return
	}

	w.Write(body)
}

type BuildChallengeRequest struct {
	FlagFormat string `json:"flag_format"`
	Seeds      []int  `json:"seeds"`
}

func (s state) challengeHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.Split(r.URL.Path, "/")
	pathLen := len(path)
	if len(path) < 2 {
		w.WriteHeader(404)
		return
	}

	chalStr := ""
	idx := pathLen - 1
	for idx >= 0 && path[idx] != "challenges" {
		chalStr = path[idx] + "/" + chalStr
		idx--
	}

	if idx < 0 || chalStr == "" {
		w.WriteHeader(404)
		return
	}

	challenge := cmgr.ChallengeId(chalStr[:len(chalStr)-1])

	var err error
	var body []byte
	switch r.Method {
	case "GET":
		var meta *cmgr.ChallengeMetadata
		meta, err = s.mgr.GetChallengeMetadata(challenge)
		if err == nil {
			body, err = json.Marshal(meta)
		}
	case "POST":
		var data []byte
		var buildReq BuildChallengeRequest
		data, err = ioutil.ReadAll(r.Body)

		if err == nil {
			err = json.Unmarshal(data, &buildReq)
		}

		var buildIds []cmgr.BuildId
		if err == nil {
			if buildReq.FlagFormat == "" {
				buildReq.FlagFormat = "flag{%s}"
			}
			buildIds, err = s.mgr.Build(challenge, buildReq.Seeds, buildReq.FlagFormat)
		}

		var buildMeta []*cmgr.BuildMetadata
		if err == nil {
			var bMeta *cmgr.BuildMetadata
			for _, build := range buildIds {
				bMeta, err = s.mgr.GetBuildMetadata(build)
				if err != nil {
					break
				}
				buildMeta = append(buildMeta, bMeta)
			}
		}

		if err == nil {
			body, err = json.Marshal(buildMeta)
		}
	default:
		w.WriteHeader(405)
		return
	}

	if err != nil {
		w.WriteHeader(500)
		body = []byte(err.Error())
	}

	w.Write(body)
	return
}

func (s state) buildHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.Split(r.URL.Path, "/")
	pathLen := len(path)
	if path[pathLen-1] == "artifacts.tar.gz" {
		s.artifactsHandler(w, r)
		return
	}

	if len(path) < 2 || path[pathLen-2] != "builds" {
		w.WriteHeader(404)
		return
	}

	buildInt, err := strconv.Atoi(path[pathLen-1])
	if err != nil {
		w.WriteHeader(400)
		w.Write([]byte(err.Error()))
	}

	build := cmgr.BuildId(buildInt)

	var body []byte
	switch r.Method {
	case "GET":
		var meta *cmgr.BuildMetadata
		meta, err = s.mgr.GetBuildMetadata(build)
		if err == nil {
			body, err = json.Marshal(meta)
		}
	case "POST":
		var instance cmgr.InstanceId
		instance, err = s.mgr.Start(build)

		var iMeta *cmgr.InstanceMetadata
		if err == nil {
			iMeta, err = s.mgr.GetInstanceMetadata(instance)
		}

		if err == nil {
			body, err = json.Marshal(iMeta)
		}
	case "DELETE":
		err = s.mgr.Destroy(build)
	default:
		w.WriteHeader(405)
		return
	}

	if err != nil {
		w.WriteHeader(500)
		body = []byte(err.Error())
	}

	w.Write(body)
}

func (s state) artifactsHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.Split(r.URL.Path, "/")
	pathLen := len(path)
	if pathLen < 3 || path[pathLen-1] != "artifacts.tar.gz" || path[pathLen-3] != "builds" {
		w.WriteHeader(404)
		return
	}

	buildInt, err := strconv.Atoi(path[pathLen-1])
	if err != nil {
		w.WriteHeader(400)
		w.Write([]byte(err.Error()))
	}

	build := cmgr.BuildId(buildInt)
	meta, err := s.mgr.GetBuildMetadata(build)

	var f *os.File
	if err == nil {
		f, err = os.Open(fmt.Sprintf("%s.tar.gz", meta.Images[0].DockerId))
	}

	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
		return
	}

	io.Copy(w, f)
}

func (s state) instanceHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.Split(r.URL.Path, "/")
	pathLen := len(path)
	if len(path) < 2 || path[pathLen-2] != "instances" {
		w.WriteHeader(404)
		return
	}

	instInt, err := strconv.Atoi(path[pathLen-1])
	if err != nil {
		w.WriteHeader(400)
		w.Write([]byte(err.Error()))
	}

	instance := cmgr.InstanceId(instInt)

	var body []byte
	switch r.Method {
	case "GET":
		var meta *cmgr.InstanceMetadata
		meta, err = s.mgr.GetInstanceMetadata(instance)
		if err == nil {
			body, err = json.Marshal(meta)
		}
	case "POST":
		err = s.mgr.CheckInstance(instance)
	case "DELETE":
		err = s.mgr.Stop(instance)
	default:
		w.WriteHeader(405)
		return
	}

	if err != nil {
		w.WriteHeader(500)
		body = []byte(err.Error())
	}

	w.Write(body)
}
