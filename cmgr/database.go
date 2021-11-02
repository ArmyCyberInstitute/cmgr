package cmgr

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

const schemaQuery string = `
	CREATE TABLE IF NOT EXISTS challenges (
		id TEXT NOT NULL PRIMARY KEY,
		name TEXT NOT NULL,
		namespace TEXT NOT NULL,
		challengetype TEXT NOT NULL,
		description TEXT NOT NULL,
		details TEXT,
		sourcechecksum INT NOT NULL,
		metadatachecksum INT NOT NULL,
		path TEXT NOT NULL,
		solvescript INTEGER NOT NULL CHECK(solvescript == 0 OR solvescript == 1),
		templatable INTEGER NOT NULL CHECK(templatable == 0 OR templatable == 1),
		maxusers INTEGER NOT NULL CHECK(maxusers >= 0),
		category TEXT,
		points INTEGER NOT NULL CHECK(points >= 0)
	);

	CREATE TABLE IF NOT EXISTS hints (
		challenge TEXT NOT NULL,
		idx INT NOT NULL,
		hint TEXT NOT NULL,
		PRIMARY KEY (challenge, idx),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS tags (
		challenge TEXT NOT NULL,
		tag TEXT NOT NULL,
		PRIMARY KEY (challenge, tag),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS tagIndex ON tags(LOWER(tag));

	CREATE TABLE IF NOT EXISTS attributes (
		challenge TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		PRIMARY KEY (challenge, key),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS attributeIndex ON attributes(LOWER(key));

	CREATE TABLE IF NOT EXISTS hosts (
		challenge TEXT NOT NULL,
		name TEXT NOT NULL,
		idx INT NOT NULL,
		target TEXT NOT NULL,
		PRIMARY KEY (challenge, name),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
		    ON UPDATE CASCADE ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS hostsIndex ON hosts(challenge);

	CREATE TABLE IF NOT EXISTS portNames (
		challenge TEXT NOT NULL,
		name TEXT NOT NULL,
		host TEXT NOT NULL,
		port INTEGER NOT NULL CHECK (port > 0 AND port < 65536),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE CASCADE ON DELETE CASCADE,
		FOREIGN KEY (challenge, host) REFERENCES hosts (challenge, name)
		    ON UPDATE CASCADE ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS builds (
		id INTEGER PRIMARY KEY,
		flag TEXT NOT NULL,
		format TEXT NOT NULL,
		seed INTEGER NOT NULL,
		hasartifacts INTEGER NOT NULL CHECK (hasartifacts = 0 OR hasartifacts = 1),
		lastsolved INTEGER,
		challenge TEXT NOT NULL,
		schema TEXT NOT NULL,
		instancecount INT NOT NULL,
		UNIQUE(schema, format, challenge, seed),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	);

	CREATE INDEX IF NOT EXISTS schemaIndex on builds(schema);

	CREATE TABLE IF NOT EXISTS images (
		id INTEGER PRIMARY KEY,
		build INTEGER NOT NULL,
		host TEXT NOT NULL,
		FOREIGN KEY (build) REFERENCES builds (id)
		    ON UPDATE RESTRICT ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS imagePorts (
		image INTEGER NOT NULL,
		port TEXT NOT NULL,
		FOREIGN KEY (image) REFERENCES images (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS lookupData (
		build INTEGER NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		FOREIGN KEY (build) REFERENCES builds (id)
			ON UPDATE RESTRICT ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS instances (
		id INTEGER PRIMARY KEY,
		lastsolved INTEGER,
		build INTEGER NOT NULL,
		FOREIGN KEY (build) REFERENCES builds (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	);

	CREATE TABLE IF NOT EXISTS portAssignments (
		instance INTEGER NOT NULL,
		name TEXT NOT NULL,
		port INTEGER NOT NULL CHECK (port > 0 AND port < 65536),
		FOREIGN KEY (instance) REFERENCES instances (id)
			ON UPDATE RESTRICT ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS containers (
		instance INTEGER NOT NULL,
		id TEXT NOT NULL PRIMARY KEY,
		FOREIGN KEY (instance) REFERENCES instances (id)
			ON UPDATE RESTRICT ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS networkOptions (
		challenge INTEGER NOT NULL,
		internal INTEGER NOT NULL CHECK(internal == 0 OR internal == 1),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS containerOptions (
		challenge INTEGER NOT NULL,
		host TEXT NOT NULL,
		init INTEGER NOT NULL CHECK(init == 0 OR init == 1),
		cpus TEXT NOT NULL,
		memory TEXT NOT NULL,
		ulimits TEXT NOT NULL,
		pidslimit INTEGER NOT NULL,
		readonlyrootfs INTEGER NOT NULL CHECK(readonlyrootfs == 0 OR readonlyrootfs == 1),
		droppedcaps TEXT NOT NULL,
		nonewprivileges INTEGER NOT NULL CHECK(nonewprivileges == 0 OR nonewprivileges == 1),
		storageopts TEXT NOT NULL,
		cgroupparent TEXT NOT NULL,
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);`

// Connects to the desired database (creating it if it does not exist) and then
// ensures that the necessary tables and indexes exist and that the sqlite
// engine is enforcing foreign key constraints.
func (m *Manager) initDatabase() error {
	dbPath, isSet := os.LookupEnv(DB_ENV)
	if !isSet {
		dbPath = "cmgr.db"
	}

	db, err := sqlx.Open("sqlite3", dbPath+"?_fk=true")
	if err != nil {
		m.log.errorf("could not open database: %s", err)
		return err
	}

	// File exists and is a valid sqlite database
	m.dbPath = dbPath

	_, err = db.Exec(schemaQuery)
	if err != nil {
		m.log.errorf("could not set database schema: %s", err)
		return err
	}

	var fkeysEnforced bool
	err = db.QueryRow("PRAGMA foreign_keys;").Scan(&fkeysEnforced)
	if err != nil {
		m.log.errorf("could not check for foreign key support: %s", err)
		return err
	}

	if !fkeysEnforced {
		m.log.errorf("foreign keys not enabled")
		return errors.New("foreign keys not enabled")
	}

	m.db = db

	return nil
}

func (m *Manager) getReversePortMap(id ChallengeId) (map[string]string, error) {
	rpm := make(map[string]string)

	res := []struct {
		Name string
		Port int
	}{}

	err := m.db.Select(&res, `SELECT name, port FROM portNames WHERE challenge=?;`, id)
	if err != nil {
		m.log.errorf("could not get challenge ports: %s", err)
		return nil, err
	}

	for _, entry := range res {
		rpm[fmt.Sprintf("%d/tcp", entry.Port)] = entry.Name
	}

	m.log.debugf("reverse port map for %s: %v", id, rpm)

	return rpm, nil
}

func (m *Manager) usedPortSet() (map[int]struct{}, error) {
	var ports []int
	err := m.db.Select(&ports, "SELECT port FROM portAssignments;")

	portSet := make(map[int]struct{})
	for _, port := range ports {
		portSet[port] = struct{}{}
	}

	return portSet, err
}

func (m *Manager) safeToRefresh(new *ChallengeMetadata) bool {
	old, err := m.lookupChallengeMetadata(new.Id)
	if err != nil {
		return false
	}

	sameType := old.ChallengeType == new.ChallengeType
	sameNetworkOptions := reflect.DeepEqual(old.NetworkOptions, new.NetworkOptions)
	sameContainerOptions := reflect.DeepEqual(old.ContainerOptions, new.ContainerOptions)

	safe := sameType && sameNetworkOptions && sameContainerOptions

	return safe
}

func (m *Manager) dumpState() ([]*ChallengeMetadata, error) {
	challenges, err := m.listChallenges()
	if err != nil {
		return nil, err
	}

	for i, challenge := range challenges {
		meta, err := m.lookupChallengeMetadata(challenge.Id)
		if err != nil {
			return nil, err
		}

		meta.Builds = []*BuildMetadata{}
		err = m.db.Select(&meta.Builds, "SELECT id FROM builds WHERE challenge=?", challenge.Id)
		if err != nil {
			m.log.errorf("failed to select builds for '%s': %s", challenge.Id, err)
			return nil, err
		}

		for j, build := range meta.Builds {
			bMeta, err := m.lookupBuildMetadata(build.Id)
			if err != nil {
				return nil, err
			}

			bMeta.Instances = []*InstanceMetadata{}
			err = m.db.Select(&bMeta.Instances, "SELECT id FROM instances WHERE build=?", bMeta.Id)
			if err != nil {
				m.log.errorf("failed to select instances for '%s/%d': %s", challenge.Id, bMeta.Id, err)
				return nil, err
			}

			for k, instance := range bMeta.Instances {
				iMeta, err := m.lookupInstanceMetadata(instance.Id)
				if err != nil {
					return nil, err
				}

				bMeta.Instances[k] = iMeta
			}
			meta.Builds[j] = bMeta
		}
		challenges[i] = meta
	}
	return challenges, nil
}
