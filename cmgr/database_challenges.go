package cmgr

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

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
	if isEmptyQueryError(err) {
		err = unknownChallengeIdError(challenge)
	}

	if err == nil {
		err = txn.Select(&metadata.Hints, "SELECT hint FROM hints WHERE challenge=? ORDER BY idx", challenge)
	}

	if err == nil {
		err = txn.Select(&metadata.Tags, "SELECT tag FROM tags WHERE challenge=?", challenge)
	}

	if err == nil {
		err = txn.Select(&metadata.Hosts, "SELECT name, target FROM hosts WHERE challenge=?", challenge)
	}

	ports := []struct {
		Name string
		Host string
		Port int
	}{}
	if err == nil {
		err = txn.Select(&ports, "SELECT name, host, port FROM portNames WHERE challenge=?", challenge)
	}

	metadata.PortMap = make(map[string]PortInfo)
	for _, port := range ports {
		metadata.PortMap[port.Name] = PortInfo{port.Host, port.Port}
	}

	attributes := []struct {
		Key   string
		Value string
	}{}
	if err == nil {
		err = txn.Select(&attributes, "SELECT key, value FROM attributes WHERE challenge=?", challenge)
	}

	metadata.Attributes = make(map[string]string)
	for _, attr := range attributes {
		metadata.Attributes[attr.Key] = attr.Value
	}

	networkOptions := new(NetworkOptions)
	if err == nil {
		err = txn.Get(networkOptions, "SELECT internal FROM networkOptions WHERE challenge=?", challenge)
	}
	metadata.NetworkOptions = *networkOptions

	containerOptions := new([]dbContainerOptions)
	if err == nil {
		err = txn.Select(containerOptions, "SELECT host, init, cpus, memory, ulimits, pidslimit, readonlyrootfs, droppedcaps, nonewprivileges, storageopts, cgroupparent FROM containerOptions WHERE challenge=?", challenge)
	}
	for _, dbOpts := range *containerOptions {
		cOpts, err := newFromDbContainerOptions(dbOpts)
		if err != nil {
			break
		}
		if metadata.ContainerOptions == nil {
			metadata.ContainerOptions = make(ContainerOptionsWrapper)
		}
		metadata.ContainerOptions[dbOpts.Host] = cOpts
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
				break
			}
		}
		if err != nil {
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
				break
			}
		}
		if err != nil {
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
				break
			}
		}
		if err != nil {
			continue
		}

		for i, host := range metadata.Hosts {
			m.log.debugf("%s: %v", metadata.Id, host)
			_, err = txn.Exec("INSERT INTO hosts(challenge, name, idx, target) VALUES (?, ?, ?, ?);",
				metadata.Id,
				host.Name,
				i,
				host.Target)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				break
			}
		}
		if err != nil {
			continue
		}

		for k, v := range metadata.PortMap {
			m.log.debugf("%s: %v", metadata.Id, v)
			_, err = txn.Exec("INSERT INTO portNames(challenge, name, host, port) VALUES (?, ?, ?, ?);",
				metadata.Id,
				k,
				v.Host,
				v.Port)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				break
			}
		}
		if err != nil {
			continue
		}

		m.log.debugf("%s: %v", metadata.Id, metadata.NetworkOptions)
		_, err = txn.Exec("INSERT INTO networkOptions(challenge, internal) VALUES (?, ?);",
			metadata.Id,
			metadata.NetworkOptions.Internal)
		if err != nil {
			m.log.error(err)
			err = txn.Rollback()
			if err != nil { // If rollback fails, we're in trouble.
				m.log.error(err)
				return append(errs, err)
			}
		}
		if err != nil {
			continue
		}

		for host, opts := range metadata.ContainerOptions {
			host_str := ""
			if host != "" {
				host_str = fmt.Sprintf(" (%s)", host)
			}
			dbOpts, err := opts.toDbContainerOptions()
			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				break
			}
			m.log.debugf("%s%s: %v", metadata.Id, host_str, dbOpts)
			_, err = txn.Exec("INSERT INTO containerOptions(challenge, host, init, cpus, memory, ulimits, pidslimit, readonlyrootfs, droppedcaps, nonewprivileges, storageopts, cgroupparent) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);",
				metadata.Id,
				host,
				dbOpts.Init,
				dbOpts.Cpus,
				dbOpts.Memory,
				dbOpts.Ulimits,
				dbOpts.PidsLimit,
				dbOpts.ReadonlyRootfs,
				dbOpts.DroppedCaps,
				dbOpts.NoNewPrivileges,
				dbOpts.StorageOpts,
				dbOpts.CgroupParent)
			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				break
			}
		}
		if err != nil {
			continue
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
				break
			}
		}
		if err != nil {
			continue
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
				break
			}
		}
		if err != nil {
			continue
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
				break
			}
		}
		if err != nil {
			continue
		}

		_, err = txn.Exec("DELETE FROM hosts WHERE challenge = ?;", metadata.Id)

		if err != nil {
			m.log.error(err)
			err = txn.Rollback()
			if err != nil { // If rollback fails, we're in trouble.
				m.log.error(err)
				return append(errs, err)
			}
			continue
		}

		for i, host := range metadata.Hosts {
			_, err = txn.Exec("INSERT INTO hosts(challenge, name, idx, target) VALUES (?, ?, ?, ?);",
				metadata.Id,
				host.Name,
				i,
				host.Target)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				break
			}
		}
		if err != nil {
			continue
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
			_, err = txn.Exec("INSERT INTO portNames(challenge, name, host, port) VALUES (?, ?, ?, ?);",
				metadata.Id,
				k,
				v.Host,
				v.Port)

			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				break
			}
		}
		if err != nil {
			continue
		}

		_, err = txn.Exec("DELETE FROM networkOptions WHERE challenge = ?;", metadata.Id)

		if err != nil {
			m.log.error(err)
			err = txn.Rollback()
			if err != nil { // If rollback fails, we're in trouble.
				m.log.error(err)
				return append(errs, err)
			}
			continue
		}

		_, err = txn.Exec("INSERT INTO networkOptions(challenge, internal) VALUES (?, ?);",
			metadata.Id,
			metadata.NetworkOptions.Internal)
		if err != nil {
			m.log.error(err)
			err = txn.Rollback()
			if err != nil { // If rollback fails, we're in trouble.
				m.log.error(err)
				return append(errs, err)
			}
		}
		if err != nil {
			continue
		}

		_, err = txn.Exec("DELETE FROM containerOptions WHERE challenge = ?;", metadata.Id)

		if err != nil {
			m.log.error(err)
			err = txn.Rollback()
			if err != nil { // If rollback fails, we're in trouble.
				m.log.error(err)
				return append(errs, err)
			}
			continue
		}

		for host, opts := range metadata.ContainerOptions {
			dbOpts, err := opts.toDbContainerOptions()
			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				break
			}
			_, err = txn.Exec("INSERT INTO containerOptions(challenge, host, init, cpus, memory, ulimits, pidslimit, readonlyrootfs, droppedcaps, nonewprivileges, storageopts, cgroupparent) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);",
				metadata.Id,
				host,
				dbOpts.Init,
				dbOpts.Cpus,
				dbOpts.Memory,
				dbOpts.Ulimits,
				dbOpts.PidsLimit,
				dbOpts.ReadonlyRootfs,
				dbOpts.DroppedCaps,
				dbOpts.NoNewPrivileges,
				dbOpts.StorageOpts,
				dbOpts.CgroupParent)
			if err != nil {
				m.log.error(err)
				err = txn.Rollback()
				if err != nil { // If rollback fails, we're in trouble.
					m.log.error(err)
					return append(errs, err)
				}
				break
			}
		}
		if err != nil {
			continue
		}

		if err := txn.Commit(); err != nil { // It's undocumented what this means...
			m.log.error(err)
			errs = append(errs, err)
			continue // next challenge
		}

		if rebuild {
			buildIds := []BuildId{}
			err = m.db.Select(&buildIds, "SELECT id FROM builds WHERE challenge=?;", metadata.Id)
			if err != nil {
				m.log.error(err)
				errs = append(errs, err)
				continue
			}

			if len(buildIds) > 0 {
				buildCtxFile, err := m.createBuildContext(metadata, m.getDockerfile(metadata.ChallengeType))
				if err != nil {
					m.log.errorf("failed to create build context: %s", err)
					errs = append(errs, err)
					continue
				}
				defer os.Remove(buildCtxFile)

				for _, buildId := range buildIds {
					build, err := m.lookupBuildMetadata(buildId)
					if err != nil {
						errs = append(errs, err)
						continue
					}
					cMeta, err := m.lookupChallengeMetadata(build.Challenge)
					if err != nil {
						errs = append(errs, err)
						continue
					}

					// Resetting the flag signals to rebuild the Dockerfile
					build.Flag = ""
					err = m.executeBuild(metadata, build, buildCtxFile)
					if err != nil {
						errs = append(errs, err)
						continue
					}

					// Update database
					err = m.finalizeBuild(build)
					if err != nil {
						errs = append(errs, err)
						continue
					}

					// Recreate network and containers
					instances, err := m.getBuildInstances(build.Id)
					if err != nil {
						errs = append(errs, err)
						continue
					}
					for _, iid := range instances {
						instance, err := m.lookupInstanceMetadata(iid)
						if err == nil {
							err = m.stopContainers(instance)
						}
						if err == nil {
							err = m.stopNetwork(instance)
						}
						if err == nil {
							err = m.startNetwork(instance, cMeta.NetworkOptions)
						}
						if err == nil {
							err = m.startContainers(build, instance, cMeta.ContainerOptions)
						}
						if err != nil {
							errs = append(errs, err)
						}
					}
				}
			}
		}
	}
	return errs
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

// Database representation of ContainerOptions
// List-based options are serialized as JSON strings
type dbContainerOptions struct {
	Host            string
	Init            bool
	Cpus            string
	Memory          string
	Ulimits         string
	PidsLimit       int64
	ReadonlyRootfs  bool
	DroppedCaps     string
	NoNewPrivileges bool
	StorageOpts     string
	CgroupParent    string
}

func newFromDbContainerOptions(dbOpts dbContainerOptions) (ContainerOptions, error) {
	cOpts := ContainerOptions{}

	cOpts.Init = dbOpts.Init

	cOpts.Cpus = dbOpts.Cpus

	cOpts.Memory = dbOpts.Memory

	ulimits := make([]string, 0)
	err := json.Unmarshal([]byte(dbOpts.Ulimits), &ulimits)
	if err != nil {
		return cOpts, err
	}
	cOpts.Ulimits = ulimits

	cOpts.PidsLimit = dbOpts.PidsLimit

	cOpts.ReadonlyRootfs = dbOpts.ReadonlyRootfs

	droppedCaps := make([]string, 0)
	err = json.Unmarshal([]byte(dbOpts.DroppedCaps), &droppedCaps)
	if err != nil {
		return cOpts, err
	}
	cOpts.DroppedCaps = droppedCaps

	cOpts.NoNewPrivileges = dbOpts.NoNewPrivileges

	storageOpts := make([]string, 0)
	err = json.Unmarshal([]byte(dbOpts.StorageOpts), &storageOpts)
	if err != nil {
		return cOpts, err
	}
	cOpts.StorageOpts = storageOpts

	cOpts.CgroupParent = dbOpts.CgroupParent

	return cOpts, nil
}

func (cOpts ContainerOptions) toDbContainerOptions() (dbContainerOptions, error) {
	dbOpts := dbContainerOptions{}

	dbOpts.Init = cOpts.Init

	dbOpts.Cpus = cOpts.Cpus

	dbOpts.Memory = cOpts.Memory

	ulimitsBytes, err := json.Marshal(cOpts.Ulimits)
	if err != nil {
		return dbOpts, err
	}
	ulimits := string(ulimitsBytes)
	dbOpts.Ulimits = ulimits

	dbOpts.PidsLimit = cOpts.PidsLimit

	dbOpts.ReadonlyRootfs = cOpts.ReadonlyRootfs

	droppedCapsBytes, err := json.Marshal(cOpts.DroppedCaps)
	if err != nil {
		return dbOpts, err
	}
	droppedCaps := string(droppedCapsBytes)
	dbOpts.DroppedCaps = droppedCaps

	dbOpts.NoNewPrivileges = cOpts.NoNewPrivileges

	storageOptsBytes, err := json.Marshal(cOpts.StorageOpts)
	if err != nil {
		return dbOpts, err
	}
	storageOpts := string(storageOptsBytes)
	dbOpts.StorageOpts = storageOpts

	dbOpts.CgroupParent = cOpts.CgroupParent

	return dbOpts, nil
}

const (
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
)
