package cmgr

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

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

// Gets just the ID and checksum for all known challenges
func (m *Manager) listChallenges() ([]*ChallengeMetadata, error) {
	metadata := []*ChallengeMetadata{}
	err := m.db.Select(&metadata, "SELECT id, name, path, sourcechecksum, metadatachecksum, solvescript FROM challenges ORDER BY id;")
	return metadata, err
}

func (m *Manager) searchChallenges(tags []string) ([]*ChallengeMetadata, error) {
	metadata := []*ChallengeMetadata{}
	var err error
	if len(tags) == 0 {
		return m.listChallenges()
	}

	interfaceTags := make([]interface{}, len(tags))
	for i, tag := range tags {
		interfaceTags[i] = strings.ReplaceAll(tag, "*", "%")
	}
	tagBaseQuery := "SELECT challenge FROM tags WHERE tag LIKE ?"
	subQuery := "(" +
		tagBaseQuery +
		strings.Repeat(" INTERSECT "+tagBaseQuery, len(tags)-1) +
		")"
	query := fmt.Sprintf("SELECT id, name, path, sourcechecksum, metadatachecksum, solvescript FROM challenges WHERE id IN %s ORDER BY id;", subQuery)
	err = m.db.Select(&metadata, query, interfaceTags...)

	return metadata, err
}

func (m *Manager) lookupChallengeMetadata(challenge ChallengeId) (*ChallengeMetadata, error) {
	metadata := new(ChallengeMetadata)
	txn := m.db.MustBegin()

	err := txn.Get(metadata, "SELECT * FROM challenges WHERE id=?", challenge)

	if err == nil {
		err = txn.Select(&metadata.Hints, "SELECT hint FROM hints WHERE challenge=? ORDER BY idx", challenge)
	}

	if err == nil {
		err = txn.Select(&metadata.Tags, "SELECT tag FROM tags WHERE challenge=?", challenge)
	}

	ports := []struct {
		Name string
		Port int
	}{}
	if err == nil {
		err = txn.Select(&ports, "SELECT name, port FROM portNames WHERE challenge=?", challenge)
	}

	metadata.PortMap = make(map[string]int)
	for _, port := range ports {
		metadata.PortMap[port.Name] = port.Port
	}

	if err == nil {
		err = txn.Commit()
		if err != nil {
			m.log.errorf("failed to commit read-only transaction: %s", err)
		}
	} else {
		m.log.errorf("read of database failed: %s", err)
		closeErr := txn.Rollback()
		if closeErr != nil {
			m.log.errorf("rollback failed: %s", err)
			err = closeErr
		}
	}

	return metadata, err
}

func (m *Manager) lookupBuildMetadata(build BuildId) (*BuildMetadata, error) {
	metadata := new(BuildMetadata)
	txn := m.db.MustBegin()

	err := txn.Get(metadata, "SELECT * FROM builds WHERE id=?", build)

	lookups := []struct {
		Key   string
		Value string
	}{}
	if err == nil {
		err = txn.Select(&lookups, "SELECT key, value FROM lookupData WHERE build=?", build)
	}

	metadata.LookupData = make(map[string]string)
	for _, kvPair := range lookups {
		metadata.LookupData[kvPair.Key] = kvPair.Value
	}

	metadata.Images = []Image{}
	if err == nil {
		err = txn.Select(&metadata.Images, "SELECT id, dockerid FROM images WHERE build=?", build)
		if err == nil {
			for i, image := range metadata.Images {
				err = txn.Select(&metadata.Images[i].Ports, "SELECT port FROM imagePorts WHERE image=?", image.Id)
			}
		}
	}

	if err == nil {
		err = txn.Commit()
		if err != nil {
			m.log.errorf("failed to commit read-only transaction: %s", err)
		}
	} else {
		m.log.errorf("read of database failed: %s", err)
		closeErr := txn.Rollback()
		if closeErr != nil {
			m.log.errorf("rollback failed: %s", err)
			err = closeErr
		}
	}

	return metadata, err
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

	return rpm, nil
}

// Adds the discovered challenges to the database
func (m *Manager) addChallenges(addedChallenges []*ChallengeMetadata) []error {
	errs := []error{}
	for _, metadata := range addedChallenges {
		txn := m.db.MustBegin()

		_, err := txn.NamedExec(challengeInsertQuery, metadata)
		if err != nil {
			m.log.error(err)
			err = txn.Rollback()
			if err != nil { // If rollback fails, we're in trouble.
				m.log.error(err)
				return append(errs, err)
			}
			continue
		}

		for i, hint := range metadata.Hints {
			_, err = txn.Exec("INSERT INTO hints(challenge, idx, hint) VALUES (?, ?, ?);",
				metadata.Id,
				i,
				hint)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		for _, tag := range metadata.Tags {
			_, err = txn.Exec("INSERT INTO tags(challenge, tag) VALUES (?, ?);",
				metadata.Id,
				tag)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		for k, v := range metadata.Attributes {
			_, err = txn.Exec("INSERT INTO attributes(challenge, key, value) VALUES (?, ?, ?);",
				metadata.Id,
				k,
				v)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		for k, v := range metadata.PortMap {
			_, err = txn.Exec("INSERT INTO portNames(challenge, name, port) VALUES (?, ?, ?);",
				metadata.Id,
				k,
				v)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		if err := txn.Commit(); err != nil { // It's undocumented what this means...
			m.log.error(err)
			errs = append(errs, err)
		}
	}
	return errs
}

func (m *Manager) updateChallenges(updatedChallenges []*ChallengeMetadata, rebuild bool) []error {
	errs := []error{}
	for _, metadata := range updatedChallenges {
		txn := m.db.MustBegin()

		_, err := txn.NamedExec(challengeUpdateQuery, metadata)
		if err != nil {
			m.log.error(err)
			err = txn.Rollback()
			if err != nil { // If rollback fails, we're in trouble.
				m.log.error(err)
				return append(errs, err)
			}
			continue
		}

		_, err = txn.Exec("DELETE FROM hints WHERE challenge = ?;", metadata.Id)

		if err != nil {
			m.log.error(err)
			err = txn.Rollback()
			if err != nil { // If rollback fails, we're in trouble.
				m.log.error(err)
				return append(errs, err)
			}
			continue
		}
		for i, hint := range metadata.Hints {

			_, err = txn.Exec("INSERT INTO hints(challenge, idx, hint) VALUES (?, ?, ?);",
				metadata.Id,
				i,
				hint)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		_, err = txn.Exec("DELETE FROM tags WHERE challenge = ?;", metadata.Id)

		if err != nil {
			m.log.error(err)
			err = txn.Rollback()
			if err != nil { // If rollback fails, we're in trouble.
				m.log.error(err)
				return append(errs, err)
			}
			continue
		}
		for _, tag := range metadata.Tags {

			_, err = txn.Exec("INSERT INTO tags(challenge, tag) VALUES (?, ?);",
				metadata.Id,
				tag)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		_, err = txn.Exec("DELETE FROM attributes WHERE challenge = ?;", metadata.Id)

		if err != nil {
			m.log.error(err)
			err = txn.Rollback()
			if err != nil { // If rollback fails, we're in trouble.
				m.log.error(err)
				return append(errs, err)
			}
			continue
		}
		for k, v := range metadata.Attributes {

			_, err = txn.Exec("INSERT INTO attributes(challenge, key, value) VALUES (?, ?, ?);",
				metadata.Id,
				k,
				v)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		_, err = txn.Exec("DELETE FROM portNames WHERE challenge = ?;", metadata.Id)

		if err != nil {
			m.log.error(err)
			err = txn.Rollback()
			if err != nil { // If rollback fails, we're in trouble.
				m.log.error(err)
				return append(errs, err)
			}
			continue
		}
		for k, v := range metadata.PortMap {

			_, err = txn.Exec("INSERT INTO portNames(challenge, name, port) VALUES (?, ?, ?);",
				metadata.Id,
				k,
				v)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				continue
			}
		}

		if rebuild {
			builds := []BuildId{}
			err = txn.Select(&builds, "SELECT id FROM builds WHERE challenge=?;", metadata.Id)
			if err != nil {
				m.log.error(err)
				errs = append(errs, err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				return errs
			}

			if len(builds) > 0 {
				buildCtxFile, err := m.createBuildContext(metadata, m.getDockerfile(metadata.ChallengeType))
				if err != nil {
					m.log.errorf("failed to create build context: %s", err)
					return append(errs, err)
				}
				buildCtx, err := os.Open(buildCtxFile)
				if err != nil {
					m.log.errorf("could not open build context: %s", err)
					return append(errs, err)
				}
				defer buildCtx.Close()

				return append(errs, errors.New("rebuild not implemented"))
			}
		}

		if err := txn.Commit(); err != nil { // It's undocumented what this means...
			m.log.error(err)
			errs = append(errs, err)
		}
	}
	return errs
}

func (m *Manager) saveBuildMetadata(builds []*BuildMetadata) ([]BuildId, error) {
	txn := m.db.MustBegin()
	ids := make([]BuildId, 0, len(builds))
	for _, build := range builds {
		res, err := txn.NamedExec(buildInsertQuery, build)

		if err != nil {
			m.log.error(err)
			cerr := txn.Rollback()
			if cerr != nil { // If rollback fails, we're in trouble.
				m.log.error(cerr)
				err = cerr
			}
			return nil, err
		}

		buildId, err := res.LastInsertId()
		if err != nil {
			m.log.error(err)
			cerr := txn.Rollback()
			if cerr != nil { // If rollback fails, we're in trouble.
				m.log.error(cerr)
				err = cerr
			}
			return nil, err
		}

		for k, v := range build.LookupData {
			_, err = txn.Exec("INSERT INTO lookupData(build, key, value) VALUES (?, ?, ?);",
				buildId,
				k,
				v)

			if err != nil {
				m.log.error(err)
				cerr := txn.Rollback()
				if cerr != nil { // If rollback fails, we're in trouble.
					m.log.error(cerr)
					err = cerr
				}
				return nil, err
			}
		}

		for _, image := range build.Images {
			res, err = txn.Exec("INSERT INTO images(build, dockerid) VALUES (?, ?);",
				buildId,
				image.DockerId)
			if err != nil {
				m.log.error(err)
				cerr := txn.Rollback()
				if cerr != nil { // If rollback fails, we're in trouble.
					m.log.error(cerr)
					err = cerr
				}
				return nil, err
			}

			imageId, err := res.LastInsertId()
			if err != nil {
				m.log.error(err)
				cerr := txn.Rollback()
				if cerr != nil { // If rollback fails, we're in trouble.
					m.log.error(cerr)
					err = cerr
				}
				return nil, err
			}

			image.Id = ImageId(imageId)

			for _, port := range image.Ports {
				_, err = txn.Exec("INSERT INTO imagePorts(image, port) VALUES (?, ?);",
					image.Id,
					port)

				if err != nil {
					m.log.error(err)
					cerr := txn.Rollback()
					if cerr != nil { // If rollback fails, we're in trouble.
						m.log.error(cerr)
						err = cerr
					}
					return nil, err
				}
			}
		}

		ids = append(ids, BuildId(buildId))
	}

	err := txn.Commit()
	if err != nil { // It's undocumented what this means...
		m.log.error(err)
	}
	return ids, err
}

func (m *Manager) removeBuildMetadata(build BuildId) error {
	txn := m.db.MustBegin()
	_, err := txn.Exec("DELETE FROM builds WHERE id=?", build)

	if err == nil {
		err = txn.Commit()
		if err != nil {
			m.log.errorf("failed to commit deletion of build: %s", err)
		}
	} else {
		m.log.errorf("failed to delete build: %s", err)
		closeErr := txn.Rollback()
		if closeErr != nil {
			m.log.errorf("rollback failed: %s", err)
			err = closeErr
		}
	}

	return err
}

func (m *Manager) saveInstanceMetadata(meta *InstanceMetadata) (InstanceId, error) {
	txn := m.db.MustBegin()
	res, err := txn.NamedExec("INSERT INTO instances(build, network, lastsolved) VALUES (:build, :network, :lastsolved);", meta)

	if err != nil {
		m.log.errorf("failed to create instance entry: %s", err)
		cerr := txn.Rollback()
		if cerr != nil { // If rollback fails, we're in trouble.
			m.log.error(cerr)
			err = cerr
		}
		return 0, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		m.log.errorf("failed to get instance id: %s", err)
		cerr := txn.Rollback()
		if cerr != nil { // If rollback fails, we're in trouble.
			m.log.error(cerr)
			err = cerr
		}
		return 0, err
	}

	for name, port := range meta.Ports {
		_, err = txn.Exec("INSERT INTO portAssignments(instance, name, port) VALUES (?, ?, ?);",
			id,
			name,
			port)

		if err != nil {
			m.log.errorf("failed to record port assignment: %s", err)
			cerr := txn.Rollback()
			if cerr != nil { // If rollback fails, we're in trouble.
				m.log.error(cerr)
				err = cerr
			}
			return 0, err
		}
	}

	for _, containerId := range meta.Containers {
		_, err = txn.Exec("INSERT INTO containers(instance, id) VALUES (?, ?);",
			id,
			containerId)

		if err != nil {
			m.log.errorf("failed to record containers: %s", err)
			cerr := txn.Rollback()
			if cerr != nil { // If rollback fails, we're in trouble.
				m.log.error(cerr)
				err = cerr
			}
			return 0, err
		}
	}

	err = txn.Commit()
	if err != nil { // It's undocumented what this means...
		m.log.error(err)
	}
	return InstanceId(id), err
}

func (m *Manager) lookupInstanceMetadata(instance InstanceId) (*InstanceMetadata, error) {
	metadata := new(InstanceMetadata)
	txn := m.db.MustBegin()

	err := txn.Get(metadata, "SELECT * FROM instances WHERE id=?", instance)

	ports := []struct {
		Name string
		Port int
	}{}
	if err == nil {
		err = txn.Select(&ports, "SELECT name, port FROM portAssignments WHERE instance=?", instance)
	}

	metadata.Ports = make(map[string]int)
	for _, kvPair := range ports {
		metadata.Ports[kvPair.Name] = kvPair.Port
	}

	metadata.Containers = []string{}
	if err == nil {
		err = txn.Select(&metadata.Containers, "SELECT id FROM containers WHERE instance=?", instance)
	}
	if err == nil {
		err = txn.Commit()
		if err != nil {
			m.log.errorf("failed to commit read-only transaction: %s", err)
		}
	} else {
		m.log.errorf("read of database failed: %s", err)
		closeErr := txn.Rollback()
		if closeErr != nil {
			m.log.errorf("rollback failed: %s", err)
			err = closeErr
		}
	}

	return metadata, err
}

func (m *Manager) removeInstanceMetadata(instance InstanceId) error {
	txn := m.db.MustBegin()
	_, err := txn.Exec("DELETE FROM instances WHERE id=?", instance)

	if err == nil {
		err = txn.Commit()
		if err != nil {
			m.log.errorf("failed to commit deletion of instance: %s", err)
		}
	} else {
		m.log.errorf("failed to delete instance: %s", err)
		closeErr := txn.Rollback()
		if closeErr != nil {
			m.log.errorf("rollback failed: %s", err)
			err = closeErr
		}
	}

	return err
}

func (m *Manager) removeChallenges(removedChallenges []*ChallengeMetadata) error {
	txn := m.db.MustBegin()
	for _, metadata := range removedChallenges {
		// This should throw an error and cause a rollback when builds exist for
		// a challenge we are removing.
		_, err := txn.Exec("DELETE FROM challenges WHERE id = ?;", metadata.Id)
		if err != nil {
			m.log.error(err)
			rbErr := txn.Rollback()
			if rbErr != nil { // If rollback fails, we're in trouble.
				m.log.error(rbErr)
				return rbErr
			}
			return err
		}
	}

	if err := txn.Commit(); err != nil { // It's undocumented what this means...
		m.log.error(err)
		return err
	}

	return nil
}

func (m *Manager) safeToRefresh(new *ChallengeMetadata) bool {
	old, err := m.lookupChallengeMetadata(new.Id)
	if err != nil {
		return false
	}

	safe := old.ChallengeType == new.ChallengeType

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

const (
	// Query that can be unconditionally run to ensure the database is initialized
	// upon first connecting.
	schemaQuery string = `
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

	CREATE TABLE IF NOT EXISTS portNames (
		challenge TEXT NOT NULL,
		name TEXT NOT NULL,
		port INTEGER NOT NULL CHECK (port > 0 AND port < 65536),
		FOREIGN KEY (challenge) REFERENCES challenges (id)
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
		FOREIGN KEY (challenge) REFERENCES challenges (id)
			ON UPDATE RESTRICT ON DELETE RESTRICT
	);

	CREATE TABLE IF NOT EXISTS images (
		id INTEGER PRIMARY KEY,
		build INTEGER NOT NULL,
		dockerid TEXT NOT NULL,
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
		network TEXT NOT NULL,
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
	);`

	challengeInsertQuery string = `
	INSERT INTO challenges (
		id,
		name,
		namespace,
		challengetype,
		description,
		details,
		sourcechecksum,
		metadatachecksum,
		path,
		solvescript,
		templatable,
		maxusers,
		category,
		points
	)
	VALUES (
		:id,
		:name,
		:namespace,
		:challengetype,
		:description,
		:details,
		:sourcechecksum,
		:metadatachecksum,
		:path,
		:solvescript,
		:templatable,
		:maxusers,
		:category,
		:points
	);`

	challengeUpdateQuery string = `
	UPDATE challenges SET
	    name = :name,
		challengetype = :challengetype,
		description = :description,
		details = :details,
		sourcechecksum = :sourcechecksum,
		metadatachecksum = :metadatachecksum,
		path = :path,
		solvescript = :solvescript,
		templatable = :templatable,
		maxusers = :maxusers,
		category = :category,
		points = :points
	WHERE id = :id;`

	buildInsertQuery string = `
	INSERT INTO builds (
		flag,
		seed,
		format,
		hasartifacts,
		lastsolved,
		challenge
	)
	VALUES (
		:flag,
		:seed,
		:format,
		:hasartifacts,
		:lastsolved,
		:challenge
	);`
)
