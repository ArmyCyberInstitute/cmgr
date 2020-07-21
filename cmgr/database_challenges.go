package cmgr

import (
	"errors"
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
