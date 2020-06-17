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
	cli                  *client.Client
	ctx                  context.Context
	log                  *logger
	chalDir              string
	artifactsDir         string
	db                   *sqlx.DB
	dbPath               string
	challengeDockerfiles map[string][]byte
}

type ChallengeId string
type ChallengeMetadata struct {
	Id               ChallengeId       `json:"id"`
	Name             string            `json:"name,omitempty"`
	Namespace        string            `json:"namespace"`
	ChallengeType    string            `json:"challenge_type"`
	Description      string            `json:"descrition,omitempty"`
	Details          string            `json:"details,omitempty"`
	Hints            []string          `json:"hints,omitempty"`
	SourceChecksum   uint32            `json:"source_checksum"`
	MetadataChecksum uint32            `json:"metadata_checksum`
	Path             string            `json:"path"`
	Templatable      bool              `json:"templatable,omitempty"`
	PortMap          map[string]int    `json:"port_map,omitempty"`
	MaxUsers         int               `json:"max_users,omitempty"`
	Category         string            `json:"category,omitempty"`
	Points           int               `json:"points,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	Attributes       map[string]string `json:"attributes,omitempty"`

	SolveScript bool             `json:"has_solve_script,omitempty"`
	Builds      []*BuildMetadata `json:"builds,omitempty"`
}
type ChallengeUpdates struct {
	Added      []*ChallengeMetadata `json:"added"`
	Refreshed  []*ChallengeMetadata `json:"refreshed"`
	Updated    []*ChallengeMetadata `json:"updated"`
	Removed    []*ChallengeMetadata `json:"removed"`
	Unmodified []*ChallengeMetadata `json:"unmodified"`
	Errors     []error              `json:"errors"`
}

type BuildId int
type BuildMetadata struct {
	Id BuildId `json:"id"`

	Flag       string            `json:"flag"`
	LookupData map[string]string `json:"lookup_data,omitempty"`

	Seed         int                 `json:"seed"`
	ImageIds     []string            `json:"images"`
	HasArtifacts bool                `json:"has_artifacts"`
	LastSolved   string              `json:"last_solved"`
	ChallengeId  ChallengeId         `json:"challenge_id"`
	Instances    []*InstanceMetadata `json:"instances,omitempty"`
}

type InstanceId int
type InstanceMetadata struct {
	Id         InstanceId     `json:"id"`
	Ports      map[string]int `json:"ports,omitempty"`
	LastSolved string         `json:"last_solved"`
	BuildId    BuildId        `json:"build_id"`
}

type portTuple struct {
	Name string
	Port int
}
