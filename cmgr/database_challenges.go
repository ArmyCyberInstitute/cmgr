package cmgr

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jmoiron/sqlx"
)

// Gets just the ID and checksum for all known challenges
func (m *Manager) listChallenges() ([]*ChallengeMetadata, error) {
	metadata := []*ChallengeMetadata{}
	err := m.db.Select(&metadata, "SELECT id, name, path, sourcechecksum, metadatachecksum, sourcedigest, metadatadigest, solvescript FROM challenges ORDER BY id;")
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
	query := fmt.Sprintf("SELECT id, name, path, sourcechecksum, metadatachecksum, sourcedigest, metadatadigest, solvescript FROM challenges WHERE id IN %s ORDER BY id;", subQuery)
	err = m.db.Select(&metadata, query, interfaceTags...)

	return metadata, err
}

func (m *Manager) lookupChallengeMetadata(challenge ChallengeId) (*ChallengeMetadata, error) {
	metadata := new(ChallengeMetadata)
	txn, err := m.db.Beginx()
	if err != nil {
		return metadata, fmt.Errorf("could not begin challenge lookup: %w", err)
	}

	err = txn.Get(metadata, "SELECT * FROM challenges WHERE id=?", challenge)
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

	if err == nil {
		err = txn.Get(
			&metadata.ChallengeOptions.NetworkOptions,
			`SELECT COALESCE(
				(SELECT allowegress FROM networkOptions WHERE challenge=?),
				0
			) AS allowegress;`,
			challenge,
		)
	}

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
			m.log.errorf("rollback failed: %s", closeErr)
			err = errors.Join(err, closeErr)
		}
	}

	return metadata, err
}

func (m *Manager) insertChallengeRelations(
	txn *sqlx.Tx,
	metadata *ChallengeMetadata,
) error {
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
	if _, err := txn.Exec(
		"INSERT INTO networkOptions(challenge, allowegress) VALUES (?, ?);",
		metadata.Id,
		metadata.ChallengeOptions.AllowEgress,
	); err != nil {
		return fmt.Errorf("could not insert network options: %w", err)
	}
	for host, opts := range metadata.ChallengeOptions.Overrides {
		dbOpts, err := opts.toDbContainerOptions()
		if err != nil {
			return fmt.Errorf(
				"could not serialize container options for host %q: %w",
				host,
				err,
			)
		}
		if _, err := txn.Exec(
			`INSERT INTO containerOptions(
				challenge, host, init, cpus, memory, ulimits, pidslimit,
				readonlyrootfs, droppedcaps, nonewprivileges, diskquota,
				cgroupparent, seccomp
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
}

func (m *Manager) addChallenge(metadata *ChallengeMetadata) error {
	return withTransaction(m.db, func(txn *sqlx.Tx) error {
		if _, err := txn.NamedExec(challengeInsertQuery, metadata); err != nil {
			return fmt.Errorf("could not insert challenge metadata: %w", err)
		}
		return m.insertChallengeRelations(txn, metadata)
	})
}

func (m *Manager) replaceChallengeMetadata(metadata *ChallengeMetadata) error {
	return withTransaction(m.db, func(txn *sqlx.Tx) error {
		result, err := txn.NamedExec(challengeUpdateQuery, metadata)
		if err != nil {
			return fmt.Errorf("could not update challenge row: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("could not inspect challenge update: %w", err)
		}
		if affected != 1 {
			return fmt.Errorf("updated %d challenge rows; expected one", affected)
		}

		for _, table := range []string{
			"hints",
			"tags",
			"attributes",
			"portNames",
			"hosts",
			"networkOptions",
			"containerOptions",
		} {
			if _, err := txn.Exec(
				fmt.Sprintf("DELETE FROM %s WHERE challenge = ?;", table),
				metadata.Id,
			); err != nil {
				return fmt.Errorf("could not clear %s: %w", table, err)
			}
		}
		return m.insertChallengeRelations(txn, metadata)
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
		persistedMetadata := metadata
		if rebuild {
			// An empty source digest is a durable "rebuild incomplete"
			// marker. DetectChanges treats it as modified, so an update
			// interrupted after this transaction will rebuild every build
			// again instead of accepting a partially cut-over generation.
			inProgressMetadata := *metadata
			inProgressMetadata.SourceDigest = ""
			persistedMetadata = &inProgressMetadata
		}
		if err := m.replaceChallengeMetadata(persistedMetadata); err != nil {
			err = fmt.Errorf("could not update challenge %q: %w", metadata.Id, err)
			m.log.error(err)
			errs = append(errs, err)
			continue
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
					randomSuffix, randomErr := randomIdentifier()
					if randomErr != nil {
						errs = append(errs, randomErr)
						challengeFailed = true
						break
					}
					qualifier := stagedBuildQualifierPrefix + randomSuffix

					// Resetting the flag signals to rebuild the Dockerfile
					candidate.Flag = ""
					err = m.executeBuild(
						metadata,
						candidate,
						buildCtxFile,
						qualifier,
					)
					if err != nil {
						errs = append(errs, err)
						if cleanupErr := m.discardStagedBuild(
							metadata,
							candidate,
							qualifier,
						); cleanupErr != nil {
							errs = append(errs, cleanupErr)
						}
						challengeFailed = true
						break
					}
					promotion, err := m.promoteStagedBuild(candidate, qualifier)
					if err != nil {
						errs = append(errs, err)
						if cleanupErr := m.discardStagedBuild(
							metadata,
							candidate,
							qualifier,
						); cleanupErr != nil {
							errs = append(errs, cleanupErr)
						}
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
				var finishErrs []error
				for _, update := range completedUpdates {
					finishErrs = append(finishErrs, m.finishBuildUpdate(update)...)
				}
				if len(finishErrs) != 0 {
					errs = append(errs, finishErrs...)
					continue
				}
			}
			if err := m.cleanupInterruptedBuildResources(
				metadata.Id,
				buildIds,
			); err != nil {
				err = fmt.Errorf(
					"could not clean interrupted update resources for challenge %q: %w",
					metadata.Id,
					err,
				)
				m.log.error(err)
				errs = append(errs, err)
				continue
			}
			if err := m.replaceChallengeMetadata(metadata); err != nil {
				err = fmt.Errorf(
					"could not complete update for challenge %q: %w",
					metadata.Id,
					err,
				)
				m.log.error(err)
				errs = append(errs, err)
			}
		}
	}
	return errs
}

func (m *Manager) removeChallenges(removedChallenges []*ChallengeMetadata) error {
	return withTransaction(m.db, func(txn *sqlx.Tx) error {
		for _, metadata := range removedChallenges {
			// Existing builds intentionally make this delete fail through the
			// foreign-key constraint.
			result, err := txn.Exec(
				"DELETE FROM challenges WHERE id = ?;",
				metadata.Id,
			)
			if err != nil {
				return fmt.Errorf("could not remove challenge %q: %w", metadata.Id, err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				return unknownChallengeIdError(metadata.Id)
			}
		}
		return nil
	})
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
			sourcedigest,
			metadatadigest,
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
			:sourcedigest,
			:metadatadigest,
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
		namespace = :namespace,
		challengetype = :challengetype,
		description = :description,
		details = :details,
			sourcechecksum = :sourcechecksum,
			metadatachecksum = :metadatachecksum,
			sourcedigest = :sourcedigest,
			metadatadigest = :metadatadigest,
		path = :path,
		solvescript = :solvescript,
		templatable = :templatable,
		maxusers = :maxusers,
		category = :category,
		points = :points
	WHERE id = :id;`
)
