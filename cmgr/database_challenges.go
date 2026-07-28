package cmgr

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jmoiron/sqlx"
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
		err = txn.Select(&metadata.Hosts, "SELECT name, target FROM hosts WHERE challenge=? ORDER BY idx", challenge)
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
	// Note: there are currently no network-level challenge options, but they will be loaded here if added in the future.

	// if err == nil {
	// 	err = txn.Get(networkOptions, "SELECT '' FROM networkOptions WHERE challenge=?", challenge)
	// }
	metadata.ChallengeOptions.NetworkOptions = *networkOptions

	containerOptions := new([]dbContainerOptions)
	if err == nil {
		err = txn.Select(containerOptions, "SELECT host, init, cpus, memory, ulimits, pidslimit, readonlyrootfs, droppedcaps, nonewprivileges, diskquota, cgroupparent, seccomp FROM containerOptions WHERE challenge=?", challenge)
	}
	for _, dbOpts := range *containerOptions {
		var cOpts ContainerOptions
		cOpts, err = newFromDbContainerOptions(dbOpts)
		if err != nil {
			err = fmt.Errorf(
				"could not load container options for challenge %q host %q: %v",
				challenge,
				dbOpts.Host,
				err,
			)
			break
		}
		if metadata.ChallengeOptions.Overrides == nil {
			metadata.ChallengeOptions.Overrides = make(map[string]ContainerOptions)
		}
		metadata.ChallengeOptions.Overrides[dbOpts.Host] = cOpts
	}
	metadata.ChallengeOptions.ContainerOptions = metadata.ChallengeOptions.Overrides[""]

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

func (m *Manager) addChallenge(metadata *ChallengeMetadata) error {
	return withTransaction(m.db, func(txn *sqlx.Tx) error {
		if _, err := txn.NamedExec(challengeInsertQuery, metadata); err != nil {
			return fmt.Errorf("could not insert challenge metadata: %w", err)
		}

		for i, hint := range metadata.Hints {
			if _, err := txn.Exec(
				"INSERT INTO hints(challenge, idx, hint) VALUES (?, ?, ?);",
				metadata.Id,
				i,
				hint,
			); err != nil {
				return fmt.Errorf("could not insert hint %d: %w", i, err)
			}
		}

		for _, tag := range metadata.Tags {
			if _, err := txn.Exec(
				"INSERT INTO tags(challenge, tag) VALUES (?, ?);",
				metadata.Id,
				tag,
			); err != nil {
				return fmt.Errorf("could not insert tag %q: %w", tag, err)
			}
		}

		for key, value := range metadata.Attributes {
			if _, err := txn.Exec(
				"INSERT INTO attributes(challenge, key, value) VALUES (?, ?, ?);",
				metadata.Id,
				key,
				value,
			); err != nil {
				return fmt.Errorf("could not insert attribute %q: %w", key, err)
			}
		}

		for i, host := range metadata.Hosts {
			m.log.debugf("%s: %v", metadata.Id, host)
			if _, err := txn.Exec(
				"INSERT INTO hosts(challenge, name, idx, target) VALUES (?, ?, ?, ?);",
				metadata.Id,
				host.Name,
				i,
				host.Target,
			); err != nil {
				return fmt.Errorf("could not insert host %q: %w", host.Name, err)
			}
		}

		for name, endpoint := range metadata.PortMap {
			m.log.debugf("%s: %v", metadata.Id, endpoint)
			if _, err := txn.Exec(
				"INSERT INTO portNames(challenge, name, host, port) VALUES (?, ?, ?, ?);",
				metadata.Id,
				name,
				endpoint.Host,
				endpoint.Port,
			); err != nil {
				return fmt.Errorf("could not insert published port %q: %w", name, err)
			}
		}

		// There are currently no network-level challenge options. They should
		// be inserted here if network options are added in the future.

		for host, opts := range metadata.ChallengeOptions.Overrides {
			dbOpts, err := opts.toDbContainerOptions()
			if err != nil {
				return fmt.Errorf(
					"could not serialize container options for host %q: %w",
					host,
					err,
				)
			}

			hostSuffix := ""
			if host != "" {
				hostSuffix = fmt.Sprintf(" (%s)", host)
			}
			logOpts := dbOpts
			if logOpts.Seccomp != "" {
				logOpts.Seccomp = "<configured>"
			}
			m.log.debugf("%s%s: %v", metadata.Id, hostSuffix, logOpts)

			if _, err := txn.Exec(
				`INSERT INTO containerOptions(
					challenge,
					host,
					init,
					cpus,
					memory,
					ulimits,
					pidslimit,
					readonlyrootfs,
					droppedcaps,
					nonewprivileges,
					diskquota,
					cgroupparent,
					seccomp
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
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
				dbOpts.DiskQuota,
				dbOpts.CgroupParent,
				dbOpts.Seccomp,
			); err != nil {
				return fmt.Errorf(
					"could not insert container options for host %q: %w",
					host,
					err,
				)
			}
		}

		return nil
	})
}

// Adds the discovered challenges to the database and returns only additions
// that committed successfully.
func (m *Manager) addChallenges(
	addedChallenges []*ChallengeMetadata,
) ([]*ChallengeMetadata, []error) {
	added := make([]*ChallengeMetadata, 0, len(addedChallenges))
	errs := []error{}

	for _, metadata := range addedChallenges {
		if err := m.addChallenge(metadata); err != nil {
			err = fmt.Errorf("could not add challenge %q: %w", metadata.Id, err)
			m.log.error(err)
			errs = append(errs, err)
			continue
		}
		added = append(added, metadata)
	}

	return added, errs
}

func (m *Manager) updateChallenges(updatedChallenges []*ChallengeMetadata, rebuild bool) []error {
	return m.updateChallengesInternal(updatedChallenges, rebuild, true)
}

func (m *Manager) updateChallengesInternal(
	updatedChallenges []*ChallengeMetadata,
	rebuild bool,
	preflight bool,
) []error {
	errs := []error{}
	for _, metadata := range updatedChallenges {
		previousMetadata, err := m.lookupChallengeMetadata(metadata.Id)
		if err != nil {
			m.log.error(err)
			errs = append(errs, err)
			continue
		}
		if preflight {
			if err := m.preflightChallengeSeccomp(metadata); err != nil {
				m.log.error(err)
				errs = append(errs, err)
				continue
			}
		}
		txn := m.db.MustBegin()

		_, err = txn.NamedExec(challengeUpdateQuery, metadata)
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

		// Note: there are currently no network-level challenge options, but they would be updated here if added in the future.

		// _, err = txn.Exec("DELETE FROM networkOptions WHERE challenge = ?;", metadata.Id)

		// if err != nil {
		// 	m.log.error(err)
		// 	err = txn.Rollback()
		// 	if err != nil { // If rollback fails, we're in trouble.
		// 		m.log.error(err)
		// 		return append(errs, err)
		// 	}
		// 	continue
		// }

		// _, err = txn.Exec("INSERT INTO networkOptions(challenge) VALUES (?);",
		// 	metadata.Id)
		// if err != nil {
		// 	m.log.error(err)
		// 	err = txn.Rollback()
		// 	if err != nil { // If rollback fails, we're in trouble.
		// 		m.log.error(err)
		// 		return append(errs, err)
		// 	}
		// }
		// if err != nil {
		// 	continue
		// }

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

		for host, opts := range metadata.ChallengeOptions.Overrides {
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
			_, err = txn.Exec("INSERT INTO containerOptions(challenge, host, init, cpus, memory, ulimits, pidslimit, readonlyrootfs, droppedcaps, nonewprivileges, diskquota, cgroupparent, seccomp) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);",
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
				dbOpts.DiskQuota,
				dbOpts.CgroupParent,
				dbOpts.Seccomp)
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
				errs = append(
					errs,
					m.updateChallengesInternal(
						[]*ChallengeMetadata{previousMetadata},
						false,
						false,
					)...,
				)
				continue
			}

			if len(buildIds) > 0 {
				buildCtxFile, err := m.createBuildContext(metadata, m.GetDockerfile(metadata.ChallengeType))
				if err != nil {
					m.log.errorf("failed to create build context: %s", err)
					errs = append(errs, err)
					errs = append(
						errs,
						m.updateChallengesInternal(
							[]*ChallengeMetadata{previousMetadata},
							false,
							false,
						)...,
					)
					continue
				}
				defer os.Remove(buildCtxFile)

				completedUpdates := make([]*stagedBuildUpdate, 0, len(buildIds))
				challengeFailed := false
				for _, buildId := range buildIds {
					build, err := m.lookupBuildMetadata(buildId)
					if err != nil {
						errs = append(errs, err)
						challengeFailed = true
						break
					}
					candidate := cloneBuildMetadata(build)
					qualifier := fmt.Sprintf(
						"cmgr-validate-%d",
						m.rand.Int63(),
					)

					// Resetting the flag signals to rebuild the Dockerfile
					candidate.Flag = ""
					err = m.executeBuild(
						metadata,
						candidate,
						buildCtxFile,
						qualifier,
					)
					if err != nil {
						m.discardStagedBuild(metadata, candidate, qualifier)
						errs = append(errs, err)
						challengeFailed = true
						break
					}
					promotion, err := m.promoteStagedBuild(candidate, qualifier)
					if err != nil {
						m.discardStagedBuild(metadata, candidate, qualifier)
						errs = append(errs, err)
						challengeFailed = true
						break
					}
					update := &stagedBuildUpdate{
						metadata:  metadata,
						previous:  build,
						candidate: candidate,
						qualifier: qualifier,
						promotion: promotion,
					}

					// Update database
					err = m.finalizeBuild(candidate)
					if err != nil {
						errs = append(errs, err)
						errs = append(errs, m.rollbackBuildUpdate(update)...)
						challengeFailed = true
						break
					}

					// Start replacements while retaining the old containers
					// for rollback until every build has validated.
					instances, err := m.getBuildInstances(candidate.Id)
					if err != nil {
						errs = append(errs, err)
						errs = append(errs, m.rollbackBuildUpdate(update)...)
						challengeFailed = true
						break
					}
					update.cutovers = make([]*instanceCutover, 0, len(instances))
					for _, iid := range instances {
						instance, err := m.lookupInstanceMetadata(iid)
						if err == nil {
							var cutover *instanceCutover
							cutover, err = m.prepareInstanceCutover(
								candidate,
								instance,
								metadata.ChallengeOptions.Overrides,
							)
							if err == nil {
								update.cutovers = append(update.cutovers, cutover)
							}
						}
						if err != nil {
							errs = append(errs, err)
							errs = append(errs, m.rollbackBuildUpdate(update)...)
							challengeFailed = true
							break
						}
					}
					if challengeFailed {
						break
					}
					completedUpdates = append(completedUpdates, update)
				}
				if challengeFailed {
					for i := len(completedUpdates) - 1; i >= 0; i-- {
						errs = append(
							errs,
							m.rollbackBuildUpdate(completedUpdates[i])...,
						)
					}
					errs = append(
						errs,
						m.updateChallengesInternal(
							[]*ChallengeMetadata{previousMetadata},
							false,
							false,
						)...,
					)
					continue
				}
				for _, update := range completedUpdates {
					errs = append(errs, m.finishBuildUpdate(update)...)
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
	DiskQuota       string
	CgroupParent    string
	Seccomp         string
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

	cOpts.DiskQuota = dbOpts.DiskQuota

	cOpts.CgroupParent = dbOpts.CgroupParent

	cOpts.Seccomp, err = unmarshalSeccompOptions(dbOpts.Seccomp)
	if err != nil {
		return cOpts, err
	}

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

	dbOpts.DiskQuota = cOpts.DiskQuota

	dbOpts.CgroupParent = cOpts.CgroupParent

	dbOpts.Seccomp, err = marshalSeccompOptions(cOpts.Seccomp)
	if err != nil {
		return dbOpts, err
	}

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
