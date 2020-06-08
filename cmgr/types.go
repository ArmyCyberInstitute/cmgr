package cmgr

import (
	"context"

	"github.com/docker/docker/client"
	"github.com/jmoiron/sqlx"
)

const (
	DB_ENV           string = "CMGR_DB"
	DIR_ENV          string = "CMGR_DIR"
	ARTIFACT_DIR_ENV string = "CMGR_ARTIFACT_DIR"
)

type Manager struct {
	cli     *client.Client
	ctx     context.Context
	log     *logger
	chalDir string
	db      *sqlx.DB
	dbPath  string
}

type ChallengeId string
type ChallengeMetadata struct {
	Id              ChallengeId       `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"descrition"`
	DetailsTemplate string            `json:"details_template"`
	HintsTemplate   string            `json:"hints_template"`
	Version         []byte            `json:"version"`
	HasSolveScript  bool              `json:"has_solve_script"`
	Templatable     bool              `json:"templatable"`
	PortMap         map[string]int    `json:"port_map"`
	MaxUsers        int               `json:"max_users"`
	Category        string            `json:"category"`
	Points          int               `json:"points"`
	Tags            []string          `json:"tags"`
	Attributes      map[string]string `json:"attributes"`
	Builds          []BuildMetadata   `json:"builds,omitempty"`
}
type ChallengeUpdates struct {
	Added      []ChallengeId `json:"added"`
	Updated    []ChallengeId `json:"updated"`
	Removed    []ChallengeId `json:"removed"`
	Unmodified []ChallengeId `json:"unmodified"`
	Errors     []error       `json:"errors"`
}

type BuildId int
type BuildMetadata struct {
	Id          BuildId            `json:"id"`
	Flag        string             `json:"flag"`
	Seed        string             `json:"seed"`
	LookupData  map[string]string  `json:"lookup_data"`
	LastSolved  string             `json:"last_solved"`
	ChallengeId ChallengeId        `json:"challenge_id"`
	Instances   []InstanceMetadata `json:"instances,omitempty"`
}

type InstanceId int
type InstanceMetadata struct {
	Id         InstanceId     `json:"id"`
	Ports      map[string]int `json:"ports"`
	LastSolved string         `json:"last_solved"`
	BuildId    BuildId        `json:"build_id"`
}
